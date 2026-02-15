package secretsmanager

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
	sdksec "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSM func(cfg awsSDK.Config) smAPI
}

func New() *Provider {
	return &Provider{newSM: func(cfg awsSDK.Config) smAPI { return sdksec.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "secretsmanager" }
func (p *Provider) DisplayName() string { return "Secrets Manager" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type smAPI interface {
	ListSecrets(ctx context.Context, params *sdksec.ListSecretsInput, optFns ...func(*sdksec.Options)) (*sdksec.ListSecretsOutput, error)
	DescribeSecret(ctx context.Context, params *sdksec.DescribeSecretInput, optFns ...func(*sdksec.Options)) (*sdksec.DescribeSecretOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("secretsmanager provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("secretsmanager provider requires account identity")
	}
	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newSM(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api smAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var next *string
	for {
		resp, err := api.ListSecrets(ctx, &sdksec.ListSecretsInput{NextToken: next})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range resp.SecretList {
			arn := awsToString(s.ARN)
			name := awsToString(s.Name)
			if arn == "" {
				continue
			}
			if name == "" {
				name = arn
			}
			key := graph.EncodeResourceKey(partition, accountID, region, "secretsmanager:secret", arn)
			raw, _ := json.Marshal(s)
			attrs := map[string]any{
				"rotationEnabled": s.RotationEnabled,
			}
			if s.CreatedDate != nil && !s.CreatedDate.IsZero() {
				attrs["created_at"] = s.CreatedDate.UTC().Format("2006-01-02 15:04")
			}
			if s.LastChangedDate != nil && !s.LastChangedDate.IsZero() {
				attrs["lastChanged"] = s.LastChangedDate.UTC().Format("2006-01-02 15:04")
			}
			if k := awsToString(s.KmsKeyId); k != "" {
				attrs["kmsKeyId"] = k
			}
			node := graph.ResourceNode{
				Key:         key,
				DisplayName: name,
				Service:     "secretsmanager",
				Type:        "secretsmanager:secret",
				Arn:         arn,
				PrimaryID:   arn,
				Tags:        map[string]string{},
				Attributes:  attrs,
				Raw:         raw,
				CollectedAt: now,
				Source:      "secretsmanager",
			}
			nodes = append(nodes, node)

			// Secret -> KMS key.
			if k := strings.TrimSpace(awsToString(s.KmsKeyId)); k != "" {
				toKey, ok := kmsRefToKey(partition, accountID, region, k)
				if ok {
					edges = append(edges, graph.RelationshipEdge{
						From:        key,
						To:          toKey,
						Kind:        "uses",
						Meta:        map[string]any{"direct": true, "source": "secretsmanager.kms"},
						CollectedAt: now,
					})
				}
			}

			// Rotation Lambda: requires DescribeSecret for the ARN.
			desc, err := api.DescribeSecret(ctx, &sdksec.DescribeSecretInput{SecretId: &arn})
			if err == nil {
				if rl := strings.TrimSpace(awsToString(desc.RotationLambdaARN)); rl != "" {
					if toKey, ok := lambdaArnToKey(partition, accountID, region, rl); ok {
						edges = append(edges, graph.RelationshipEdge{
							From:        key,
							To:          toKey,
							Kind:        "uses",
							Meta:        map[string]any{"direct": true, "source": "secretsmanager.rotation"},
							CollectedAt: now,
						})
					}
				}
			}
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		next = resp.NextToken
	}

	return nodes, edges, nil
}

func lambdaArnToKey(partition, accountID, fallbackRegion, arn string) (graph.ResourceKey, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", false
	}
	r := parts[3]
	if r == "" {
		r = fallbackRegion
	}
	return graph.EncodeResourceKey(partition, accountID, r, "lambda:function", arn), true
}

func kmsRefToKey(partition, accountID, fallbackRegion, ref string) (graph.ResourceKey, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:alias", ref), true
	}
	if strings.HasPrefix(ref, "arn:") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:key", ref), true
	}
	if strings.HasPrefix(ref, "alias/") {
		arn := fmt.Sprintf("arn:%s:kms:%s:%s:%s", partition, fallbackRegion, accountID, ref)
		return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:alias", arn), true
	}
	return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:key", ref), true
}

func arnRegion(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
