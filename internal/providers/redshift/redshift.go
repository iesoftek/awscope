package redshift

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
	sdkredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/redshift/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newRedshift func(cfg awsSDK.Config) redshiftAPI
}

func New() *Provider {
	return &Provider{newRedshift: func(cfg awsSDK.Config) redshiftAPI { return sdkredshift.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "redshift" }
func (p *Provider) DisplayName() string { return "Redshift" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type redshiftAPI interface {
	DescribeClusters(ctx context.Context, params *sdkredshift.DescribeClustersInput, optFns ...func(*sdkredshift.Options)) (*sdkredshift.DescribeClustersOutput, error)
	DescribeClusterSubnetGroups(ctx context.Context, params *sdkredshift.DescribeClusterSubnetGroupsInput, optFns ...func(*sdkredshift.Options)) (*sdkredshift.DescribeClusterSubnetGroupsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("redshift provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("redshift provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newRedshift(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api redshiftAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var marker *string
	for {
		out, err := api.DescribeClusters(ctx, &sdkredshift.DescribeClustersInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, c := range out.Clusters {
			n, stubs, es := normalizeCluster(partition, accountID, region, c, now)
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
		out, err := api.DescribeClusterSubnetGroups(ctx, &sdkredshift.DescribeClusterSubnetGroupsInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, sg := range out.ClusterSubnetGroups {
			n, stubs, es := normalizeSubnetGroup(partition, accountID, region, sg, now)
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

func normalizeCluster(partition, accountID, region string, c types.Cluster, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(c.ClusterIdentifier))
	arn := strings.TrimSpace(awsToString(c.ClusterNamespaceArn))
	if arn == "" && id != "" {
		arn = fmt.Sprintf("arn:%s:redshift:%s:%s:cluster:%s", partition, region, accountID, id)
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "redshift:cluster", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":             strings.TrimSpace(awsToString(c.ClusterStatus)),
		"availabilityStatus": strings.TrimSpace(awsToString(c.ClusterAvailabilityStatus)),
		"nodeType":           strings.TrimSpace(awsToString(c.NodeType)),
		"dbName":             strings.TrimSpace(awsToString(c.DBName)),
		"public":             awsToBool(c.PubliclyAccessible),
		"encrypted":          awsToBool(c.Encrypted),
		"vpcId":              strings.TrimSpace(awsToString(c.VpcId)),
		"subnetGroup":        strings.TrimSpace(awsToString(c.ClusterSubnetGroupName)),
	}
	if c.ClusterCreateTime != nil {
		attrs["created_at"] = c.ClusterCreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(c)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(id, arn), Service: "redshift", Type: "redshift:cluster", Arn: arn, PrimaryID: firstNonEmpty(arn, id), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "redshift"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if vpc := strings.TrimSpace(awsToString(c.VpcId)); vpc != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "redshift.vpc", "direct": true}, CollectedAt: now})
	}
	if subnetGroup := strings.TrimSpace(awsToString(c.ClusterSubnetGroupName)); subnetGroup != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "redshift:subnet-group", subnetGroup)
		stubs = append(stubs, stubNode(toKey, "redshift", "redshift:subnet-group", subnetGroup, now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "redshift.subnet-group", "direct": true}, CollectedAt: now})
	}
	for _, sg := range c.VpcSecurityGroups {
		sgid := strings.TrimSpace(awsToString(sg.VpcSecurityGroupId))
		if sgid == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgid)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sgid, now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "redshift.sg", "direct": true}, CollectedAt: now})
	}
	if kms := strings.TrimSpace(awsToString(c.KmsKeyId)); kms != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
		stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "redshift.kms", "direct": true}, CollectedAt: now})
	}
	for _, role := range c.IamRoles {
		roleArn := strings.TrimSpace(awsToString(role.IamRoleArn))
		if roleArn == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "redshift.iam", "direct": true}, CollectedAt: now})
	}

	return node, stubs, edges
}

func normalizeSubnetGroup(partition, accountID, region string, sg types.ClusterSubnetGroup, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	name := strings.TrimSpace(awsToString(sg.ClusterSubnetGroupName))
	key := graph.EncodeResourceKey(partition, accountID, region, "redshift:subnet-group", name)
	attrs := map[string]any{
		"status":      strings.TrimSpace(awsToString(sg.SubnetGroupStatus)),
		"description": strings.TrimSpace(awsToString(sg.Description)),
		"vpcId":       strings.TrimSpace(awsToString(sg.VpcId)),
	}
	if len(sg.SupportedClusterIpAddressTypes) > 0 {
		attrs["supportedIpAddressTypes"] = sg.SupportedClusterIpAddressTypes
	}
	raw, _ := json.Marshal(sg)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "redshift",
		Type:        "redshift:subnet-group",
		PrimaryID:   name,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "redshift",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if vpc := strings.TrimSpace(awsToString(sg.VpcId)); vpc != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "redshift.subnet-group.vpc", "direct": true}, CollectedAt: now})
	}
	for _, sn := range sg.Subnets {
		subnetID := strings.TrimSpace(awsToString(sn.SubnetIdentifier))
		if subnetID == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnetID, now, "redshift"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "contains", Meta: map[string]any{"source": "redshift.subnet-group.subnet", "direct": true}, CollectedAt: now})
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
