package ec2

import (
	"context"
	"fmt"

	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newEC2 func(cfg awsSDK.Config) ec2API
}

func New() *Provider {
	return &Provider{
		newEC2: func(cfg awsSDK.Config) ec2API { return sdkec2.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "ec2" }
func (p *Provider) DisplayName() string { return "EC2" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type ec2API interface {
	DescribeInstances(ctx context.Context, params *sdkec2.DescribeInstancesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInstancesOutput, error)
	DescribeVpcs(ctx context.Context, params *sdkec2.DescribeVpcsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVpcsOutput, error)
	DescribeSubnets(ctx context.Context, params *sdkec2.DescribeSubnetsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *sdkec2.DescribeSecurityGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSecurityGroupsOutput, error)
	DescribeVolumes(ctx context.Context, params *sdkec2.DescribeVolumesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVolumesOutput, error)
	DescribeImages(ctx context.Context, params *sdkec2.DescribeImagesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeImagesOutput, error)
	DescribeSnapshots(ctx context.Context, params *sdkec2.DescribeSnapshotsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSnapshotsOutput, error)
	DescribeInternetGateways(ctx context.Context, params *sdkec2.DescribeInternetGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInternetGatewaysOutput, error)
	DescribePlacementGroups(ctx context.Context, params *sdkec2.DescribePlacementGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribePlacementGroupsOutput, error)
	DescribeLaunchTemplates(ctx context.Context, params *sdkec2.DescribeLaunchTemplatesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplatesOutput, error)
	DescribeLaunchTemplateVersions(ctx context.Context, params *sdkec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplateVersionsOutput, error)
	DescribeNatGateways(ctx context.Context, params *sdkec2.DescribeNatGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNatGatewaysOutput, error)
	DescribeAddresses(ctx context.Context, params *sdkec2.DescribeAddressesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeAddressesOutput, error)
	DescribeRouteTables(ctx context.Context, params *sdkec2.DescribeRouteTablesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeRouteTablesOutput, error)
	DescribeNetworkAcls(ctx context.Context, params *sdkec2.DescribeNetworkAclsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkAclsOutput, error)
	DescribeNetworkInterfaces(ctx context.Context, params *sdkec2.DescribeNetworkInterfacesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkInterfacesOutput, error)
	DescribeKeyPairs(ctx context.Context, params *sdkec2.DescribeKeyPairsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeKeyPairsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("ec2 provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("ec2 provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region

		nodes, edges, err := p.listRegion(ctx, p.newEC2(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}
