package guardduty

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkguardduty "github.com/aws/aws-sdk-go-v2/service/guardduty"
	"github.com/aws/aws-sdk-go-v2/service/guardduty/types"
)

type fakeGuardDuty struct{}

func (fakeGuardDuty) ListDetectors(ctx context.Context, params *sdkguardduty.ListDetectorsInput, optFns ...func(*sdkguardduty.Options)) (*sdkguardduty.ListDetectorsOutput, error) {
	return &sdkguardduty.ListDetectorsOutput{DetectorIds: []string{"det-1"}}, nil
}

func (fakeGuardDuty) GetDetector(ctx context.Context, params *sdkguardduty.GetDetectorInput, optFns ...func(*sdkguardduty.Options)) (*sdkguardduty.GetDetectorOutput, error) {
	return &sdkguardduty.GetDetectorOutput{Status: types.DetectorStatusEnabled, ServiceRole: awsSDK.String("arn:aws:iam::123456789012:role/aws-service-role/guardduty")}, nil
}

func TestProvider_List_ProducesDetector(t *testing.T) {
	p := New()
	p.newGuardDuty = func(cfg awsSDK.Config) guardDutyAPI { return fakeGuardDuty{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	found := false
	for _, n := range res.Nodes {
		if n.Type == "guardduty:detector" {
			found = true
		}
	}
	if !found {
		t.Fatalf("detector node not found")
	}
}
