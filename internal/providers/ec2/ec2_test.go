package ec2

import (
	"context"
	"testing"
	"time"

	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestNormalizeInstance_BuildsNodeAndEdges(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

	instID := "i-abc123"
	nameKey := "Name"
	nameVal := "web-1"
	vpcID := "vpc-1"
	subnetID := "subnet-1"
	sgID := "sg-1"
	sgName := "default"
	az := "us-east-1a"
	privateIP := "10.0.0.10"

	inst := types.Instance{
		InstanceId:        &instID,
		VpcId:             &vpcID,
		SubnetId:          &subnetID,
		PrivateIpAddress:  &privateIP,
		Placement:         &types.Placement{AvailabilityZone: &az},
		State:             &types.InstanceState{Name: types.InstanceStateNameRunning},
		SecurityGroups:    []types.GroupIdentifier{{GroupId: &sgID, GroupName: &sgName}},
		Tags:              []types.Tag{{Key: &nameKey, Value: &nameVal}},
		InstanceType:      types.InstanceTypeT3Micro,
		Monitoring:        &types.Monitoring{State: types.MonitoringStateEnabled},
		NetworkInterfaces: []types.InstanceNetworkInterface{},
	}

	node, stubs, edges := normalizeInstance("aws", "123456789012", "us-east-1", inst, now)

	wantKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", instID)
	if node.Key != wantKey {
		t.Fatalf("node key: got %q want %q", node.Key, wantKey)
	}
	if node.DisplayName != "web-1" {
		t.Fatalf("display name: got %q", node.DisplayName)
	}
	if node.Attributes["state"] != "running" {
		t.Fatalf("state attr: %#v", node.Attributes)
	}

	// We should have stub nodes for vpc, subnet, and sg.
	if len(stubs) != 0 {
		t.Fatalf("stubs: got %d want 0", len(stubs))
	}

	if len(edges) != 3 {
		t.Fatalf("edges: got %d want 3", len(edges))
	}
}

type fakeEC2API struct {
	vpcs    []types.Vpc
	subnets []types.Subnet
	sgs     []types.SecurityGroup
	vols    []types.Volume
}

func (f fakeEC2API) DescribeInstances(ctx context.Context, params *sdkec2.DescribeInstancesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInstancesOutput, error) {
	return &sdkec2.DescribeInstancesOutput{}, nil
}

func (f fakeEC2API) DescribeVpcs(ctx context.Context, params *sdkec2.DescribeVpcsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVpcsOutput, error) {
	return &sdkec2.DescribeVpcsOutput{Vpcs: f.vpcs}, nil
}

func (f fakeEC2API) DescribeSubnets(ctx context.Context, params *sdkec2.DescribeSubnetsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSubnetsOutput, error) {
	return &sdkec2.DescribeSubnetsOutput{Subnets: f.subnets}, nil
}

func (f fakeEC2API) DescribeSecurityGroups(ctx context.Context, params *sdkec2.DescribeSecurityGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSecurityGroupsOutput, error) {
	return &sdkec2.DescribeSecurityGroupsOutput{SecurityGroups: f.sgs}, nil
}

func (f fakeEC2API) DescribeVolumes(ctx context.Context, params *sdkec2.DescribeVolumesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVolumesOutput, error) {
	return &sdkec2.DescribeVolumesOutput{Volumes: f.vols}, nil
}

func TestProvider_ListRegion_EmitsSubnetAndSecurityGroupVpcEdges(t *testing.T) {
	ctx := context.Background()

	vpcID := "vpc-1"
	subnetID := "subnet-1"
	sgID := "sg-1"
	sgName := "default"

	api := fakeEC2API{
		vpcs: []types.Vpc{
			{VpcId: &vpcID},
		},
		subnets: []types.Subnet{
			{SubnetId: &subnetID, VpcId: &vpcID},
		},
		sgs: []types.SecurityGroup{
			{GroupId: &sgID, GroupName: &sgName, VpcId: &vpcID},
		},
	}

	p := New()
	nodes, edges, err := p.listRegion(ctx, api, "aws", "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("listRegion: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("expected nodes")
	}

	vpcKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", vpcID)
	subnetKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:subnet", subnetID)
	sgKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:security-group", sgID)

	var foundSubnet, foundSG bool
	for _, e := range edges {
		if e.Kind != "member-of" {
			continue
		}
		if e.From == subnetKey && e.To == vpcKey {
			foundSubnet = true
		}
		if e.From == sgKey && e.To == vpcKey {
			foundSG = true
		}
	}
	if !foundSubnet || !foundSG {
		t.Fatalf("missing edges: subnet=%v sg=%v edges=%#v", foundSubnet, foundSG, edges)
	}
}

func TestProvider_ListRegion_EmitsVolumeAttachmentEdges(t *testing.T) {
	ctx := context.Background()

	volID := "vol-1"
	instID := "i-1"
	dev := "/dev/xvda"
	enc := true
	kmsArn := "arn:aws:kms:us-east-1:123456789012:key/abc"

	api := fakeEC2API{
		vols: []types.Volume{
			{
				VolumeId:         &volID,
				Encrypted:        &enc,
				KmsKeyId:         &kmsArn,
				AvailabilityZone: awsSDK.String("us-east-1a"),
				VolumeType:       types.VolumeTypeGp3,
				Size:             awsSDK.Int32(8),
				Attachments: []types.VolumeAttachment{
					{InstanceId: &instID, Device: &dev, State: types.VolumeAttachmentStateAttached},
				},
			},
		},
	}

	p := New()
	nodes, edges, err := p.listRegion(ctx, api, "aws", "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("listRegion: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("expected nodes")
	}

	volKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:volume", volID)
	instKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", instID)
	kmsKey := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "kms:key", kmsArn)

	var foundAttach, foundKMS bool
	for _, e := range edges {
		if e.From == volKey && e.To == instKey && e.Kind == "attached-to" {
			foundAttach = true
		}
		if e.From == volKey && e.To == kmsKey && e.Kind == "uses" {
			foundKMS = true
		}
	}
	if !foundAttach || !foundKMS {
		t.Fatalf("missing edges: attach=%v kms=%v edges=%#v", foundAttach, foundKMS, edges)
	}
}
