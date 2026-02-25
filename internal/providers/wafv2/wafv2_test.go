package wafv2

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkwaf "github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

type fakeWAF struct{}

func (fakeWAF) ListWebACLs(ctx context.Context, params *sdkwaf.ListWebACLsInput, optFns ...func(*sdkwaf.Options)) (*sdkwaf.ListWebACLsOutput, error) {
	scope := params.Scope
	if scope == types.ScopeCloudfront {
		return &sdkwaf.ListWebACLsOutput{WebACLs: []types.WebACLSummary{{
			ARN:  awsSDK.String("arn:aws:wafv2:us-east-1:123456789012:global/webacl/cf/1"),
			Id:   awsSDK.String("cf-1"),
			Name: awsSDK.String("cf-acl"),
		}}}, nil
	}
	return &sdkwaf.ListWebACLsOutput{WebACLs: []types.WebACLSummary{{
		ARN:  awsSDK.String("arn:aws:wafv2:us-east-1:123456789012:regional/webacl/regional/1"),
		Id:   awsSDK.String("reg-1"),
		Name: awsSDK.String("regional-acl"),
	}}}, nil
}

func TestProvider_List_ProducesACLNodes(t *testing.T) {
	p := New()
	p.newWAF = func(cfg awsSDK.Config) wafAPI { return fakeWAF{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) < 2 {
		t.Fatalf("expected >=2 nodes, got %d", len(res.Nodes))
	}
}
