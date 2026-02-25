package audittrail

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"
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
	rowsByID := map[string]store.CloudTrailEventRow{}
	for _, row := range res.Rows {
		rowsByID[row.EventID] = row
	}
	if row := rowsByID["evt-1"]; row.EventID != "evt-1" || row.ResourceKey != instanceKey {
		t.Fatalf("evt-1 row not resolved as expected: %#v", row)
	}
	if row := rowsByID["evt-3"]; row.EventID != "evt-3" || row.Action != "delete" {
		t.Fatalf("evt-3 row unexpected: %#v", row)
	}
}

type concurrentLookupAPI struct {
	mu       sync.Mutex
	calls    map[string]int
	inFlight int32
	maxSeen  int32
	barrier  chan struct{}
	enterN   int32
}

func (f *concurrentLookupAPI) LookupEvents(ctx context.Context, params *sdkcloudtrail.LookupEventsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.LookupEventsOutput, error) {
	source := ""
	if len(params.LookupAttributes) == 1 && params.LookupAttributes[0].AttributeValue != nil {
		source = awsSDK.ToString(params.LookupAttributes[0].AttributeValue)
	}

	n := atomic.AddInt32(&f.inFlight, 1)
	for {
		cur := atomic.LoadInt32(&f.maxSeen)
		if n <= cur || atomic.CompareAndSwapInt32(&f.maxSeen, cur, n) {
			break
		}
	}
	defer atomic.AddInt32(&f.inFlight, -1)

	if f.barrier != nil {
		enter := atomic.AddInt32(&f.enterN, 1)
		if enter == 2 {
			close(f.barrier)
		}
		if enter <= 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-f.barrier:
			}
		}
	}

	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	page := f.calls[source]
	f.calls[source] = page + 1
	f.mu.Unlock()

	event := func(id, name, source string) types.Event {
		return types.Event{
			EventId:     awsSDK.String(id),
			EventName:   awsSDK.String(name),
			EventSource: awsSDK.String(source),
			EventTime:   awsSDK.Time(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)),
			CloudTrailEvent: awsSDK.String(`{
  "eventSource":"` + source + `",
  "eventName":"` + name + `"
}`),
		}
	}

	switch source {
	case "ec2.amazonaws.com":
		if page == 0 {
			return &sdkcloudtrail.LookupEventsOutput{
				Events:    []types.Event{event("evt-dup", "RunInstances", source)},
				NextToken: awsSDK.String("next"),
			}, nil
		}
		return &sdkcloudtrail.LookupEventsOutput{
			Events: []types.Event{event("evt-dup", "RunInstances", source)},
		}, nil
	case "iam.amazonaws.com":
		return &sdkcloudtrail.LookupEventsOutput{
			Events: []types.Event{event("evt-iam", "DeleteRole", source)},
		}, nil
	default:
		return &sdkcloudtrail.LookupEventsOutput{}, nil
	}
}

func TestIndexer_IndexRegion_SourceConcurrencyAndDedupe(t *testing.T) {
	i := NewIndexer(awsSDK.Config{}, "111111111111", "aws", nil, Options{
		WindowDays:         7,
		MaxEventsPerRegion: 100,
		LookupInterval:     0,
		SourceConcurrency:  3,
	})
	api := &concurrentLookupAPI{barrier: make(chan struct{})}
	i.newCloudTrail = func(cfg awsSDK.Config) cloudTrailLookupAPI { return api }

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
	if got := atomic.LoadInt32(&api.maxSeen); got < 2 {
		t.Fatalf("expected concurrent source lookups, max in-flight=%d", got)
	}
}

type throttleAPIError struct{}

func (throttleAPIError) Error() string        { return "ThrottlingException: Rate exceeded" }
func (throttleAPIError) ErrorCode() string    { return "ThrottlingException" }
func (throttleAPIError) ErrorMessage() string { return "Rate exceeded" }
func (throttleAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

type throttlingLookupAPI struct {
	attempts atomic.Int32
}

func (f *throttlingLookupAPI) LookupEvents(ctx context.Context, params *sdkcloudtrail.LookupEventsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.LookupEventsOutput, error) {
	source := ""
	if len(params.LookupAttributes) == 1 && params.LookupAttributes[0].AttributeValue != nil {
		source = awsSDK.ToString(params.LookupAttributes[0].AttributeValue)
	}
	if source == "ec2.amazonaws.com" {
		n := f.attempts.Add(1)
		if n <= 2 {
			return nil, throttleAPIError{}
		}
		return &sdkcloudtrail.LookupEventsOutput{
			Events: []types.Event{
				{
					EventId:     awsSDK.String("evt-throttle"),
					EventName:   awsSDK.String("RunInstances"),
					EventSource: awsSDK.String("ec2.amazonaws.com"),
					EventTime:   awsSDK.Time(time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)),
					CloudTrailEvent: awsSDK.String(`{
  "eventSource":"ec2.amazonaws.com",
  "eventName":"RunInstances"
}`),
				},
			},
		}, nil
	}
	return &sdkcloudtrail.LookupEventsOutput{}, nil
}

func TestIndexer_IndexRegion_RetriesThrottling(t *testing.T) {
	i := NewIndexer(awsSDK.Config{}, "111111111111", "aws", nil, Options{
		WindowDays:         7,
		MaxEventsPerRegion: 100,
		LookupInterval:     0,
		SourceConcurrency:  1,
	})
	api := &throttlingLookupAPI{}
	i.newCloudTrail = func(cfg awsSDK.Config) cloudTrailLookupAPI { return api }

	res, err := i.IndexRegion(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("IndexRegion: %v", err)
	}
	if res.Summary.Indexed != 1 {
		t.Fatalf("indexed: got %d want 1", res.Summary.Indexed)
	}
	if got := api.attempts.Load(); got < 3 {
		t.Fatalf("expected retries on throttling, attempts=%d", got)
	}
}
