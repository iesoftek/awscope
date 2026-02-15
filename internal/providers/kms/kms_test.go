package kms

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkkms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type fakeKMSAPI struct{}

func (f fakeKMSAPI) ListKeys(ctx context.Context, params *sdkkms.ListKeysInput, optFns ...func(*sdkkms.Options)) (*sdkkms.ListKeysOutput, error) {
	id := "kid"
	return &sdkkms.ListKeysOutput{Keys: []types.KeyListEntry{{KeyId: &id}}}, nil
}

func (f fakeKMSAPI) DescribeKey(ctx context.Context, params *sdkkms.DescribeKeyInput, optFns ...func(*sdkkms.Options)) (*sdkkms.DescribeKeyOutput, error) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:kms:us-east-1:123456789012:key/abc"
	id := "abc"
	md := types.KeyMetadata{
		Arn:          &arn,
		KeyId:        &id,
		KeyManager:   types.KeyManagerTypeCustomer,
		KeyState:     types.KeyStateEnabled,
		CreationDate: &now,
	}
	return &sdkkms.DescribeKeyOutput{KeyMetadata: &md}, nil
}

func (f fakeKMSAPI) ListAliases(ctx context.Context, params *sdkkms.ListAliasesInput, optFns ...func(*sdkkms.Options)) (*sdkkms.ListAliasesOutput, error) {
	name := "alias/test"
	arn := "arn:aws:kms:us-east-1:123456789012:alias/test"
	return &sdkkms.ListAliasesOutput{Aliases: []types.AliasListEntry{{AliasName: &name, AliasArn: &arn}}}, nil
}

func TestProvider_Scope_IsRegional(t *testing.T) {
	if New().Scope() != providers.ScopeRegional {
		t.Fatalf("scope mismatch")
	}
}

func TestProvider_ListRegion_EmitsKeyAndAlias(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newKMS = func(cfg awsSDK.Config) kmsAPI { return fakeKMSAPI{} }

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
}
