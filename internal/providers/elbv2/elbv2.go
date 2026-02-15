package elbv2

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
	sdklb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newLB func(cfg awsSDK.Config) lbAPI
}

func New() *Provider {
	return &Provider{
		newLB: func(cfg awsSDK.Config) lbAPI { return sdklb.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "elbv2" }
func (p *Provider) DisplayName() string { return "ELBv2" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type lbAPI interface {
	DescribeTargetGroups(ctx context.Context, params *sdklb.DescribeTargetGroupsInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeTargetGroupsOutput, error)
	DescribeLoadBalancers(ctx context.Context, params *sdklb.DescribeLoadBalancersInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeLoadBalancersOutput, error)
	DescribeListeners(ctx context.Context, params *sdklb.DescribeListenersInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeListenersOutput, error)
	DescribeRules(ctx context.Context, params *sdklb.DescribeRulesInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeRulesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("elbv2 provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("elbv2 provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newLB(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api lbAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var (
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
	)

	// Load balancers.
	lbs, err := listAllLoadBalancers(ctx, api)
	if err != nil {
		return nil, nil, err
	}
	for _, lb := range lbs {
		lbNode := normalizeLoadBalancer(partition, accountID, region, lb, now)
		nodes = append(nodes, lbNode)

		// Load balancer -> VPC / subnets / security groups (navigational edges).
		if vpcID := awsToString(lb.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			nodes = append(nodes, stubNode(vpcKey, "ec2", "ec2:vpc", vpcID, now, "elbv2"))
			edges = append(edges, graph.RelationshipEdge{
				From:        lbNode.Key,
				To:          vpcKey,
				Kind:        "member-of",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
		for _, sgID := range lb.SecurityGroups {
			if sgID == "" {
				continue
			}
			sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)
			nodes = append(nodes, stubNode(sgKey, "ec2", "ec2:security-group", sgID, now, "elbv2"))
			edges = append(edges, graph.RelationshipEdge{
				From:        lbNode.Key,
				To:          sgKey,
				Kind:        "attached-to",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
		for _, az := range lb.AvailabilityZones {
			subnetID := awsToString(az.SubnetId)
			if subnetID == "" {
				continue
			}
			subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
			nodes = append(nodes, stubNode(subnetKey, "ec2", "ec2:subnet", subnetID, now, "elbv2"))
			edges = append(edges, graph.RelationshipEdge{
				From:        lbNode.Key,
				To:          subnetKey,
				Kind:        "member-of",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}

		// Optional v1: include listeners so the graph has a navigable depth.
		listeners, err := listAllListeners(ctx, api, awsToString(lb.LoadBalancerArn))
		if err != nil {
			return nil, nil, err
		}
		for _, l := range listeners {
			lNode := normalizeListener(partition, accountID, region, l, now)
			nodes = append(nodes, lNode)
			edges = append(edges, graph.RelationshipEdge{
				From:        lbNode.Key,
				To:          lNode.Key,
				Kind:        "contains",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})

			// Rules: listener -> rules, rule -> target group(s) via forward actions.
			rules, err := listAllRules(ctx, api, awsToString(l.ListenerArn))
			if err != nil {
				return nil, nil, err
			}
			for _, r := range rules {
				rNode := normalizeRule(partition, accountID, region, r, now)
				nodes = append(nodes, rNode)
				edges = append(edges, graph.RelationshipEdge{
					From:        lNode.Key,
					To:          rNode.Key,
					Kind:        "contains",
					Meta:        map[string]any{"direct": true},
					CollectedAt: now,
				})

				tgArns := ruleTargetGroups(r)
				for _, tgArn := range tgArns {
					tgKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:target-group", tgArn)
					edges = append(edges, graph.RelationshipEdge{
						From:        rNode.Key,
						To:          tgKey,
						Kind:        "forwards-to",
						Meta:        map[string]any{"direct": true},
						CollectedAt: now,
					})
					// Convenience edge for navigation without opening the rule.
					edges = append(edges, graph.RelationshipEdge{
						From:        lNode.Key,
						To:          tgKey,
						Kind:        "forwards-to",
						Meta:        map[string]any{"direct": true, "via": "rule"},
						CollectedAt: now,
					})
				}
			}
		}
	}

	// Target groups + TG->LB edges from LoadBalancerArns.
	tgs, err := listAllTargetGroups(ctx, api)
	if err != nil {
		return nil, nil, err
	}
	for _, tg := range tgs {
		tgNode := normalizeTargetGroup(partition, accountID, region, tg, now)
		nodes = append(nodes, tgNode)
		if vpcID := awsToString(tg.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			nodes = append(nodes, stubNode(vpcKey, "ec2", "ec2:vpc", vpcID, now, "elbv2"))
			edges = append(edges, graph.RelationshipEdge{
				From:        tgNode.Key,
				To:          vpcKey,
				Kind:        "member-of",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
		for _, lbArn := range tg.LoadBalancerArns {
			if lbArn == "" {
				continue
			}
			lbKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:load-balancer", lbArn)
			edges = append(edges, graph.RelationshipEdge{
				From:        tgNode.Key,
				To:          lbKey,
				Kind:        "attached-to",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
	}

	return nodes, edges, nil
}

func listAllLoadBalancers(ctx context.Context, api lbAPI) ([]types.LoadBalancer, error) {
	var out []types.LoadBalancer
	var marker *string
	for {
		resp, err := api.DescribeLoadBalancers(ctx, &sdklb.DescribeLoadBalancersInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.LoadBalancers...)
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func listAllTargetGroups(ctx context.Context, api lbAPI) ([]types.TargetGroup, error) {
	var out []types.TargetGroup
	var marker *string
	for {
		resp, err := api.DescribeTargetGroups(ctx, &sdklb.DescribeTargetGroupsInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.TargetGroups...)
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func listAllListeners(ctx context.Context, api lbAPI, loadBalancerArn string) ([]types.Listener, error) {
	if loadBalancerArn == "" {
		return nil, nil
	}
	var out []types.Listener
	var marker *string
	for {
		resp, err := api.DescribeListeners(ctx, &sdklb.DescribeListenersInput{
			LoadBalancerArn: &loadBalancerArn,
			Marker:          marker,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Listeners...)
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func listAllRules(ctx context.Context, api lbAPI, listenerArn string) ([]types.Rule, error) {
	if listenerArn == "" {
		return nil, nil
	}
	var out []types.Rule
	var marker *string
	for {
		resp, err := api.DescribeRules(ctx, &sdklb.DescribeRulesInput{
			ListenerArn: &listenerArn,
			Marker:      marker,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Rules...)
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func normalizeLoadBalancer(partition, accountID, region string, lb types.LoadBalancer, now time.Time) graph.ResourceNode {
	arn := awsToString(lb.LoadBalancerArn)
	name := awsToString(lb.LoadBalancerName)
	if name == "" {
		name = shortArn(arn)
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "elbv2:load-balancer", arn)
	raw, _ := json.Marshal(lb)

	state := ""
	if lb.State != nil {
		state = string(lb.State.Code)
	}

	attrs := map[string]any{
		"type":          string(lb.Type),
		"scheme":        string(lb.Scheme),
		"state":         state,
		"dnsName":       awsToString(lb.DNSName),
		"ipAddressType": string(lb.IpAddressType),
	}
	if lb.CreatedTime != nil {
		attrs["created_at"] = lb.CreatedTime.UTC().Format("2006-01-02 15:04")
	}

	return graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "elbv2",
		Type:        "elbv2:load-balancer",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "elbv2",
	}
}

func normalizeListener(partition, accountID, region string, l types.Listener, now time.Time) graph.ResourceNode {
	arn := awsToString(l.ListenerArn)
	display := fmt.Sprintf("%s:%d", string(l.Protocol), l.Port)
	if arn == "" {
		display = "listener"
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "elbv2:listener", arn)
	raw, _ := json.Marshal(l)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "elbv2",
		Type:        "elbv2:listener",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes: map[string]any{
			"protocol": string(l.Protocol),
			"port":     l.Port,
		},
		Raw:         raw,
		CollectedAt: now,
		Source:      "elbv2",
	}
}

func normalizeRule(partition, accountID, region string, r types.Rule, now time.Time) graph.ResourceNode {
	arn := awsToString(r.RuleArn)
	priority := awsToString(r.Priority)
	display := "rule"
	if priority != "" {
		display = "prio " + priority
	}
	if r.IsDefault != nil && *r.IsDefault {
		display = "default"
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "elbv2:rule", arn)
	raw, _ := json.Marshal(r)
	isDefault := false
	if r.IsDefault != nil {
		isDefault = *r.IsDefault
	}

	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "elbv2",
		Type:        "elbv2:rule",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes: map[string]any{
			"priority":     priority,
			"isDefault":    isDefault,
			"conditions":   len(r.Conditions),
			"actions":      len(r.Actions),
			"targetGroups": len(ruleTargetGroups(r)),
		},
		Raw:         raw,
		CollectedAt: now,
		Source:      "elbv2",
	}
}

func ruleTargetGroups(r types.Rule) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(arn string) {
		if arn == "" {
			return
		}
		if _, ok := seen[arn]; ok {
			return
		}
		seen[arn] = struct{}{}
		out = append(out, arn)
	}

	for _, a := range r.Actions {
		// Direct target group on action.
		add(awsToString(a.TargetGroupArn))

		// ForwardConfig contains target groups too.
		if a.ForwardConfig != nil {
			for _, tg := range a.ForwardConfig.TargetGroups {
				add(awsToString(tg.TargetGroupArn))
			}
		}
	}
	return out
}

func normalizeTargetGroup(partition, accountID, region string, tg types.TargetGroup, now time.Time) graph.ResourceNode {
	arn := awsToString(tg.TargetGroupArn)
	name := awsToString(tg.TargetGroupName)
	if name == "" {
		name = shortArn(arn)
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "elbv2:target-group", arn)
	raw, _ := json.Marshal(tg)

	attrs := map[string]any{
		"protocol":        string(tg.Protocol),
		"port":            tg.Port,
		"targetType":      string(tg.TargetType),
		"healthCheckPath": awsToString(tg.HealthCheckPath),
		"vpc":             awsToString(tg.VpcId),
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "elbv2",
		Type:        "elbv2:target-group",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "elbv2",
	}
}

func shortArn(arn string) string {
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		Arn:         "",
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}
