package accessanalyzer

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
	sdkaccess "github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newAccessAnalyzer func(cfg awsSDK.Config) accessAnalyzerAPI
}

func New() *Provider {
	return &Provider{newAccessAnalyzer: func(cfg awsSDK.Config) accessAnalyzerAPI { return sdkaccess.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "accessanalyzer" }
func (p *Provider) DisplayName() string { return "IAM Access Analyzer" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type accessAnalyzerAPI interface {
	ListAnalyzers(ctx context.Context, params *sdkaccess.ListAnalyzersInput, optFns ...func(*sdkaccess.Options)) (*sdkaccess.ListAnalyzersOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("accessanalyzer provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("accessanalyzer provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newAccessAnalyzer(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api accessAnalyzerAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var nodes []graph.ResourceNode
	var nextToken *string
	for {
		out, err := api.ListAnalyzers(ctx, &sdkaccess.ListAnalyzersInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, a := range out.Analyzers {
			nodes = append(nodes, normalizeAnalyzer(partition, accountID, region, a, now))
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, nil, nil
}

func normalizeAnalyzer(partition, accountID, region string, a types.AnalyzerSummary, now time.Time) graph.ResourceNode {
	arn := strings.TrimSpace(awsToString(a.Arn))
	name := strings.TrimSpace(awsToString(a.Name))
	primary := firstNonEmpty(arn, name)
	key := graph.EncodeResourceKey(partition, accountID, region, "accessanalyzer:analyzer", primary)
	attrs := map[string]any{
		"status": strings.TrimSpace(string(a.Status)),
		"type":   strings.TrimSpace(string(a.Type)),
	}
	if a.CreatedAt != nil {
		attrs["created_at"] = a.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	if a.LastResourceAnalyzedAt != nil {
		attrs["lastResourceAnalyzedAt"] = a.LastResourceAnalyzedAt.UTC().Format("2006-01-02 15:04")
	}
	if s := strings.TrimSpace(awsToString(a.LastResourceAnalyzed)); s != "" {
		attrs["lastResourceAnalyzed"] = s
	}
	raw, _ := json.Marshal(a)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, arn),
		Service:     "accessanalyzer",
		Type:        "accessanalyzer:analyzer",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "accessanalyzer",
	}
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
