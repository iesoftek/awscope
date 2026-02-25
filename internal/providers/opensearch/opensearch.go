package opensearch

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
	sdkopensearch "github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newOpenSearch func(cfg awsSDK.Config) openSearchAPI
}

func New() *Provider {
	return &Provider{newOpenSearch: func(cfg awsSDK.Config) openSearchAPI { return sdkopensearch.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "opensearch" }
func (p *Provider) DisplayName() string { return "OpenSearch" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type openSearchAPI interface {
	ListDomainNames(ctx context.Context, params *sdkopensearch.ListDomainNamesInput, optFns ...func(*sdkopensearch.Options)) (*sdkopensearch.ListDomainNamesOutput, error)
	DescribeDomain(ctx context.Context, params *sdkopensearch.DescribeDomainInput, optFns ...func(*sdkopensearch.Options)) (*sdkopensearch.DescribeDomainOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("opensearch provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("opensearch provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newOpenSearch(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api openSearchAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	list, err := api.ListDomainNames(ctx, &sdkopensearch.ListDomainNamesInput{})
	if err != nil {
		return nil, nil, err
	}
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	for _, info := range list.DomainNames {
		name := strings.TrimSpace(awsToString(info.DomainName))
		if name == "" {
			continue
		}
		desc, err := api.DescribeDomain(ctx, &sdkopensearch.DescribeDomainInput{DomainName: awsSDK.String(name)})
		if err != nil {
			return nil, nil, err
		}
		if desc.DomainStatus == nil {
			continue
		}
		n, stubs, es := normalizeDomain(partition, accountID, region, *desc.DomainStatus, now)
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}
	return nodes, edges, nil
}

func normalizeDomain(partition, accountID, region string, d types.DomainStatus, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(d.ARN))
	name := strings.TrimSpace(awsToString(d.DomainName))
	key := graph.EncodeResourceKey(partition, accountID, region, "opensearch:domain", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status":       strings.TrimSpace(string(d.DomainProcessingStatus)),
		"engine":       strings.TrimSpace(awsToString(d.EngineVersion)),
		"processing":   awsToBool(d.Processing),
		"created":      awsToBool(d.Created),
		"deleted":      awsToBool(d.Deleted),
		"endpoint":     strings.TrimSpace(awsToString(d.Endpoint)),
		"endpointV2":   strings.TrimSpace(awsToString(d.EndpointV2)),
		"publicDomain": d.VPCOptions == nil,
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "opensearch", Type: "opensearch:domain", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "opensearch"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if d.VPCOptions != nil {
		if vpc := strings.TrimSpace(awsToString(d.VPCOptions.VPCId)); vpc != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "opensearch"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "opensearch.vpc", "direct": true}, CollectedAt: now})
		}
		for _, subnet := range d.VPCOptions.SubnetIds {
			subnet = strings.TrimSpace(subnet)
			if subnet == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnet, now, "opensearch"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "opensearch.subnet", "direct": true}, CollectedAt: now})
		}
		for _, sg := range d.VPCOptions.SecurityGroupIds {
			sg = strings.TrimSpace(sg)
			if sg == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "opensearch"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "opensearch.sg", "direct": true}, CollectedAt: now})
		}
	}
	if d.EncryptionAtRestOptions != nil {
		if kms := strings.TrimSpace(awsToString(d.EncryptionAtRestOptions.KmsKeyId)); kms != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
			stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "opensearch"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "opensearch.kms", "direct": true}, CollectedAt: now})
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

func awsToBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
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
