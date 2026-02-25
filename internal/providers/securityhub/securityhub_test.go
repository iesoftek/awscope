package securityhub

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksecurityhub "github.com/aws/aws-sdk-go-v2/service/securityhub"
	"github.com/aws/aws-sdk-go-v2/service/securityhub/types"
)

type fakeSecurityHub struct{}

func (fakeSecurityHub) DescribeHub(ctx context.Context, params *sdksecurityhub.DescribeHubInput, optFns ...func(*sdksecurityhub.Options)) (*sdksecurityhub.DescribeHubOutput, error) {
	return &sdksecurityhub.DescribeHubOutput{HubArn: awsSDK.String("arn:aws:securityhub:us-east-1:123456789012:hub/default"), SubscribedAt: awsSDK.String("2026-02-01T00:00:00Z")}, nil
}

func (fakeSecurityHub) GetEnabledStandards(ctx context.Context, params *sdksecurityhub.GetEnabledStandardsInput, optFns ...func(*sdksecurityhub.Options)) (*sdksecurityhub.GetEnabledStandardsOutput, error) {
	return &sdksecurityhub.GetEnabledStandardsOutput{StandardsSubscriptions: []types.StandardsSubscription{{
		StandardsArn:             awsSDK.String("arn:aws:securityhub:::ruleset/cis-aws-foundations-benchmark/v/1.2.0"),
		StandardsSubscriptionArn: awsSDK.String("arn:aws:securityhub:us-east-1:123456789012:subscription/cis"),
		StandardsStatus:          types.StandardsStatusReady,
	}}}, nil
}

func TestProvider_List_ProducesHubAndStandards(t *testing.T) {
	p := New()
	p.newSecurityHub = func(cfg awsSDK.Config) securityHubAPI { return fakeSecurityHub{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) < 2 {
		t.Fatalf("expected >=2 nodes, got %d", len(res.Nodes))
	}
}
