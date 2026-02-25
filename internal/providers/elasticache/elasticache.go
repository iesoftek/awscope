package elasticache

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
	sdkelasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newElastiCache func(cfg awsSDK.Config) elastiCacheAPI
}

func New() *Provider {
	return &Provider{newElastiCache: func(cfg awsSDK.Config) elastiCacheAPI { return sdkelasticache.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "elasticache" }
func (p *Provider) DisplayName() string { return "ElastiCache" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type elastiCacheAPI interface {
	DescribeReplicationGroups(ctx context.Context, params *sdkelasticache.DescribeReplicationGroupsInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeReplicationGroupsOutput, error)
	DescribeCacheClusters(ctx context.Context, params *sdkelasticache.DescribeCacheClustersInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeCacheClustersOutput, error)
	DescribeCacheSubnetGroups(ctx context.Context, params *sdkelasticache.DescribeCacheSubnetGroupsInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeCacheSubnetGroupsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("elasticache provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("elasticache provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newElastiCache(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api elastiCacheAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var marker *string
	for {
		out, err := api.DescribeReplicationGroups(ctx, &sdkelasticache.DescribeReplicationGroupsInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, rg := range out.ReplicationGroups {
			n, stubs, es := normalizeReplicationGroup(partition, accountID, region, rg, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.Marker == nil || strings.TrimSpace(*out.Marker) == "" {
			break
		}
		marker = out.Marker
	}

	marker = nil
	for {
		out, err := api.DescribeCacheClusters(ctx, &sdkelasticache.DescribeCacheClustersInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, cc := range out.CacheClusters {
			n, stubs, es := normalizeCacheCluster(partition, accountID, region, cc, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.Marker == nil || strings.TrimSpace(*out.Marker) == "" {
			break
		}
		marker = out.Marker
	}

	marker = nil
	for {
		out, err := api.DescribeCacheSubnetGroups(ctx, &sdkelasticache.DescribeCacheSubnetGroupsInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, sg := range out.CacheSubnetGroups {
			n, stubs, es := normalizeCacheSubnetGroup(partition, accountID, region, sg, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.Marker == nil || strings.TrimSpace(*out.Marker) == "" {
			break
		}
		marker = out.Marker
	}

	return nodes, edges, nil
}

func normalizeReplicationGroup(partition, accountID, region string, rg types.ReplicationGroup, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(rg.ReplicationGroupId))
	arn := strings.TrimSpace(awsToString(rg.ARN))
	key := graph.EncodeResourceKey(partition, accountID, region, "elasticache:replication-group", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":      strings.TrimSpace(awsToString(rg.Status)),
		"engine":      strings.TrimSpace(awsToString(rg.Engine)),
		"nodeType":    strings.TrimSpace(awsToString(rg.CacheNodeType)),
		"clusterMode": strings.TrimSpace(string(rg.ClusterMode)),
		"multiAz":     strings.TrimSpace(string(rg.MultiAZ)),
	}
	if rg.ReplicationGroupCreateTime != nil {
		attrs["created_at"] = rg.ReplicationGroupCreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(rg)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(id, arn), Service: "elasticache", Type: "elasticache:replication-group", Arn: arn, PrimaryID: firstNonEmpty(arn, id), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "elasticache"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if kms := strings.TrimSpace(awsToString(rg.KmsKeyId)); kms != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
		stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "elasticache.kms", "direct": true}, CollectedAt: now})
	}
	for _, member := range rg.MemberClusters {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "elasticache:cache-cluster", member)
		stubs = append(stubs, stubNode(toKey, "elasticache", "elasticache:cache-cluster", member, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "contains", Meta: map[string]any{"source": "elasticache.members", "direct": true}, CollectedAt: now})
	}

	return node, stubs, edges
}

func normalizeCacheCluster(partition, accountID, region string, cc types.CacheCluster, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(cc.CacheClusterId))
	arn := strings.TrimSpace(awsToString(cc.ARN))
	key := graph.EncodeResourceKey(partition, accountID, region, "elasticache:cache-cluster", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":           strings.TrimSpace(awsToString(cc.CacheClusterStatus)),
		"engine":           strings.TrimSpace(awsToString(cc.Engine)),
		"engineVersion":    strings.TrimSpace(awsToString(cc.EngineVersion)),
		"nodeType":         strings.TrimSpace(awsToString(cc.CacheNodeType)),
		"subnetGroup":      strings.TrimSpace(awsToString(cc.CacheSubnetGroupName)),
		"numCacheNodes":    cc.NumCacheNodes,
		"replicationGroup": strings.TrimSpace(awsToString(cc.ReplicationGroupId)),
	}
	if cc.CacheClusterCreateTime != nil {
		attrs["created_at"] = cc.CacheClusterCreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(cc)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(id, arn), Service: "elasticache", Type: "elasticache:cache-cluster", Arn: arn, PrimaryID: firstNonEmpty(arn, id), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "elasticache"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if rg := strings.TrimSpace(awsToString(cc.ReplicationGroupId)); rg != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "elasticache:replication-group", rg)
		stubs = append(stubs, stubNode(toKey, "elasticache", "elasticache:replication-group", rg, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "elasticache.replication-group", "direct": true}, CollectedAt: now})
	}
	if subnetGroup := strings.TrimSpace(awsToString(cc.CacheSubnetGroupName)); subnetGroup != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "elasticache:subnet-group", subnetGroup)
		stubs = append(stubs, stubNode(toKey, "elasticache", "elasticache:subnet-group", subnetGroup, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "elasticache.subnet-group", "direct": true}, CollectedAt: now})
	}
	for _, sg := range cc.SecurityGroups {
		sgid := strings.TrimSpace(awsToString(sg.SecurityGroupId))
		if sgid == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgid)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sgid, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "elasticache.sg", "direct": true}, CollectedAt: now})
	}

	return node, stubs, edges
}

func normalizeCacheSubnetGroup(partition, accountID, region string, sg types.CacheSubnetGroup, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	name := strings.TrimSpace(awsToString(sg.CacheSubnetGroupName))
	arn := strings.TrimSpace(awsToString(sg.ARN))
	key := graph.EncodeResourceKey(partition, accountID, region, "elasticache:subnet-group", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status":      "available",
		"description": strings.TrimSpace(awsToString(sg.CacheSubnetGroupDescription)),
		"vpc":         strings.TrimSpace(awsToString(sg.VpcId)),
	}
	if len(sg.SupportedNetworkTypes) > 0 {
		nts := make([]string, 0, len(sg.SupportedNetworkTypes))
		for _, nt := range sg.SupportedNetworkTypes {
			nts = append(nts, strings.TrimSpace(string(nt)))
		}
		attrs["networkTypes"] = nts
	}
	raw, _ := json.Marshal(sg)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, arn),
		Service:     "elasticache",
		Type:        "elasticache:subnet-group",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(arn, name),
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "elasticache",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if vpc := strings.TrimSpace(awsToString(sg.VpcId)); vpc != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "elasticache.subnet-group.vpc", "direct": true}, CollectedAt: now})
	}
	for _, sn := range sg.Subnets {
		subnetID := strings.TrimSpace(awsToString(sn.SubnetIdentifier))
		if subnetID == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnetID, now, "elasticache"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "contains", Meta: map[string]any{"source": "elasticache.subnet-group.subnet", "direct": true}, CollectedAt: now})
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
