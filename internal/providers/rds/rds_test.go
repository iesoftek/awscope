package rds

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkrds "github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
)

type fakeRDSAPI struct{}

func (f fakeRDSAPI) DescribeDBInstances(ctx context.Context, params *sdkrds.DescribeDBInstancesInput, optFns ...func(*sdkrds.Options)) (*sdkrds.DescribeDBInstancesOutput, error) {
	id := "db1"
	arn := "arn:aws:rds:us-east-1:123456789012:db:db1"
	status := "available"
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	return &sdkrds.DescribeDBInstancesOutput{
		DBInstances: []types.DBInstance{
			{DBInstanceIdentifier: &id, DBInstanceArn: &arn, DBInstanceStatus: &status, InstanceCreateTime: &now},
		},
	}, nil
}

func (f fakeRDSAPI) DescribeDBClusters(ctx context.Context, params *sdkrds.DescribeDBClustersInput, optFns ...func(*sdkrds.Options)) (*sdkrds.DescribeDBClustersOutput, error) {
	return &sdkrds.DescribeDBClustersOutput{}, nil
}

func TestProvider_List_EmitsInstance(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newRDS = func(cfg awsSDK.Config) rdsAPI { return fakeRDSAPI{} }

	res, err := p.List(ctx, awsSDK.Config{Region: "us-east-1"}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"us-east-1"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
}
