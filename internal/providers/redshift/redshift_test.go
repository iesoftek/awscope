package redshift

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/redshift/types"
)

type fakeRedshift struct{}

func (fakeRedshift) DescribeClusters(ctx context.Context, params *sdkredshift.DescribeClustersInput, optFns ...func(*sdkredshift.Options)) (*sdkredshift.DescribeClustersOutput, error) {
	now := time.Now().UTC()
	return &sdkredshift.DescribeClustersOutput{Clusters: []types.Cluster{{
		ClusterIdentifier:      awsSDK.String("demo"),
		ClusterCreateTime:      &now,
		ClusterStatus:          awsSDK.String("available"),
		VpcId:                  awsSDK.String("vpc-1"),
		ClusterSubnetGroupName: awsSDK.String("subnet-group-1"),
		VpcSecurityGroups:      []types.VpcSecurityGroupMembership{{VpcSecurityGroupId: awsSDK.String("sg-1")}},
		KmsKeyId:               awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc"),
		IamRoles:               []types.ClusterIamRole{{IamRoleArn: awsSDK.String("arn:aws:iam::123456789012:role/redshift-role")}},
	}}}, nil
}

func (fakeRedshift) DescribeClusterSubnetGroups(ctx context.Context, params *sdkredshift.DescribeClusterSubnetGroupsInput, optFns ...func(*sdkredshift.Options)) (*sdkredshift.DescribeClusterSubnetGroupsOutput, error) {
	return &sdkredshift.DescribeClusterSubnetGroupsOutput{ClusterSubnetGroups: []types.ClusterSubnetGroup{{
		ClusterSubnetGroupName: awsSDK.String("subnet-group-1"),
		VpcId:                  awsSDK.String("vpc-1"),
		Subnets:                []types.Subnet{{SubnetIdentifier: awsSDK.String("subnet-1")}},
	}}}, nil
}

func TestProvider_List_ProducesCluster(t *testing.T) {
	p := New()
	p.newRedshift = func(cfg awsSDK.Config) redshiftAPI { return fakeRedshift{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
	hasSubnetGroup := false
	for _, n := range res.Nodes {
		if n.Type == "redshift:subnet-group" {
			hasSubnetGroup = true
			break
		}
	}
	if !hasSubnetGroup {
		t.Fatalf("expected redshift:subnet-group node")
	}
}
