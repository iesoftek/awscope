package elbv2

import (
	"context"
	"testing"
	"time"

	"awscope/internal/graph"

	sdklb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

func TestNormalizeTargetGroupAndLoadBalancer(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

	tgArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc"
	tgName := "tg"
	vpcID := "vpc-1"
	port := int32(80)
	tg := types.TargetGroup{
		TargetGroupArn:  &tgArn,
		TargetGroupName: &tgName,
		VpcId:           &vpcID,
		Protocol:        types.ProtocolEnumHttp,
		Port:            &port,
		TargetType:      types.TargetTypeEnumInstance,
	}
	tgn := normalizeTargetGroup("aws", "123456789012", "us-east-1", tg, now)
	if tgn.Type != "elbv2:target-group" || tgn.Service != "elbv2" {
		t.Fatalf("tg type/service: %#v", tgn)
	}

	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/lb/xyz"
	lbName := "lb"
	lb := types.LoadBalancer{
		LoadBalancerArn:  &lbArn,
		LoadBalancerName: &lbName,
		Type:             types.LoadBalancerTypeEnumApplication,
		Scheme:           types.LoadBalancerSchemeEnumInternetFacing,
	}
	lbn := normalizeLoadBalancer("aws", "123456789012", "us-east-1", lb, now)
	if lbn.Type != "elbv2:load-balancer" || lbn.DisplayName != "lb" {
		t.Fatalf("lb: %#v", lbn)
	}
}

type fakeLBAPI struct {
	loadBalancers []types.LoadBalancer
	targetGroups  []types.TargetGroup
}

func (f *fakeLBAPI) DescribeTargetGroups(ctx context.Context, params *sdklb.DescribeTargetGroupsInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeTargetGroupsOutput, error) {
	return &sdklb.DescribeTargetGroupsOutput{TargetGroups: f.targetGroups}, nil
}

func (f *fakeLBAPI) DescribeLoadBalancers(ctx context.Context, params *sdklb.DescribeLoadBalancersInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeLoadBalancersOutput, error) {
	return &sdklb.DescribeLoadBalancersOutput{LoadBalancers: f.loadBalancers}, nil
}

func (f *fakeLBAPI) DescribeListeners(ctx context.Context, params *sdklb.DescribeListenersInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeListenersOutput, error) {
	// No listeners needed for edge tests.
	return &sdklb.DescribeListenersOutput{Listeners: nil}, nil
}

func (f *fakeLBAPI) DescribeRules(ctx context.Context, params *sdklb.DescribeRulesInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeRulesOutput, error) {
	// No rules needed for edge tests.
	return &sdklb.DescribeRulesOutput{Rules: nil}, nil
}

func TestProvider_ListRegion_EmitsVpcSubnetAndSecurityGroupEdges(t *testing.T) {
	p := &Provider{}
	ctx := context.Background()

	partition := "aws"
	accountID := "123456789012"
	region := "us-east-1"

	vpcID := "vpc-1"
	sgID := "sg-1"
	subnet1 := "subnet-1"
	subnet2 := "subnet-2"

	lbArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/lb/xyz"
	lbName := "lb"
	lb := types.LoadBalancer{
		LoadBalancerArn:  &lbArn,
		LoadBalancerName: &lbName,
		VpcId:            &vpcID,
		SecurityGroups:   []string{sgID},
		AvailabilityZones: []types.AvailabilityZone{
			{SubnetId: &subnet1},
			{SubnetId: &subnet2},
		},
		Type: types.LoadBalancerTypeEnumApplication,
	}

	tgArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc"
	tgName := "tg"
	port := int32(80)
	tg := types.TargetGroup{
		TargetGroupArn:  &tgArn,
		TargetGroupName: &tgName,
		VpcId:           &vpcID,
		Protocol:        types.ProtocolEnumHttp,
		Port:            &port,
		TargetType:      types.TargetTypeEnumInstance,
		LoadBalancerArns: []string{
			lbArn,
		},
	}

	api := &fakeLBAPI{
		loadBalancers: []types.LoadBalancer{lb},
		targetGroups:  []types.TargetGroup{tg},
	}

	_, edges, err := p.listRegion(ctx, api, partition, accountID, region)
	if err != nil {
		t.Fatalf("listRegion: %v", err)
	}

	lbKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:load-balancer", lbArn)
	tgKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:target-group", tgArn)
	vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
	subnetKey1 := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet1)
	subnetKey2 := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet2)
	sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)

	have := map[string]bool{}
	for _, e := range edges {
		have[string(e.From)+"|"+e.Kind+"|"+string(e.To)] = true
	}

	want := []struct {
		from graph.ResourceKey
		kind string
		to   graph.ResourceKey
	}{
		{from: lbKey, kind: "member-of", to: vpcKey},
		{from: lbKey, kind: "member-of", to: subnetKey1},
		{from: lbKey, kind: "member-of", to: subnetKey2},
		{from: lbKey, kind: "attached-to", to: sgKey},
		{from: tgKey, kind: "member-of", to: vpcKey},
	}
	for _, w := range want {
		k := string(w.from) + "|" + w.kind + "|" + string(w.to)
		if !have[k] {
			t.Errorf("missing edge: %s", k)
		}
	}
}
