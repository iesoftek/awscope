package config

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkconfig "github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/aws/aws-sdk-go-v2/service/configservice/types"
)

type fakeConfig struct{}

func (fakeConfig) DescribeConfigurationRecorders(ctx context.Context, params *sdkconfig.DescribeConfigurationRecordersInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeConfigurationRecordersOutput, error) {
	return &sdkconfig.DescribeConfigurationRecordersOutput{ConfigurationRecorders: []types.ConfigurationRecorder{{
		Name:    awsSDK.String("default"),
		Arn:     awsSDK.String("arn:aws:config:us-east-1:123456789012:config-recorder/default"),
		RoleARN: awsSDK.String("arn:aws:iam::123456789012:role/service-role/config-role"),
	}}}, nil
}

func (fakeConfig) DescribeConfigurationRecorderStatus(ctx context.Context, params *sdkconfig.DescribeConfigurationRecorderStatusInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeConfigurationRecorderStatusOutput, error) {
	now := time.Now().UTC()
	return &sdkconfig.DescribeConfigurationRecorderStatusOutput{ConfigurationRecordersStatus: []types.ConfigurationRecorderStatus{{
		Name:                 awsSDK.String("default"),
		Recording:            true,
		LastStatus:           types.RecorderStatusSuccess,
		LastStatusChangeTime: &now,
	}}}, nil
}

func (fakeConfig) DescribeDeliveryChannels(ctx context.Context, params *sdkconfig.DescribeDeliveryChannelsInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeDeliveryChannelsOutput, error) {
	return &sdkconfig.DescribeDeliveryChannelsOutput{DeliveryChannels: []types.DeliveryChannel{{
		Name:         awsSDK.String("default"),
		S3BucketName: awsSDK.String("cfg-bucket"),
	}}}, nil
}

func TestProvider_List_ProducesRecorderAndChannel(t *testing.T) {
	p := New()
	p.newConfig = func(cfg awsSDK.Config) configAPI { return fakeConfig{} }

	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) < 2 {
		t.Fatalf("expected >=2 nodes, got %d", len(res.Nodes))
	}
	var hasRecorder, hasChannel bool
	for _, n := range res.Nodes {
		if n.Type == "config:recorder" {
			hasRecorder = true
		}
		if n.Type == "config:delivery-channel" {
			hasChannel = true
		}
	}
	if !hasRecorder || !hasChannel {
		t.Fatalf("missing recorder/channel nodes")
	}
}
