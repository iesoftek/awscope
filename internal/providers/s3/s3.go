package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdks3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newS3 func(cfg awsSDK.Config) s3API
}

func New() *Provider {
	return &Provider{
		newS3: func(cfg awsSDK.Config) s3API { return sdks3.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "s3" }
func (p *Provider) DisplayName() string { return "S3" }
func (p *Provider) Scope() providers.ScopeKind {
	// Bucket listing is account-scoped, but buckets have a real region.
	return providers.ScopeAccount
}

type s3API interface {
	ListBuckets(ctx context.Context, params *sdks3.ListBucketsInput, optFns ...func(*sdks3.Options)) (*sdks3.ListBucketsOutput, error)
	GetBucketLocation(ctx context.Context, params *sdks3.GetBucketLocationInput, optFns ...func(*sdks3.Options)) (*sdks3.GetBucketLocationOutput, error)
	GetBucketEncryption(ctx context.Context, params *sdks3.GetBucketEncryptionInput, optFns ...func(*sdks3.Options)) (*sdks3.GetBucketEncryptionOutput, error)
	GetPublicAccessBlock(ctx context.Context, params *sdks3.GetPublicAccessBlockInput, optFns ...func(*sdks3.Options)) (*sdks3.GetPublicAccessBlockOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("s3 provider requires account identity")
	}
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("s3 provider requires at least one region")
	}

	// S3 bucket list is global; the SDK still expects a region for endpoint resolution.
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	api := p.newS3(cfg)
	now := time.Now().UTC()

	allowed := map[string]bool{}
	for _, r := range req.Regions {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		allowed[r] = true
	}

	out, err := api.ListBuckets(ctx, &sdks3.ListBucketsInput{})
	if err != nil {
		return providers.ListResult{}, err
	}

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	for _, b := range out.Buckets {
		name := awsToString(b.Name)
		if name == "" {
			continue
		}
		region, err := bucketRegion(ctx, api, name)
		if err != nil {
			return providers.ListResult{}, err
		}
		if !allowed[region] {
			continue
		}

		n, es := normalizeBucket(req.Partition, req.AccountID, region, b, now)
		nodes = append(nodes, n)
		edges = append(edges, es...)

		// Best-effort encryption + public access block posture.
		encAttrs, encEdges, _ := bucketEncryption(ctx, api, req.Partition, req.AccountID, region, name, n.Key, now)
		for k, v := range encAttrs {
			n.Attributes[k] = v
		}
		edges = append(edges, encEdges...)

		pabAttrs, _ := bucketPublicAccessBlock(ctx, api, name)
		for k, v := range pabAttrs {
			n.Attributes[k] = v
		}
		// Update the node with posture details.
		nodes[len(nodes)-1] = n
	}

	return providers.ListResult{Nodes: nodes, Edges: edges}, nil
}

func normalizeBucket(partition, accountID, region string, b types.Bucket, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(b.Name)
	key := graph.EncodeResourceKey(partition, accountID, region, "s3:bucket", name)
	arn := ""
	if name != "" {
		arn = fmt.Sprintf("arn:%s:s3:::%s", partition, name)
	}

	attrs := map[string]any{
		"region": region,
	}
	if b.CreationDate != nil && !b.CreationDate.IsZero() {
		attrs["created_at"] = b.CreationDate.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(b)

	return graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "s3",
		Type:        "s3:bucket",
		Arn:         arn,
		PrimaryID:   name,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "s3",
	}, nil
}

func bucketRegion(ctx context.Context, api s3API, bucket string) (string, error) {
	resp, err := api.GetBucketLocation(ctx, &sdks3.GetBucketLocationInput{Bucket: &bucket})
	if err != nil {
		return "", err
	}
	loc := string(resp.LocationConstraint)
	switch strings.TrimSpace(loc) {
	case "", "US":
		return "us-east-1", nil
	case "EU":
		// Legacy constraint value maps to eu-west-1.
		return "eu-west-1", nil
	default:
		return loc, nil
	}
}

func bucketEncryption(ctx context.Context, api s3API, partition, accountID, region, bucket string, bucketKey graph.ResourceKey, now time.Time) (map[string]any, []graph.RelationshipEdge, error) {
	attrs := map[string]any{}
	resp, err := api.GetBucketEncryption(ctx, &sdks3.GetBucketEncryptionInput{Bucket: &bucket})
	if err != nil {
		if isS3NotFound(err, "ServerSideEncryptionConfigurationNotFoundError") {
			return attrs, nil, nil
		}
		return nil, nil, err
	}

	var edges []graph.RelationshipEdge
	rules := resp.ServerSideEncryptionConfiguration.Rules
	if len(rules) == 0 {
		return attrs, nil, nil
	}
	// Record the first rule for quick filtering; keep raw details in attributes.
	rule := rules[0]
	byDefault := rule.ApplyServerSideEncryptionByDefault
	if byDefault != nil {
		algo := string(byDefault.SSEAlgorithm)
		if algo != "" {
			attrs["encryption"] = algo
		}
		k := awsToString(byDefault.KMSMasterKeyID)
		if k != "" {
			attrs["kms_master_key_id"] = k
			// Link to KMS for graph navigation.
			toKey, ok := kmsRefToKey(partition, accountID, region, k)
			if ok {
				edges = append(edges, graph.RelationshipEdge{
					From:        bucketKey,
					To:          toKey,
					Kind:        "uses",
					Meta:        map[string]any{"direct": true, "source": "s3.encryption"},
					CollectedAt: now,
				})
			}
		}
	}
	return attrs, edges, nil
}

func bucketPublicAccessBlock(ctx context.Context, api s3API, bucket string) (map[string]any, error) {
	resp, err := api.GetPublicAccessBlock(ctx, &sdks3.GetPublicAccessBlockInput{Bucket: &bucket})
	if err != nil {
		if isS3NotFound(err, "NoSuchPublicAccessBlockConfiguration") {
			return map[string]any{"public_access_block": "not-configured"}, nil
		}
		return nil, err
	}
	if resp.PublicAccessBlockConfiguration == nil {
		return nil, nil
	}
	c := resp.PublicAccessBlockConfiguration
	return map[string]any{
		"public_access_block": map[string]any{
			"block_public_acls":       c.BlockPublicAcls,
			"ignore_public_acls":      c.IgnorePublicAcls,
			"block_public_policy":     c.BlockPublicPolicy,
			"restrict_public_buckets": c.RestrictPublicBuckets,
		},
	}, nil
}

func kmsRefToKey(partition, accountID, fallbackRegion, ref string) (graph.ResourceKey, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}

	// Alias ARN.
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":kms:") && strings.Contains(ref, ":alias/") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:alias", ref), true
	}

	// Key ARN.
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":kms:") && strings.Contains(ref, ":key/") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:key", ref), true
	}

	// Alias name (alias/foo) -> build ARN.
	if strings.HasPrefix(ref, "alias/") {
		arn := fmt.Sprintf("arn:%s:kms:%s:%s:%s", partition, fallbackRegion, accountID, ref)
		return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:alias", arn), true
	}

	// Key ID -> best-effort (won't join to kms:key unless KMS provider uses the same).
	return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:key", ref), true
}

func arnRegion(arn string) string {
	// arn:partition:service:region:account:...
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

func isS3NotFound(err error, code string) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == code
	}
	return false
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
