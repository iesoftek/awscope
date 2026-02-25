package cloudfront

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudfront "github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

type fakeCloudFront struct{}

func (fakeCloudFront) ListDistributions(ctx context.Context, params *sdkcloudfront.ListDistributionsInput, optFns ...func(*sdkcloudfront.Options)) (*sdkcloudfront.ListDistributionsOutput, error) {
	return &sdkcloudfront.ListDistributionsOutput{DistributionList: &types.DistributionList{
		IsTruncated: awsSDK.Bool(false),
		Items: []types.DistributionSummary{{
			ARN:     awsSDK.String("arn:aws:cloudfront::123456789012:distribution/ABC"),
			Id:      awsSDK.String("ABC"),
			Status:  awsSDK.String("Deployed"),
			Enabled: awsSDK.Bool(true),
			Origins: &types.Origins{Items: []types.Origin{
				{DomainName: awsSDK.String("my-bucket.s3.amazonaws.com")},
				{DomainName: awsSDK.String("a1b2c3.execute-api.us-west-2.amazonaws.com")},
			}},
			ViewerCertificate: &types.ViewerCertificate{
				ACMCertificateArn: awsSDK.String("arn:aws:acm:us-east-1:123456789012:certificate/cert-1"),
			},
			WebACLId: awsSDK.String("arn:aws:wafv2:us-east-1:123456789012:global/webacl/demo/1"),
		},
		},
	}}, nil
}

func TestProvider_List_ProducesDistribution(t *testing.T) {
	p := New()
	p.newCloudFront = func(cfg awsSDK.Config) cloudFrontAPI { return fakeCloudFront{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"global"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
	hasAPIGatewayEdge := false
	for _, e := range res.Edges {
		if e.Kind == "uses" {
			hasAPIGatewayEdge = true
			break
		}
	}
	if !hasAPIGatewayEdge {
		t.Fatalf("expected uses edges for linked resources")
	}
}
