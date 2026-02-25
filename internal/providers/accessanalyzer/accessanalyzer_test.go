package accessanalyzer

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkaccess "github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

type fakeAccessAnalyzer struct{}

func (fakeAccessAnalyzer) ListAnalyzers(ctx context.Context, params *sdkaccess.ListAnalyzersInput, optFns ...func(*sdkaccess.Options)) (*sdkaccess.ListAnalyzersOutput, error) {
	now := time.Now().UTC()
	return &sdkaccess.ListAnalyzersOutput{Analyzers: []types.AnalyzerSummary{{
		Arn:       awsSDK.String("arn:aws:access-analyzer:us-east-1:123456789012:analyzer/default"),
		Name:      awsSDK.String("default"),
		Status:    types.AnalyzerStatusActive,
		Type:      types.TypeAccount,
		CreatedAt: &now,
	}}}, nil
}

func TestProvider_List_ProducesAnalyzer(t *testing.T) {
	p := New()
	p.newAccessAnalyzer = func(cfg awsSDK.Config) accessAnalyzerAPI { return fakeAccessAnalyzer{} }
	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(res.Nodes))
	}
	if res.Nodes[0].Type != "accessanalyzer:analyzer" {
		t.Fatalf("unexpected type: %s", res.Nodes[0].Type)
	}
}
