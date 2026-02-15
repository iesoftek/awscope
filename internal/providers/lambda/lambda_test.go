package lambda

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdklambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type fakeLambdaAPI struct{}

func (f fakeLambdaAPI) ListFunctions(ctx context.Context, params *sdklambda.ListFunctionsInput, optFns ...func(*sdklambda.Options)) (*sdklambda.ListFunctionsOutput, error) {
	arn := "arn:aws:lambda:us-east-1:123456789012:function:f1"
	name := "f1"
	role := "arn:aws:iam::123456789012:role/r1"
	return &sdklambda.ListFunctionsOutput{
		Functions: []types.FunctionConfiguration{
			{FunctionArn: &arn, FunctionName: &name, Role: &role},
		},
	}, nil
}

func (f fakeLambdaAPI) ListEventSourceMappings(ctx context.Context, params *sdklambda.ListEventSourceMappingsInput, optFns ...func(*sdklambda.Options)) (*sdklambda.ListEventSourceMappingsOutput, error) {
	fnArn := "arn:aws:lambda:us-east-1:123456789012:function:f1"
	srcArn := "arn:aws:sqs:us-east-1:123456789012:q1"
	return &sdklambda.ListEventSourceMappingsOutput{
		EventSourceMappings: []types.EventSourceMappingConfiguration{
			{FunctionArn: &fnArn, EventSourceArn: &srcArn},
		},
	}, nil
}

func TestProvider_List_EmitsFunctionAndEdges(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newLambda = func(cfg awsSDK.Config) lambdaAPI { return fakeLambdaAPI{} }

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
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
}
