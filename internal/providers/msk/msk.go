package msk

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
	sdkkafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newMSK func(cfg awsSDK.Config) mskAPI
}

func New() *Provider {
	return &Provider{newMSK: func(cfg awsSDK.Config) mskAPI { return sdkkafka.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "msk" }
func (p *Provider) DisplayName() string { return "MSK" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type mskAPI interface {
	ListClustersV2(ctx context.Context, params *sdkkafka.ListClustersV2Input, optFns ...func(*sdkkafka.Options)) (*sdkkafka.ListClustersV2Output, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("msk provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("msk provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newMSK(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api mskAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var nextToken *string
	for {
		out, err := api.ListClustersV2(ctx, &sdkkafka.ListClustersV2Input{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, c := range out.ClusterInfoList {
			n, stubs, es := normalizeCluster(partition, accountID, region, c, now)
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

func normalizeCluster(partition, accountID, region string, c types.Cluster, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(c.ClusterArn))
	name := strings.TrimSpace(awsToString(c.ClusterName))
	key := graph.EncodeResourceKey(partition, accountID, region, "msk:cluster", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status":         strings.TrimSpace(string(c.State)),
		"clusterType":    strings.TrimSpace(string(c.ClusterType)),
		"currentVersion": strings.TrimSpace(awsToString(c.CurrentVersion)),
	}
	if c.CreationTime != nil {
		attrs["created_at"] = c.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	if c.Provisioned != nil {
		attrs["brokerNodes"] = c.Provisioned.NumberOfBrokerNodes
		if c.Provisioned.BrokerNodeGroupInfo != nil {
			attrs["instanceType"] = strings.TrimSpace(awsToString(c.Provisioned.BrokerNodeGroupInfo.InstanceType))
		}
	}
	raw, _ := json.Marshal(c)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "msk", Type: "msk:cluster", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "msk"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if c.Provisioned != nil {
		if c.Provisioned.BrokerNodeGroupInfo != nil {
			for _, subnet := range c.Provisioned.BrokerNodeGroupInfo.ClientSubnets {
				subnet = strings.TrimSpace(subnet)
				if subnet == "" {
					continue
				}
				toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
				stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnet, now, "msk"))
				edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "msk.subnet", "direct": true}, CollectedAt: now})
			}
			for _, sg := range c.Provisioned.BrokerNodeGroupInfo.SecurityGroups {
				sg = strings.TrimSpace(sg)
				if sg == "" {
					continue
				}
				toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
				stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "msk"))
				edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "msk.sg", "direct": true}, CollectedAt: now})
			}
		}
		if c.Provisioned.EncryptionInfo != nil && c.Provisioned.EncryptionInfo.EncryptionAtRest != nil {
			if kms := strings.TrimSpace(awsToString(c.Provisioned.EncryptionInfo.EncryptionAtRest.DataVolumeKMSKeyId)); kms != "" {
				toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
				stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "msk"))
				edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "msk.kms", "direct": true}, CollectedAt: now})
			}
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
