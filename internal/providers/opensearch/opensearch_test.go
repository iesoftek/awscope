package opensearch

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkopensearch "github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"
)

type fakeOpenSearch struct{}

func (fakeOpenSearch) ListDomainNames(ctx context.Context, params *sdkopensearch.ListDomainNamesInput, optFns ...func(*sdkopensearch.Options)) (*sdkopensearch.ListDomainNamesOutput, error) {
	return &sdkopensearch.ListDomainNamesOutput{DomainNames: []types.DomainInfo{{DomainName: awsSDK.String("demo")}}}, nil
}

func (fakeOpenSearch) DescribeDomain(ctx context.Context, params *sdkopensearch.DescribeDomainInput, optFns ...func(*sdkopensearch.Options)) (*sdkopensearch.DescribeDomainOutput, error) {
	return &sdkopensearch.DescribeDomainOutput{DomainStatus: &types.DomainStatus{
		ARN:                     awsSDK.String("arn:aws:es:us-east-1:123456789012:domain/demo"),
		DomainName:              awsSDK.String("demo"),
		VPCOptions:              &types.VPCDerivedInfo{VPCId: awsSDK.String("vpc-1"), SubnetIds: []string{"subnet-1"}, SecurityGroupIds: []string{"sg-1"}},
		EncryptionAtRestOptions: &types.EncryptionAtRestOptions{KmsKeyId: awsSDK.String("arn:aws:kms:us-east-1:123456789012:key/abc")},
	}}, nil
}

func TestProvider_List_ProducesDomain(t *testing.T) {
	p := New()
	p.newOpenSearch = func(cfg awsSDK.Config) openSearchAPI { return fakeOpenSearch{} }
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
