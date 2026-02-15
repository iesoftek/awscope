package dynamodb

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDDBAPI struct{}

func (f fakeDDBAPI) ListTables(ctx context.Context, params *sdkddb.ListTablesInput, optFns ...func(*sdkddb.Options)) (*sdkddb.ListTablesOutput, error) {
	return &sdkddb.ListTablesOutput{TableNames: []string{"t1"}}, nil
}

func (f fakeDDBAPI) DescribeTable(ctx context.Context, params *sdkddb.DescribeTableInput, optFns ...func(*sdkddb.Options)) (*sdkddb.DescribeTableOutput, error) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:dynamodb:us-east-1:123456789012:table/t1"
	kmsArn := "arn:aws:kms:us-east-1:123456789012:key/abc"
	return &sdkddb.DescribeTableOutput{
		Table: &types.TableDescription{
			TableName:        params.TableName,
			TableArn:         &arn,
			TableStatus:      types.TableStatusActive,
			CreationDateTime: &now,
			SSEDescription:   &types.SSEDescription{Status: types.SSEStatusEnabled, KMSMasterKeyArn: &kmsArn},
		},
	}, nil
}

func TestProvider_Scope_IsRegional(t *testing.T) {
	if New().Scope() != providers.ScopeRegional {
		t.Fatalf("scope mismatch")
	}
}

func TestProvider_List_EmitsTables(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newDDB = func(cfg awsSDK.Config) ddbAPI { return fakeDDBAPI{} }

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
	if res.Nodes[0].Type != "dynamodb:table" {
		t.Fatalf("node: %#v", res.Nodes[0])
	}
	if len(res.Edges) != 1 {
		t.Fatalf("edges: got %d want 1", len(res.Edges))
	}
}
