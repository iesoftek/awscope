package acm

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkacm "github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/acm/types"
)

type fakeACM struct{}

func (fakeACM) ListCertificates(ctx context.Context, params *sdkacm.ListCertificatesInput, optFns ...func(*sdkacm.Options)) (*sdkacm.ListCertificatesOutput, error) {
	now := time.Now().UTC()
	return &sdkacm.ListCertificatesOutput{CertificateSummaryList: []types.CertificateSummary{{
		CertificateArn: awsSDK.String("arn:aws:acm:us-east-1:123456789012:certificate/abc"),
		DomainName:     awsSDK.String("example.com"),
		Status:         types.CertificateStatusIssued,
		CreatedAt:      &now,
	}}}, nil
}

func (fakeACM) DescribeCertificate(ctx context.Context, params *sdkacm.DescribeCertificateInput, optFns ...func(*sdkacm.Options)) (*sdkacm.DescribeCertificateOutput, error) {
	now := time.Now().UTC()
	return &sdkacm.DescribeCertificateOutput{Certificate: &types.CertificateDetail{
		CertificateArn: awsSDK.String("arn:aws:acm:us-east-1:123456789012:certificate/abc"),
		CreatedAt:      &now,
		Status:         types.CertificateStatusIssued,
		InUseBy:        []string{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/demo/123"},
	}}, nil
}

func TestProvider_List_ProducesCertificateAndEdge(t *testing.T) {
	p := New()
	p.newACM = func(cfg awsSDK.Config) acmAPI { return fakeACM{} }
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
