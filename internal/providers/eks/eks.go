package eks

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
	sdkeks "github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newEKS func(cfg awsSDK.Config) eksAPI
}

func New() *Provider {
	return &Provider{newEKS: func(cfg awsSDK.Config) eksAPI { return sdkeks.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "eks" }
func (p *Provider) DisplayName() string { return "EKS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type eksAPI interface {
	ListClusters(ctx context.Context, params *sdkeks.ListClustersInput, optFns ...func(*sdkeks.Options)) (*sdkeks.ListClustersOutput, error)
	DescribeCluster(ctx context.Context, params *sdkeks.DescribeClusterInput, optFns ...func(*sdkeks.Options)) (*sdkeks.DescribeClusterOutput, error)
	ListNodegroups(ctx context.Context, params *sdkeks.ListNodegroupsInput, optFns ...func(*sdkeks.Options)) (*sdkeks.ListNodegroupsOutput, error)
	DescribeNodegroup(ctx context.Context, params *sdkeks.DescribeNodegroupInput, optFns ...func(*sdkeks.Options)) (*sdkeks.DescribeNodegroupOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("eks provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("eks provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newEKS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api eksAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var nextToken *string
	for {
		out, err := api.ListClusters(ctx, &sdkeks.ListClustersInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, name := range out.Clusters {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			d, err := api.DescribeCluster(ctx, &sdkeks.DescribeClusterInput{Name: awsSDK.String(name)})
			if err != nil {
				return nil, nil, err
			}
			if d.Cluster == nil {
				continue
			}
			n, stubs, es := normalizeCluster(partition, accountID, region, *d.Cluster, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)

			// Managed nodegroups.
			var ngToken *string
			for {
				ngOut, err := api.ListNodegroups(ctx, &sdkeks.ListNodegroupsInput{
					ClusterName: awsSDK.String(name),
					NextToken:   ngToken,
				})
				if err != nil {
					return nil, nil, err
				}
				for _, ngName := range ngOut.Nodegroups {
					ngName = strings.TrimSpace(ngName)
					if ngName == "" {
						continue
					}
					ngDesc, err := api.DescribeNodegroup(ctx, &sdkeks.DescribeNodegroupInput{
						ClusterName:   awsSDK.String(name),
						NodegroupName: awsSDK.String(ngName),
					})
					if err != nil {
						return nil, nil, err
					}
					if ngDesc.Nodegroup == nil {
						continue
					}
					ngNode, ngStubs, ngEdges := normalizeNodegroup(partition, accountID, region, n.Key, *ngDesc.Nodegroup, now)
					nodes = append(nodes, ngNode)
					nodes = append(nodes, ngStubs...)
					edges = append(edges, ngEdges...)
				}
				if ngOut.NextToken == nil || strings.TrimSpace(*ngOut.NextToken) == "" {
					break
				}
				ngToken = ngOut.NextToken
			}
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}
	return nodes, edges, nil
}

func normalizeCluster(partition, accountID, region string, c types.Cluster, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(c.Arn))
	name := strings.TrimSpace(awsToString(c.Name))
	key := graph.EncodeResourceKey(partition, accountID, region, "eks:cluster", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status":    strings.TrimSpace(string(c.Status)),
		"version":   strings.TrimSpace(awsToString(c.Version)),
		"platform":  strings.TrimSpace(awsToString(c.PlatformVersion)),
		"endpoint":  strings.TrimSpace(awsToString(c.Endpoint)),
		"publicApi": false,
	}
	if c.CreatedAt != nil {
		attrs["created_at"] = c.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	if c.ResourcesVpcConfig != nil {
		attrs["publicApi"] = c.ResourcesVpcConfig.EndpointPublicAccess
		attrs["privateApi"] = c.ResourcesVpcConfig.EndpointPrivateAccess
	}
	raw, _ := json.Marshal(c)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "eks", Type: "eks:cluster", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Tags: c.Tags, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "eks"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if roleArn := strings.TrimSpace(awsToString(c.RoleArn)); roleArn != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "eks"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "eks.role", "direct": true}, CollectedAt: now})
	}
	if c.ResourcesVpcConfig != nil {
		if vpc := strings.TrimSpace(awsToString(c.ResourcesVpcConfig.VpcId)); vpc != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "eks.vpc", "direct": true}, CollectedAt: now})
		}
		for _, subnet := range c.ResourcesVpcConfig.SubnetIds {
			subnet = strings.TrimSpace(subnet)
			if subnet == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnet, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "eks.subnet", "direct": true}, CollectedAt: now})
		}
		for _, sg := range c.ResourcesVpcConfig.SecurityGroupIds {
			sg = strings.TrimSpace(sg)
			if sg == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "eks.sg", "direct": true}, CollectedAt: now})
		}
	}
	for _, ec := range c.EncryptionConfig {
		if ec.Provider == nil {
			continue
		}
		if kms := strings.TrimSpace(awsToString(ec.Provider.KeyArn)); kms != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
			stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "eks"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "eks.kms", "direct": true}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func normalizeNodegroup(partition, accountID, region string, clusterKey graph.ResourceKey, ng types.Nodegroup, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(ng.NodegroupArn))
	name := strings.TrimSpace(awsToString(ng.NodegroupName))
	cluster := strings.TrimSpace(awsToString(ng.ClusterName))
	primary := firstNonEmpty(arn, cluster+"/"+name, name)
	key := graph.EncodeResourceKey(partition, accountID, region, "eks:nodegroup", primary)

	attrs := map[string]any{
		"status":       strings.TrimSpace(string(ng.Status)),
		"capacityType": strings.TrimSpace(string(ng.CapacityType)),
		"amiType":      strings.TrimSpace(string(ng.AmiType)),
		"clusterName":  cluster,
		"version":      strings.TrimSpace(awsToString(ng.Version)),
	}
	if ng.CreatedAt != nil {
		attrs["created_at"] = ng.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	if ng.ModifiedAt != nil {
		attrs["updated_at"] = ng.ModifiedAt.UTC().Format("2006-01-02 15:04")
	}
	if ng.ScalingConfig != nil {
		attrs["desiredSize"] = ng.ScalingConfig.DesiredSize
		attrs["minSize"] = ng.ScalingConfig.MinSize
		attrs["maxSize"] = ng.ScalingConfig.MaxSize
	}
	if ng.DiskSize != nil {
		attrs["diskSizeGiB"] = *ng.DiskSize
	}
	raw, _ := json.Marshal(ng)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, arn),
		Service:     "eks",
		Type:        "eks:nodegroup",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        ng.Tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "eks",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	edges = append(edges, graph.RelationshipEdge{
		From:        clusterKey,
		To:          key,
		Kind:        "contains",
		Meta:        map[string]any{"source": "eks.nodegroup", "direct": true},
		CollectedAt: now,
	})

	if roleArn := strings.TrimSpace(awsToString(ng.NodeRole)); roleArn != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "eks"))
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          toKey,
			Kind:        "uses",
			Meta:        map[string]any{"source": "eks.nodegroup.role", "direct": true},
			CollectedAt: now,
		})
	}
	for _, subnet := range ng.Subnets {
		subnet = strings.TrimSpace(subnet)
		if subnet == "" {
			continue
		}
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnet, now, "eks"))
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          toKey,
			Kind:        "member-of",
			Meta:        map[string]any{"source": "eks.nodegroup.subnet", "direct": true},
			CollectedAt: now,
		})
	}
	if ng.Resources != nil {
		if sg := strings.TrimSpace(awsToString(ng.Resources.RemoteAccessSecurityGroup)); sg != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "attached-to",
				Meta:        map[string]any{"source": "eks.nodegroup.remote-sg", "direct": true},
				CollectedAt: now,
			})
		}
		for _, asg := range ng.Resources.AutoScalingGroups {
			asgName := strings.TrimSpace(awsToString(asg.Name))
			if asgName == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "autoscaling:group", asgName)
			stubs = append(stubs, stubNode(toKey, "autoscaling", "autoscaling:group", asgName, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "contains",
				Meta:        map[string]any{"source": "eks.nodegroup.asg", "direct": true},
				CollectedAt: now,
			})
		}
	}
	if ng.LaunchTemplate != nil {
		ltID := strings.TrimSpace(awsToString(ng.LaunchTemplate.Id))
		ltName := strings.TrimSpace(awsToString(ng.LaunchTemplate.Name))
		ltPrimary := firstNonEmpty(ltID, ltName)
		if ltPrimary != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:launch-template", ltPrimary)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:launch-template", ltPrimary, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "uses",
				Meta: map[string]any{
					"source":  "eks.nodegroup.launch-template",
					"direct":  true,
					"version": strings.TrimSpace(awsToString(ng.LaunchTemplate.Version)),
				},
				CollectedAt: now,
			})
		}
	}
	if ng.RemoteAccess != nil {
		for _, sg := range ng.RemoteAccess.SourceSecurityGroups {
			sg = strings.TrimSpace(sg)
			if sg == "" {
				continue
			}
			toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
			stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "eks"))
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "attached-to",
				Meta:        map[string]any{"source": "eks.nodegroup.source-sg", "direct": true},
				CollectedAt: now,
			})
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
