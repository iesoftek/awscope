package secretsmanager

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksec "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type fakeSMAPI struct{}

func (f fakeSMAPI) ListSecrets(ctx context.Context, params *sdksec.ListSecretsInput, optFns ...func(*sdksec.Options)) (*sdksec.ListSecretsOutput, error) {
	arn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:s1"
	name := "s1"
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	rot := true
	return &sdksec.ListSecretsOutput{
		SecretList: []types.SecretListEntry{{ARN: &arn, Name: &name, CreatedDate: &now, RotationEnabled: &rot}},
	}, nil
}

func (f fakeSMAPI) DescribeSecret(ctx context.Context, params *sdksec.DescribeSecretInput, optFns ...func(*sdksec.Options)) (*sdksec.DescribeSecretOutput, error) {
	rl := "arn:aws:lambda:us-east-1:123456789012:function:rot"
	return &sdksec.DescribeSecretOutput{RotationLambdaARN: &rl}, nil
}

func TestProvider_List_EmitsSecret(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newSM = func(cfg awsSDK.Config) smAPI { return fakeSMAPI{} }

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
}
