package audittrail

import (
	"context"
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
)

type fakeLookupAPI struct {
	out *sdkcloudtrail.LookupEventsOutput
	err error
}

func (f *fakeLookupAPI) LookupEvents(ctx context.Context, params *sdkcloudtrail.LookupEventsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.LookupEventsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Mimic LookupAttribute EventSource server-side filtering.
	if len(params.LookupAttributes) == 1 &&
		params.LookupAttributes[0].AttributeKey == types.LookupAttributeKeyEventSource &&
		params.LookupAttributes[0].AttributeValue != nil {
		want := awsSDK.ToString(params.LookupAttributes[0].AttributeValue)
		out := &sdkcloudtrail.LookupEventsOutput{}
		for _, ev := range f.out.Events {
			if awsSDK.ToString(ev.EventSource) == want {
				out.Events = append(out.Events, ev)
			}
		}
		return out, nil
	}
	return f.out, nil
}

func TestIndexer_IndexRegion_FiltersAndResolves(t *testing.T) {
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	instanceKey := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-abc123")

	i := NewIndexer(awsSDK.Config{}, "111111111111", "aws", []store.ResourceLookup{
		{
			Key:         instanceKey,
			Region:      "us-east-1",
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-abc123",
			Arn:         "arn:aws:ec2:us-east-1:111111111111:instance/i-abc123",
			DisplayName: "web-1",
		},
	}, Options{WindowDays: 7, MaxEventsPerRegion: 10, LookupInterval: time.Millisecond})
	i.nowFunc = func() time.Time { return now }
	i.newCloudTrail = func(cfg awsSDK.Config) cloudTrailLookupAPI {
		return &fakeLookupAPI{
			out: &sdkcloudtrail.LookupEventsOutput{
				Events: []types.Event{
					{
						EventId:     awsSDK.String("evt-1"),
						EventName:   awsSDK.String("RunInstances"),
						EventSource: awsSDK.String("ec2.amazonaws.com"),
						Username:    awsSDK.String("alice"),
						EventTime:   awsSDK.Time(now.Add(-time.Hour)),
						CloudTrailEvent: awsSDK.String(`{
  "eventTime":"2026-02-24T09:00:00Z",
  "eventSource":"ec2.amazonaws.com",
  "eventName":"RunInstances",
  "sourceIPAddress":"1.2.3.4",
  "requestParameters":{"instancesSet":{"items":[{"instanceId":"i-abc123"}]}}
}`),
						Resources: []types.Resource{
							{ResourceType: awsSDK.String("AWS::EC2::Instance"), ResourceName: awsSDK.String("i-abc123")},
						},
					},
					{
						EventId:         awsSDK.String("evt-2"),
						EventName:       awsSDK.String("ModifyInstanceAttribute"),
						EventSource:     awsSDK.String("ec2.amazonaws.com"),
						Username:        awsSDK.String("alice"),
						EventTime:       awsSDK.Time(now.Add(-30 * time.Minute)),
						CloudTrailEvent: awsSDK.String(`{"eventName":"ModifyInstanceAttribute","eventSource":"ec2.amazonaws.com"}`),
					},
					{
						EventId:     awsSDK.String("evt-3"),
						EventName:   awsSDK.String("DeleteRole"),
						EventSource: awsSDK.String("iam.amazonaws.com"),
						Username:    awsSDK.String("bob"),
						EventTime:   awsSDK.Time(now.Add(-20 * time.Minute)),
						CloudTrailEvent: awsSDK.String(`{
  "eventName":"DeleteRole",
  "eventSource":"iam.amazonaws.com",
  "requestParameters":{"roleName":"old-role"},
  "userIdentity":{"arn":"arn:aws:iam::111111111111:user/bob"}
}`),
					},
				},
			},
		}
	}

	res, err := i.IndexRegion(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("IndexRegion: %v", err)
	}
	if res.Summary.Indexed != 2 {
		t.Fatalf("indexed: got %d want 2", res.Summary.Indexed)
	}
	if res.Summary.Create != 1 || res.Summary.Delete != 1 {
		t.Fatalf("summary create/delete: %#v", res.Summary)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows len: got %d want 2", len(res.Rows))
	}
	if res.Rows[0].EventID != "evt-1" || res.Rows[0].ResourceKey != instanceKey {
		t.Fatalf("row1 not resolved as expected: %#v", res.Rows[0])
	}
	if res.Rows[1].EventID != "evt-3" || res.Rows[1].Action != "delete" {
		t.Fatalf("row2 unexpected: %#v", res.Rows[1])
	}
}
