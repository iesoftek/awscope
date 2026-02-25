package wafv2

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
	sdkwaf "github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newWAF func(cfg awsSDK.Config) wafAPI
}

func New() *Provider {
	return &Provider{newWAF: func(cfg awsSDK.Config) wafAPI { return sdkwaf.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "wafv2" }
func (p *Provider) DisplayName() string { return "WAFv2" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type wafAPI interface {
	ListWebACLs(ctx context.Context, params *sdkwaf.ListWebACLsInput, optFns ...func(*sdkwaf.Options)) (*sdkwaf.ListWebACLsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("wafv2 provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("wafv2 provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newWAF(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api wafAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode

	regional, err := listScopeWebACLs(ctx, api, partition, accountID, region, types.ScopeRegional, now)
	if err != nil {
		return nil, nil, err
	}
	nodes = append(nodes, regional...)

	// CloudFront-scoped WAFv2 APIs must be called in us-east-1.
	if region == "us-east-1" {
		globalACLs, err := listScopeWebACLs(ctx, api, partition, accountID, "global", types.ScopeCloudfront, now)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, globalACLs...)
	}

	return nodes, nil, nil
}

func listScopeWebACLs(ctx context.Context, api wafAPI, partition, accountID, nodeRegion string, scope types.Scope, now time.Time) ([]graph.ResourceNode, error) {
	var nodes []graph.ResourceNode
	var nextMarker *string
	for {
		out, err := api.ListWebACLs(ctx, &sdkwaf.ListWebACLsInput{Scope: scope, NextMarker: nextMarker, Limit: awsSDK.Int32(100)})
		if err != nil {
			return nil, err
		}
		for _, w := range out.WebACLs {
			nodes = append(nodes, normalizeWebACL(partition, accountID, nodeRegion, scope, w, now))
		}
		if out.NextMarker == nil || strings.TrimSpace(*out.NextMarker) == "" {
			break
		}
		nextMarker = out.NextMarker
	}
	return nodes, nil
}

func normalizeWebACL(partition, accountID, region string, scope types.Scope, w types.WebACLSummary, now time.Time) graph.ResourceNode {
	arn := strings.TrimSpace(awsToString(w.ARN))
	name := strings.TrimSpace(awsToString(w.Name))
	id := strings.TrimSpace(awsToString(w.Id))
	primary := firstNonEmpty(arn, id, name)
	key := graph.EncodeResourceKey(partition, accountID, region, "wafv2:web-acl", primary)
	attrs := map[string]any{
		"status":      "active",
		"scope":       string(scope),
		"description": strings.TrimSpace(awsToString(w.Description)),
		"id":          id,
	}
	raw, _ := json.Marshal(w)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, id, arn),
		Service:     "wafv2",
		Type:        "wafv2:web-acl",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "wafv2",
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
