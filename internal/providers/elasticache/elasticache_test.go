package elasticache

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkelasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
)

type fakeElastiCache struct{}

func (fakeElastiCache) DescribeReplicationGroups(ctx context.Context, params *sdkelasticache.DescribeReplicationGroupsInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeReplicationGroupsOutput, error) {
	now := time.Now().UTC()
	return &sdkelasticache.DescribeReplicationGroupsOutput{ReplicationGroups: []types.ReplicationGroup{{
		ReplicationGroupId:         awsSDK.String("rg1"),
		ARN:                        awsSDK.String("arn:aws:elasticache:us-east-1:123456789012:replicationgroup:rg1"),
		ReplicationGroupCreateTime: &now,
		Status:                     awsSDK.String("available"),
		KmsKeyId:                   awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc"),
		MemberClusters:             []string{"cc1"},
	}}}, nil
}

func (fakeElastiCache) DescribeCacheClusters(ctx context.Context, params *sdkelasticache.DescribeCacheClustersInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeCacheClustersOutput, error) {
	now := time.Now().UTC()
	return &sdkelasticache.DescribeCacheClustersOutput{CacheClusters: []types.CacheCluster{{
		CacheClusterId:         awsSDK.String("cc1"),
		ARN:                    awsSDK.String("arn:aws:elasticache:us-east-1:123456789012:cluster:cc1"),
		CacheClusterStatus:     awsSDK.String("available"),
		CacheClusterCreateTime: &now,
		ReplicationGroupId:     awsSDK.String("rg1"),
		CacheSubnetGroupName:   awsSDK.String("sgroup-1"),
		SecurityGroups:         []types.SecurityGroupMembership{{SecurityGroupId: awsSDK.String("sg-1")}},
	}}}, nil
}

func (fakeElastiCache) DescribeCacheSubnetGroups(ctx context.Context, params *sdkelasticache.DescribeCacheSubnetGroupsInput, optFns ...func(*sdkelasticache.Options)) (*sdkelasticache.DescribeCacheSubnetGroupsOutput, error) {
	return &sdkelasticache.DescribeCacheSubnetGroupsOutput{CacheSubnetGroups: []types.CacheSubnetGroup{{
		CacheSubnetGroupName: awsSDK.String("sgroup-1"),
		VpcId:                awsSDK.String("vpc-1"),
		Subnets:              []types.Subnet{{SubnetIdentifier: awsSDK.String("subnet-1")}},
	}}}, nil
}

func TestProvider_List_ProducesNodes(t *testing.T) {
	p := New()
	p.newElastiCache = func(cfg awsSDK.Config) elastiCacheAPI { return fakeElastiCache{} }
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
	hasSubnetGroup := false
	for _, n := range res.Nodes {
		if n.Type == "elasticache:subnet-group" {
			hasSubnetGroup = true
			break
		}
	}
	if !hasSubnetGroup {
		t.Fatalf("expected elasticache:subnet-group node")
	}
}
