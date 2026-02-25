package eks

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkeks "github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
)

type fakeEKS struct{}

func (fakeEKS) ListClusters(ctx context.Context, params *sdkeks.ListClustersInput, optFns ...func(*sdkeks.Options)) (*sdkeks.ListClustersOutput, error) {
	return &sdkeks.ListClustersOutput{Clusters: []string{"demo"}}, nil
}

func (fakeEKS) DescribeCluster(ctx context.Context, params *sdkeks.DescribeClusterInput, optFns ...func(*sdkeks.Options)) (*sdkeks.DescribeClusterOutput, error) {
	now := time.Now().UTC()
	return &sdkeks.DescribeClusterOutput{Cluster: &types.Cluster{
		Arn:       awsSDK.String("arn:aws:eks:us-east-1:123456789012:cluster/demo"),
		Name:      awsSDK.String("demo"),
		Status:    types.ClusterStatusActive,
		CreatedAt: &now,
		RoleArn:   awsSDK.String("arn:aws:iam::123456789012:role/eks-role"),
		ResourcesVpcConfig: &types.VpcConfigResponse{
			VpcId:            awsSDK.String("vpc-1"),
			SubnetIds:        []string{"subnet-1"},
			SecurityGroupIds: []string{"sg-1"},
		},
		EncryptionConfig: []types.EncryptionConfig{{Provider: &types.Provider{KeyArn: awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc")}}},
	}}, nil
}

func (fakeEKS) ListNodegroups(ctx context.Context, params *sdkeks.ListNodegroupsInput, optFns ...func(*sdkeks.Options)) (*sdkeks.ListNodegroupsOutput, error) {
	return &sdkeks.ListNodegroupsOutput{Nodegroups: []string{"workers"}}, nil
}

func (fakeEKS) DescribeNodegroup(ctx context.Context, params *sdkeks.DescribeNodegroupInput, optFns ...func(*sdkeks.Options)) (*sdkeks.DescribeNodegroupOutput, error) {
	now := time.Now().UTC()
	desired := int32(2)
	min := int32(1)
	max := int32(3)
	disk := int32(20)
	return &sdkeks.DescribeNodegroupOutput{
		Nodegroup: &types.Nodegroup{
			NodegroupArn:  awsSDK.String("arn:aws:eks:us-east-1:123456789012:nodegroup/demo/workers/ng-123"),
			NodegroupName: awsSDK.String("workers"),
			ClusterName:   awsSDK.String("demo"),
			Status:        types.NodegroupStatusActive,
			CreatedAt:     &now,
			ModifiedAt:    &now,
			NodeRole:      awsSDK.String("arn:aws:iam::123456789012:role/node-role"),
			Subnets:       []string{"subnet-1"},
			ScalingConfig: &types.NodegroupScalingConfig{
				DesiredSize: &desired,
				MinSize:     &min,
				MaxSize:     &max,
			},
			DiskSize: &disk,
			Resources: &types.NodegroupResources{
				RemoteAccessSecurityGroup: awsSDK.String("sg-remote"),
				AutoScalingGroups:         []types.AutoScalingGroup{{Name: awsSDK.String("asg-workers")}},
			},
			LaunchTemplate: &types.LaunchTemplateSpecification{
				Id:      awsSDK.String("lt-123"),
				Version: awsSDK.String("1"),
			},
		},
	}, nil
}

func TestProvider_List_ProducesCluster(t *testing.T) {
	p := New()
	p.newEKS = func(cfg awsSDK.Config) eksAPI { return fakeEKS{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	if len(res.Edges) < 3 {
		t.Fatalf("expected edges, got %d", len(res.Edges))
	}
	hasCluster := false
	hasNodegroup := false
	hasContains := false
	for _, n := range res.Nodes {
		if n.Type == "eks:cluster" {
			hasCluster = true
		}
		if n.Type == "eks:nodegroup" {
			hasNodegroup = true
		}
	}
	for _, e := range res.Edges {
		if e.Kind == "contains" {
			hasContains = true
			break
		}
	}
	if !hasCluster {
		t.Fatalf("expected eks:cluster node")
	}
	if !hasNodegroup {
		t.Fatalf("expected eks:nodegroup node")
	}
	if !hasContains {
		t.Fatalf("expected contains edge")
	}
}
