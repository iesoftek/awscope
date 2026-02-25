package ecr

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

type fakeECR struct{}

func (fakeECR) DescribeRepositories(ctx context.Context, params *sdkecr.DescribeRepositoriesInput, optFns ...func(*sdkecr.Options)) (*sdkecr.DescribeRepositoriesOutput, error) {
	now := time.Now().UTC()
	return &sdkecr.DescribeRepositoriesOutput{Repositories: []types.Repository{{
		RepositoryArn:           awsSDK.String("arn:aws:ecr:us-east-1:123456789012:repository/demo"),
		RepositoryName:          awsSDK.String("demo"),
		CreatedAt:               &now,
		EncryptionConfiguration: &types.EncryptionConfiguration{KmsKey: awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc")},
	}}}, nil
}

func TestProvider_List_ProducesRepo(t *testing.T) {
	p := New()
	p.newECR = func(cfg awsSDK.Config) ecrAPI { return fakeECR{} }
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
