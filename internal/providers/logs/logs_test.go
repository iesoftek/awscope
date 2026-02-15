package logs

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdklogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type fakeLogs struct {
	out *sdklogs.DescribeLogGroupsOutput
	err error
}

func (f fakeLogs) DescribeLogGroups(ctx context.Context, params *sdklogs.DescribeLogGroupsInput, optFns ...func(*sdklogs.Options)) (*sdklogs.DescribeLogGroupsOutput, error) {
	_ = ctx
	_ = params
	_ = optFns
	return f.out, f.err
}

func TestProvider_List_ProducesLogGroupNodesAndKMSEdge(t *testing.T) {
	now := time.Now().UTC()
	ms := now.UnixMilli()
	stored := int64(1024 * 1024 * 1024) // 1 GiB
	ret := int32(30)

	p := New()
	p.newLogs = func(cfg awsSDK.Config) logsAPI {
		_ = cfg
		return fakeLogs{out: &sdklogs.DescribeLogGroupsOutput{
			LogGroups: []types.LogGroup{
				{
					LogGroupName:    awsSDK.String("/aws/lambda/demo"),
					Arn:             awsSDK.String("arn:aws:logs:us-west-2:123456789012:log-group:/aws/lambda/demo:*"),
					StoredBytes:     &stored,
					RetentionInDays: &ret,
					CreationTime:    &ms,
					KmsKeyId:        awsSDK.String("arn:aws:kms:us-west-2:123456789012:key/abc"),
					LogGroupClass:   types.LogGroupClassStandard,
				},
			},
		}}
	}

	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"us-west-2"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	var found bool
	for _, n := range res.Nodes {
		if n.Service == "logs" && n.Type == "logs:log-group" {
			found = true
			if n.DisplayName != "/aws/lambda/demo" {
				t.Fatalf("displayName: got %q", n.DisplayName)
			}
			if n.Attributes["retentionDays"].(int32) != 30 {
				t.Fatalf("retentionDays: %#v", n.Attributes["retentionDays"])
			}
			if n.Attributes["storedBytes"].(int64) != stored {
				t.Fatalf("storedBytes: %#v", n.Attributes["storedBytes"])
			}
			if _, ok := n.Attributes["created_at"].(string); !ok {
				t.Fatalf("created_at missing")
			}
		}
	}
	if !found {
		t.Fatalf("log group node not found")
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
	var hasUses bool
	for _, e := range res.Edges {
		if e.Kind == "uses" {
			hasUses = true
		}
	}
	if !hasUses {
		t.Fatalf("expected uses edge to kms key")
	}
}

