package efs

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkefs "github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/efs/types"
)

type fakeEFS struct{}

func (fakeEFS) DescribeFileSystems(ctx context.Context, params *sdkefs.DescribeFileSystemsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeFileSystemsOutput, error) {
	now := time.Now().UTC()
	return &sdkefs.DescribeFileSystemsOutput{FileSystems: []types.FileSystemDescription{{
		FileSystemId:         awsSDK.String("fs-1"),
		FileSystemArn:        awsSDK.String("arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-1"),
		CreationTime:         &now,
		LifeCycleState:       types.LifeCycleStateAvailable,
		NumberOfMountTargets: 1,
		PerformanceMode:      types.PerformanceModeGeneralPurpose,
		SizeInBytes:          &types.FileSystemSize{Value: 1024},
		KmsKeyId:             awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc"),
	}}}, nil
}

func (fakeEFS) DescribeMountTargets(ctx context.Context, params *sdkefs.DescribeMountTargetsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeMountTargetsOutput, error) {
	return &sdkefs.DescribeMountTargetsOutput{MountTargets: []types.MountTargetDescription{{
		MountTargetId:  awsSDK.String("mt-1"),
		LifeCycleState: types.LifeCycleStateAvailable,
		SubnetId:       awsSDK.String("subnet-1"),
		VpcId:          awsSDK.String("vpc-1"),
	}}}, nil
}

func (fakeEFS) DescribeMountTargetSecurityGroups(ctx context.Context, params *sdkefs.DescribeMountTargetSecurityGroupsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeMountTargetSecurityGroupsOutput, error) {
	return &sdkefs.DescribeMountTargetSecurityGroupsOutput{SecurityGroups: []string{"sg-1"}}, nil
}

func (fakeEFS) DescribeAccessPoints(ctx context.Context, params *sdkefs.DescribeAccessPointsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeAccessPointsOutput, error) {
	return &sdkefs.DescribeAccessPointsOutput{
		AccessPoints: []types.AccessPointDescription{{
			AccessPointId:  awsSDK.String("fsap-1"),
			AccessPointArn: awsSDK.String("arn:aws:elasticfilesystem:us-east-1:123456789012:access-point/fsap-1"),
			FileSystemId:   awsSDK.String("fs-1"),
			LifeCycleState: types.LifeCycleStateAvailable,
			Name:           awsSDK.String("demo-ap"),
			RootDirectory:  &types.RootDirectory{Path: awsSDK.String("/data")},
			PosixUser:      &types.PosixUser{Uid: awsSDK.Int64(1000), Gid: awsSDK.Int64(1000)},
		}},
	}, nil
}

func TestProvider_List_ProducesFileSystemAndMountTarget(t *testing.T) {
	p := New()
	p.newEFS = func(cfg awsSDK.Config) efsAPI { return fakeEFS{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) < 2 {
		t.Fatalf("expected nodes, got %d", len(res.Nodes))
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
	hasAccessPoint := false
	for _, n := range res.Nodes {
		if n.Type == "efs:access-point" {
			hasAccessPoint = true
			break
		}
	}
	if !hasAccessPoint {
		t.Fatalf("expected efs:access-point node")
	}
}
