package apigateway

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkapigateway "github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigateway/types"
)

type fakeAPIGateway struct{}

func (fakeAPIGateway) GetRestApis(ctx context.Context, params *sdkapigateway.GetRestApisInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetRestApisOutput, error) {
	now := time.Now().UTC()
	return &sdkapigateway.GetRestApisOutput{Items: []types.RestApi{{
		Id:          awsSDK.String("a1b2c3"),
		Name:        awsSDK.String("demo"),
		ApiStatus:   types.ApiStatusAvailable,
		CreatedDate: &now,
		EndpointConfiguration: &types.EndpointConfiguration{
			Types:          []types.EndpointType{types.EndpointTypePrivate},
			VpcEndpointIds: []string{"vpce-123"},
		},
	}}}, nil
}

func (fakeAPIGateway) GetDomainNames(ctx context.Context, params *sdkapigateway.GetDomainNamesInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetDomainNamesOutput, error) {
	return &sdkapigateway.GetDomainNamesOutput{Items: []types.DomainName{{
			DomainName:             awsSDK.String("api.example.com"),
			DomainNameArn:          awsSDK.String("arn:aws:apigateway:us-east-1::/domainnames/api.example.com"),
			DomainNameStatus:       types.DomainNameStatusAvailable,
			RegionalCertificateArn: awsSDK.String("arn:aws:acm:us-east-1:123456789012:certificate/cert-1"),
		}}},
		nil
}

func (fakeAPIGateway) GetBasePathMappings(ctx context.Context, params *sdkapigateway.GetBasePathMappingsInput, optFns ...func(*sdkapigateway.Options)) (*sdkapigateway.GetBasePathMappingsOutput, error) {
	return &sdkapigateway.GetBasePathMappingsOutput{Items: []types.BasePathMapping{{
		BasePath:  awsSDK.String("v1"),
		RestApiId: awsSDK.String("a1b2c3"),
		Stage:     awsSDK.String("prod"),
	}}}, nil
}

func TestProvider_List_ProducesAPI(t *testing.T) {
	p := New()
	p.newAPIGateway = func(cfg awsSDK.Config) apiGatewayAPI { return fakeAPIGateway{} }
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
	hasDomain := false
	for _, n := range res.Nodes {
		if n.Type == "apigateway:domain-name" {
			hasDomain = true
			break
		}
	}
	if !hasDomain {
		t.Fatalf("expected apigateway:domain-name node")
	}
}
