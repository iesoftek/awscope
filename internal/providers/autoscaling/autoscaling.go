package autoscaling

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
	sdkasg "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newASG func(cfg awsSDK.Config) asgAPI
}

func New() *Provider {
	return &Provider{
		newASG: func(cfg awsSDK.Config) asgAPI { return sdkasg.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "autoscaling" }
func (p *Provider) DisplayName() string { return "Auto Scaling" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type asgAPI interface {
	DescribeAutoScalingGroups(ctx context.Context, params *sdkasg.DescribeAutoScalingGroupsInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeAutoScalingGroupsOutput, error)
	DescribeAutoScalingInstances(ctx context.Context, params *sdkasg.DescribeAutoScalingInstancesInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeAutoScalingInstancesOutput, error)
	DescribeLaunchConfigurations(ctx context.Context, params *sdkasg.DescribeLaunchConfigurationsInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeLaunchConfigurationsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("autoscaling provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("autoscaling provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newASG(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api asgAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	lcByName := map[string]types.LaunchConfiguration{}
	var lcToken *string
	for {
		out, err := api.DescribeLaunchConfigurations(ctx, &sdkasg.DescribeLaunchConfigurationsInput{NextToken: lcToken})
		if err != nil {
			return nil, nil, err
		}
		for _, lc := range out.LaunchConfigurations {
			name := strings.TrimSpace(awsToString(lc.LaunchConfigurationName))
			if name != "" {
				lcByName[name] = lc
			}
			n, lcEdges := normalizeLaunchConfiguration(partition, accountID, region, lc, now)
			nodes = append(nodes, n)
			edges = append(edges, lcEdges...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		lcToken = out.NextToken
	}

	var nextToken *string
	for {
		out, err := api.DescribeAutoScalingGroups(ctx, &sdkasg.DescribeAutoScalingGroupsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, g := range out.AutoScalingGroups {
			gNode := normalizeGroup(partition, accountID, region, g, now)
			nodes = append(nodes, gNode)

			for _, subnetID := range splitCSV(awsToString(g.VPCZoneIdentifier)) {
				subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
				edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
			}
			for _, tg := range g.TargetGroupARNs {
				tgArn := strings.TrimSpace(tg)
				if tgArn == "" {
					continue
				}
				tgKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:target-group", tgArn)
				edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: tgKey, Kind: "targets", Meta: map[string]any{"direct": true}, CollectedAt: now})
			}
			for _, inst := range g.Instances {
				iid := strings.TrimSpace(awsToString(inst.InstanceId))
				if iid == "" {
					continue
				}
				instKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", iid)
				edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: instKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})

				asgInstNode := normalizeASGInstance(partition, accountID, region, awsToString(g.AutoScalingGroupName), inst, now)
				nodes = append(nodes, asgInstNode)
				edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: asgInstNode.Key, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
				edges = append(edges, graph.RelationshipEdge{From: asgInstNode.Key, To: instKey, Kind: "belongs-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
			}

			if lcName := strings.TrimSpace(awsToString(g.LaunchConfigurationName)); lcName != "" {
				lcKey := graph.EncodeResourceKey(partition, accountID, region, "autoscaling:launch-configuration", lcName)
				edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: lcKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
				if lc, ok := lcByName[lcName]; ok {
					for _, k := range launchConfigRefs(partition, accountID, region, lc) {
						edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: k, Kind: "uses", Meta: map[string]any{"derivedFrom": "launch-configuration"}, CollectedAt: now})
					}
				}
			}

			if g.LaunchTemplate != nil {
				ltPrimary := strings.TrimSpace(awsToString(g.LaunchTemplate.LaunchTemplateId))
				if ltPrimary == "" {
					ltPrimary = strings.TrimSpace(awsToString(g.LaunchTemplate.LaunchTemplateName))
				}
				if ltPrimary != "" {
					ltKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:launch-template", ltPrimary)
					edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: ltKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
				}
			}
			if g.MixedInstancesPolicy != nil && g.MixedInstancesPolicy.LaunchTemplate != nil && g.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification != nil {
				spec := g.MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification
				ltPrimary := strings.TrimSpace(awsToString(spec.LaunchTemplateId))
				if ltPrimary == "" {
					ltPrimary = strings.TrimSpace(awsToString(spec.LaunchTemplateName))
				}
				if ltPrimary != "" {
					ltKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:launch-template", ltPrimary)
					edges = append(edges, graph.RelationshipEdge{From: gNode.Key, To: ltKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "mixed-instances"}, CollectedAt: now})
				}
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, edges, nil
}

func normalizeGroup(partition, accountID, region string, g types.AutoScalingGroup, now time.Time) graph.ResourceNode {
	name := strings.TrimSpace(awsToString(g.AutoScalingGroupName))
	arn := strings.TrimSpace(awsToString(g.AutoScalingGroupARN))
	primary := name
	if primary == "" {
		primary = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "autoscaling:group", primary)
	tags := map[string]string{}
	for _, t := range g.Tags {
		k := strings.TrimSpace(awsToString(t.Key))
		if k == "" {
			continue
		}
		tags[k] = strings.TrimSpace(awsToString(t.Value))
	}
	attrs := map[string]any{
		"status":            strings.TrimSpace(awsToString(g.Status)),
		"minSize":           g.MinSize,
		"maxSize":           g.MaxSize,
		"desiredCapacity":   g.DesiredCapacity,
		"defaultCooldown":   g.DefaultCooldown,
		"availabilityZones": len(g.AvailabilityZones),
		"instances":         len(g.Instances),
		"vpcZoneIdentifier": strings.TrimSpace(awsToString(g.VPCZoneIdentifier)),
	}
	if g.CreatedTime != nil && !g.CreatedTime.IsZero() {
		attrs["created_at"] = g.CreatedTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(g)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, primary),
		Service:     "autoscaling",
		Type:        "autoscaling:group",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "autoscaling",
	}
}

func normalizeASGInstance(partition, accountID, region, groupName string, inst types.Instance, now time.Time) graph.ResourceNode {
	iid := strings.TrimSpace(awsToString(inst.InstanceId))
	primary := groupName + "/" + iid
	key := graph.EncodeResourceKey(partition, accountID, region, "autoscaling:instance", primary)
	attrs := map[string]any{
		"instanceId":           iid,
		"healthStatus":         strings.TrimSpace(awsToString(inst.HealthStatus)),
		"lifecycleState":       string(inst.LifecycleState),
		"availabilityZone":     strings.TrimSpace(awsToString(inst.AvailabilityZone)),
		"protectedFromScaleIn": boolFromPtr(inst.ProtectedFromScaleIn),
	}
	raw, _ := json.Marshal(inst)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: primary,
		Service:     "autoscaling",
		Type:        "autoscaling:instance",
		PrimaryID:   primary,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "autoscaling",
	}
}

func normalizeLaunchConfiguration(partition, accountID, region string, lc types.LaunchConfiguration, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := strings.TrimSpace(awsToString(lc.LaunchConfigurationName))
	key := graph.EncodeResourceKey(partition, accountID, region, "autoscaling:launch-configuration", name)
	attrs := map[string]any{
		"imageId":        strings.TrimSpace(awsToString(lc.ImageId)),
		"instanceType":   strings.TrimSpace(awsToString(lc.InstanceType)),
		"keyName":        strings.TrimSpace(awsToString(lc.KeyName)),
		"securityGroups": len(lc.SecurityGroups),
	}
	if lc.CreatedTime != nil && !lc.CreatedTime.IsZero() {
		attrs["created_at"] = lc.CreatedTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(lc)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "autoscaling",
		Type:        "autoscaling:launch-configuration",
		PrimaryID:   name,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "autoscaling",
	}
	var edges []graph.RelationshipEdge
	for _, ref := range launchConfigRefs(partition, accountID, region, lc) {
		edges = append(edges, graph.RelationshipEdge{From: key, To: ref, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	return node, edges
}

func launchConfigRefs(partition, accountID, region string, lc types.LaunchConfiguration) []graph.ResourceKey {
	var out []graph.ResourceKey
	if imageID := strings.TrimSpace(awsToString(lc.ImageId)); imageID != "" {
		out = append(out, graph.EncodeResourceKey(partition, accountID, region, "ec2:ami", imageID))
	}
	if keyName := strings.TrimSpace(awsToString(lc.KeyName)); keyName != "" {
		out = append(out, graph.EncodeResourceKey(partition, accountID, region, "ec2:key-pair", keyName))
	}
	for _, sg := range lc.SecurityGroups {
		sgID := strings.TrimSpace(sg)
		if sgID == "" {
			continue
		}
		out = append(out, graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID))
	}
	if prof := strings.TrimSpace(awsToString(lc.IamInstanceProfile)); prof != "" {
		out = append(out, graph.EncodeResourceKey(partition, accountID, "global", "iam:role", prof))
	}
	return out
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func boolFromPtr[T ~bool](v *T) bool {
	if v == nil {
		return false
	}
	return bool(*v)
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}
