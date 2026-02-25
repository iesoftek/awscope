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

func (f fakeEC2API) DescribeImages(ctx context.Context, params *sdkec2.DescribeImagesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeImagesOutput, error) {
	return &sdkec2.DescribeImagesOutput{}, nil
}

func (f fakeEC2API) DescribeSnapshots(ctx context.Context, params *sdkec2.DescribeSnapshotsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSnapshotsOutput, error) {
	return &sdkec2.DescribeSnapshotsOutput{}, nil
}

func (f fakeEC2API) DescribeInternetGateways(ctx context.Context, params *sdkec2.DescribeInternetGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInternetGatewaysOutput, error) {
	return &sdkec2.DescribeInternetGatewaysOutput{}, nil
}

func (f fakeEC2API) DescribePlacementGroups(ctx context.Context, params *sdkec2.DescribePlacementGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribePlacementGroupsOutput, error) {
	return &sdkec2.DescribePlacementGroupsOutput{}, nil
}

func (f fakeEC2API) DescribeLaunchTemplates(ctx context.Context, params *sdkec2.DescribeLaunchTemplatesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplatesOutput, error) {
	return &sdkec2.DescribeLaunchTemplatesOutput{}, nil
}

func (f fakeEC2API) DescribeLaunchTemplateVersions(ctx context.Context, params *sdkec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplateVersionsOutput, error) {
	return &sdkec2.DescribeLaunchTemplateVersionsOutput{}, nil
}

func (f fakeEC2API) DescribeNatGateways(ctx context.Context, params *sdkec2.DescribeNatGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNatGatewaysOutput, error) {
	return &sdkec2.DescribeNatGatewaysOutput{}, nil
}

func (f fakeEC2API) DescribeAddresses(ctx context.Context, params *sdkec2.DescribeAddressesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeAddressesOutput, error) {
	return &sdkec2.DescribeAddressesOutput{}, nil
}

func (f fakeEC2API) DescribeRouteTables(ctx context.Context, params *sdkec2.DescribeRouteTablesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeRouteTablesOutput, error) {
	return &sdkec2.DescribeRouteTablesOutput{}, nil
}

func (f fakeEC2API) DescribeNetworkAcls(ctx context.Context, params *sdkec2.DescribeNetworkAclsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkAclsOutput, error) {
	return &sdkec2.DescribeNetworkAclsOutput{}, nil
}

func (f fakeEC2API) DescribeNetworkInterfaces(ctx context.Context, params *sdkec2.DescribeNetworkInterfacesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkInterfacesOutput, error) {
	return &sdkec2.DescribeNetworkInterfacesOutput{}, nil
}

func (f fakeEC2API) DescribeKeyPairs(ctx context.Context, params *sdkec2.DescribeKeyPairsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeKeyPairsOutput, error) {
	return &sdkec2.DescribeKeyPairsOutput{}, nil
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

func TestNormalizeSnapshot_UsesVolume(t *testing.T) {
	now := time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC)
	snapID := "snap-1"
	volID := "vol-1"
	s := types.Snapshot{
		SnapshotId: &snapID,
		VolumeId:   &volID,
		State:      types.SnapshotStateCompleted,
	}
	node, edges := normalizeSnapshot("aws", "123456789012", "us-east-1", s, now)
	if node.Type != "ec2:snapshot" {
		t.Fatalf("node type: %s", node.Type)
	}
	if len(edges) != 1 || edges[0].Kind != "uses" {
		t.Fatalf("snapshot edges: %#v", edges)
	}
}

func TestNormalizeRouteTable_TargetsNatGateway(t *testing.T) {
	now := time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC)
	rtID := "rtb-1"
	vpcID := "vpc-1"
	natID := "nat-1"
	rt := types.RouteTable{
		RouteTableId: &rtID,
		VpcId:        &vpcID,
		Routes: []types.Route{
			{NatGatewayId: &natID},
		},
	}
	_, edges := normalizeRouteTable("aws", "123456789012", "us-east-1", rt, now)
	foundMember := false
	foundTarget := false
	for _, e := range edges {
		if e.Kind == "member-of" {
			foundMember = true
		}
		if e.Kind == "targets" {
			foundTarget = true
		}
	}
	if !foundMember || !foundTarget {
		t.Fatalf("missing expected route table edges: %#v", edges)
	}
}
