package ecr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newECR func(cfg awsSDK.Config) ecrAPI
}

func New() *Provider {
	return &Provider{newECR: func(cfg awsSDK.Config) ecrAPI { return sdkecr.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "ecr" }
func (p *Provider) DisplayName() string { return "ECR" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type ecrAPI interface {
	DescribeRepositories(ctx context.Context, params *sdkecr.DescribeRepositoriesInput, optFns ...func(*sdkecr.Options)) (*sdkecr.DescribeRepositoriesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("ecr provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("ecr provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newECR(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api ecrAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var nextToken *string
	for {
		out, err := api.DescribeRepositories(ctx, &sdkecr.DescribeRepositoriesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, r := range out.Repositories {
			n, stubs, es := normalizeRepository(partition, accountID, region, r, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}
	return nodes, edges, nil
}

func normalizeRepository(partition, accountID, region string, r types.Repository, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(r.RepositoryArn))
	name := strings.TrimSpace(awsToString(r.RepositoryName))
	key := graph.EncodeResourceKey(partition, accountID, region, "ecr:repository", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status":             "active",
		"repositoryUri":      strings.TrimSpace(awsToString(r.RepositoryUri)),
		"imageTagMutability": strings.TrimSpace(string(r.ImageTagMutability)),
	}
	if r.CreatedAt != nil {
		attrs["created_at"] = r.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	if r.EncryptionConfiguration != nil {
		attrs["encryptionType"] = string(r.EncryptionConfiguration.EncryptionType)
	}
	raw, _ := json.Marshal(r)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, arn),
		Service:     "ecr",
		Type:        "ecr:repository",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(arn, name),
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ecr",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if r.EncryptionConfiguration != nil {
		if kms := strings.TrimSpace(awsToString(r.EncryptionConfiguration.KmsKey)); kms != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
			stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "ecr"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "ecr.kms"}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{Key: key, DisplayName: display, Service: service, Type: typ, PrimaryID: primaryID, Tags: map[string]string{}, Attributes: map[string]any{"stub": true}, Raw: []byte(`{}`), CollectedAt: now, Source: source}
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}
