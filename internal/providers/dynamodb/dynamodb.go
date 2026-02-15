package dynamodb

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
	sdkddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newDDB func(cfg awsSDK.Config) ddbAPI
}

func New() *Provider {
	return &Provider{newDDB: func(cfg awsSDK.Config) ddbAPI { return sdkddb.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "dynamodb" }
func (p *Provider) DisplayName() string { return "DynamoDB" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type ddbAPI interface {
	ListTables(ctx context.Context, params *sdkddb.ListTablesInput, optFns ...func(*sdkddb.Options)) (*sdkddb.ListTablesOutput, error)
	DescribeTable(ctx context.Context, params *sdkddb.DescribeTableInput, optFns ...func(*sdkddb.Options)) (*sdkddb.DescribeTableOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("dynamodb provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("dynamodb provider requires account identity")
	}
	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newDDB(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api ddbAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var start *string
	for {
		resp, err := api.ListTables(ctx, &sdkddb.ListTablesInput{ExclusiveStartTableName: start})
		if err != nil {
			return nil, nil, err
		}
		for _, name := range resp.TableNames {
			n, es, err := p.describeAndNormalize(ctx, api, partition, accountID, region, name, now)
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if resp.LastEvaluatedTableName == nil || *resp.LastEvaluatedTableName == "" {
			break
		}
		start = resp.LastEvaluatedTableName
	}

	return nodes, edges, nil
}

func (p *Provider) describeAndNormalize(ctx context.Context, api ddbAPI, partition, accountID, region, name string, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge, error) {
	resp, err := api.DescribeTable(ctx, &sdkddb.DescribeTableInput{TableName: &name})
	if err != nil {
		return graph.ResourceNode{}, nil, err
	}
	if resp.Table == nil {
		return graph.ResourceNode{}, nil, fmt.Errorf("describe table: missing table")
	}
	t := *resp.Table

	arn := awsToString(t.TableArn)
	primary := arn
	if primary == "" {
		primary = name
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "dynamodb:table", primary)

	attrs := map[string]any{
		"status": string(t.TableStatus),
	}
	if t.CreationDateTime != nil && !t.CreationDateTime.IsZero() {
		attrs["created_at"] = t.CreationDateTime.UTC().Format("2006-01-02 15:04")
	}
	if t.BillingModeSummary != nil {
		attrs["billingMode"] = string(t.BillingModeSummary.BillingMode)
	}
	if t.SSEDescription != nil {
		attrs["sseStatus"] = string(t.SSEDescription.Status)
		if k := awsToString(t.SSEDescription.KMSMasterKeyArn); k != "" {
			attrs["kmsKeyArn"] = k
		}
	}

	raw, _ := json.Marshal(t)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "dynamodb",
		Type:        "dynamodb:table",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "dynamodb",
	}

	var edges []graph.RelationshipEdge
	if t.SSEDescription != nil {
		if k := awsToString(t.SSEDescription.KMSMasterKeyArn); k != "" {
			toKey, ok := kmsRefToKey(partition, accountID, region, k)
			if ok {
				edges = append(edges, graph.RelationshipEdge{
					From:        key,
					To:          toKey,
					Kind:        "uses",
					Meta:        map[string]any{"direct": true, "source": "dynamodb.sse"},
					CollectedAt: now,
				})
			}
		}
	}

	return node, edges, nil
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
