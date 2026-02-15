package sns

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
)

type fakeSNSAPI struct{}

func (f fakeSNSAPI) ListTopics(ctx context.Context, params *sdksns.ListTopicsInput, optFns ...func(*sdksns.Options)) (*sdksns.ListTopicsOutput, error) {
	arn := "arn:aws:sns:us-east-1:123456789012:t1"
	return &sdksns.ListTopicsOutput{Topics: []types.Topic{{TopicArn: &arn}}}, nil
}

func (f fakeSNSAPI) ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error) {
	subArn := "arn:aws:sns:us-east-1:123456789012:t1:sub1"
	proto := "sqs"
	endpoint := "arn:aws:sqs:us-east-1:123456789012:q1"
	return &sdksns.ListSubscriptionsByTopicOutput{
		Subscriptions: []types.Subscription{{SubscriptionArn: &subArn, Protocol: &proto, Endpoint: &endpoint}},
	}, nil
}

func TestProvider_List_EmitsTopicAndSubscription(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newSNS = func(cfg awsSDK.Config) snsAPI { return fakeSNSAPI{} }

	res, err := p.List(ctx, awsSDK.Config{Region: "us-east-1"}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"us-east-1"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("nodes: got %d want 2", len(res.Nodes))
	}
	if len(res.Edges) != 2 {
		t.Fatalf("edges: got %d want 2", len(res.Edges))
	}
}
