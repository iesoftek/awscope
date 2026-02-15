package sqs

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeSQSAPI struct{}

func (f fakeSQSAPI) ListQueues(ctx context.Context, params *sdksqs.ListQueuesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueuesOutput, error) {
	return &sdksqs.ListQueuesOutput{QueueUrls: []string{"https://sqs.us-east-1.amazonaws.com/123456789012/q1"}}, nil
}

func (f fakeSQSAPI) GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error) {
	arn := "arn:aws:sqs:us-east-1:123456789012:q1"
	kms := "arn:aws:kms:us-east-1:123456789012:key/abc"
	return &sdksqs.GetQueueAttributesOutput{
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameQueueArn):             arn,
			string(sqstypes.QueueAttributeNameKmsMasterKeyId):       kms,
			string(sqstypes.QueueAttributeNameSqsManagedSseEnabled): "true",
		},
	}, nil
}

func TestProvider_List_EmitsQueue(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newSQS = func(cfg awsSDK.Config) sqsAPI { return fakeSQSAPI{} }

	res, err := p.List(ctx, awsSDK.Config{Region: "us-east-1"}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"us-east-1"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("nodes: got %d want 1", len(res.Nodes))
	}
	if len(res.Edges) != 1 {
		t.Fatalf("edges: got %d want 1", len(res.Edges))
	}
}
