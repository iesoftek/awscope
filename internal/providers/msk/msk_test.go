package msk

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkkafka "github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

type fakeMSK struct{}

func (fakeMSK) ListClustersV2(ctx context.Context, params *sdkkafka.ListClustersV2Input, optFns ...func(*sdkkafka.Options)) (*sdkkafka.ListClustersV2Output, error) {
	now := time.Now().UTC()
	return &sdkkafka.ListClustersV2Output{ClusterInfoList: []types.Cluster{{
		ClusterArn:   awsSDK.String("arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc"),
		ClusterName:  awsSDK.String("demo"),
		State:        types.ClusterStateActive,
		CreationTime: &now,
		Provisioned: &types.Provisioned{
			BrokerNodeGroupInfo: &types.BrokerNodeGroupInfo{
				ClientSubnets:  []string{"subnet-1"},
				SecurityGroups: []string{"sg-1"},
			},
			EncryptionInfo: &types.EncryptionInfo{EncryptionAtRest: &types.EncryptionAtRest{DataVolumeKMSKeyId: awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc")}},
		},
	}}}, nil
}

func TestProvider_List_ProducesCluster(t *testing.T) {
	p := New()
	p.newMSK = func(cfg awsSDK.Config) mskAPI { return fakeMSK{} }
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
}
