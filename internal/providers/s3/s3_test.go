package s3

import (
	"context"
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdks3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeS3API struct {
	buckets []types.Bucket
	loc     map[string]string
}

func (f *fakeS3API) ListBuckets(ctx context.Context, params *sdks3.ListBucketsInput, optFns ...func(*sdks3.Options)) (*sdks3.ListBucketsOutput, error) {
	return &sdks3.ListBucketsOutput{Buckets: f.buckets}, nil
}

func (f *fakeS3API) GetBucketLocation(ctx context.Context, params *sdks3.GetBucketLocationInput, optFns ...func(*sdks3.Options)) (*sdks3.GetBucketLocationOutput, error) {
	b := ""
	if params != nil && params.Bucket != nil {
		b = *params.Bucket
	}
	loc := f.loc[b]
	return &sdks3.GetBucketLocationOutput{LocationConstraint: types.BucketLocationConstraint(loc)}, nil
}

func (f *fakeS3API) GetBucketEncryption(ctx context.Context, params *sdks3.GetBucketEncryptionInput, optFns ...func(*sdks3.Options)) (*sdks3.GetBucketEncryptionOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "ServerSideEncryptionConfigurationNotFoundError", Message: "not configured"}
}

func (f *fakeS3API) GetPublicAccessBlock(ctx context.Context, params *sdks3.GetPublicAccessBlockInput, optFns ...func(*sdks3.Options)) (*sdks3.GetPublicAccessBlockOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "NoSuchPublicAccessBlockConfiguration", Message: "not configured"}
}

func TestProvider_Scope_IsAccount(t *testing.T) {
	p := New()
	if p.Scope() != providers.ScopeAccount {
		t.Fatalf("scope: got %v want %v", p.Scope(), providers.ScopeAccount)
	}
}

func TestProvider_List_FiltersByRegions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

	n1 := "b1"
	n2 := "b2"
	p := New()
	p.newS3 = func(cfg awsSDK.Config) s3API {
		return &fakeS3API{
			buckets: []types.Bucket{
				{Name: &n1, CreationDate: &now},
				{Name: &n2, CreationDate: &now},
			},
			loc: map[string]string{
				"b1": "us-west-2",
				"b2": "us-east-1",
			},
		}
	}

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
	_, _, gotRegion, _, pid, err := graph.ParseResourceKey(res.Nodes[0].Key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	if gotRegion != "us-east-1" || pid != "b2" {
		t.Fatalf("node: region=%q pid=%q", gotRegion, pid)
	}
}
