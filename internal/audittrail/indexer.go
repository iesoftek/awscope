package audittrail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	defaultWindowDays         = 7
	defaultMaxEventsPerRegion = 2000
	defaultLookupInterval     = 500 * time.Millisecond
	defaultCompactMaxBytes    = 32768
)

type Options struct {
	WindowDays         int
	MaxEventsPerRegion int
	LookupInterval     time.Duration
	CompactMaxBytes    int
	MaxRegionDuration  time.Duration
	OnPage             func(PageProgress)
}

type RegionSummary struct {
	Indexed int
	Create  int
	Delete  int
	Pages   int
	Samples []string
}

type RegionResult struct {
	Rows      []store.CloudTrailEventRow
	Summary   RegionSummary
	Region    string
	Window    int
	Truncated bool
}

type PageProgress struct {
	Region     string
	Source     string
	Page       int
	Indexed    int
	RawFetched int
}

type Indexer struct {
	cfg       awsSDK.Config
	accountID string
	partition string
	opts      Options
	resolver  *resourceResolver

	newCloudTrail func(cfg awsSDK.Config) cloudTrailLookupAPI
	nowFunc       func() time.Time
}

type cloudTrailLookupAPI interface {
	LookupEvents(ctx context.Context, params *sdkcloudtrail.LookupEventsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.LookupEventsOutput, error)
}

func NewIndexer(cfg awsSDK.Config, accountID, partition string, lookups []store.ResourceLookup, opts Options) *Indexer {
	if opts.WindowDays <= 0 {
		opts.WindowDays = defaultWindowDays
	}
	if opts.MaxEventsPerRegion <= 0 {
		opts.MaxEventsPerRegion = defaultMaxEventsPerRegion
	}
	if opts.LookupInterval <= 0 {
		opts.LookupInterval = defaultLookupInterval
	}
	if opts.CompactMaxBytes <= 0 {
		opts.CompactMaxBytes = defaultCompactMaxBytes
	}
	if opts.MaxRegionDuration <= 0 {
		opts.MaxRegionDuration = 2 * time.Minute
	}
	return &Indexer{
		cfg:       cfg,
		accountID: strings.TrimSpace(accountID),
		partition: strings.TrimSpace(partition),
		opts:      opts,
		resolver:  newResourceResolver(partition, accountID, lookups),
		newCloudTrail: func(cfg awsSDK.Config) cloudTrailLookupAPI {
			return sdkcloudtrail.NewFromConfig(cfg)
		},
		nowFunc: func() time.Time { return time.Now().UTC() },
	}
}

func (i *Indexer) IndexRegion(ctx context.Context, region string) (RegionResult, error) {
	region = strings.TrimSpace(region)
	if region == "" || strings.EqualFold(region, "global") {
		return RegionResult{Region: region, Window: i.opts.WindowDays}, nil
	}

	c := i.cfg
	c.Region = region
	api := i.newCloudTrail(c)

	now := i.nowFunc().UTC()
	start := now.AddDate(0, 0, -i.opts.WindowDays)
	indexedAt := now
	startedAt := time.Now()
	ticker := time.NewTicker(i.opts.LookupInterval)
	defer ticker.Stop()
	first := true

	var (
		rows      []store.CloudTrailEventRow
		summary   RegionSummary
		truncated bool
	)

	for _, source := range allowedEventSources() {
		var nextToken *string
		seenTokens := map[string]struct{}{}
		for {
			if i.opts.MaxRegionDuration > 0 && time.Since(startedAt) >= i.opts.MaxRegionDuration {
				truncated = true
				break
			}

			if !first {
				select {
				case <-ctx.Done():
					return RegionResult{}, ctx.Err()
				case <-ticker.C:
				}
			}
			first = false

			out, err := api.LookupEvents(ctx, &sdkcloudtrail.LookupEventsInput{
				StartTime: awsSDK.Time(start),
				EndTime:   awsSDK.Time(now),
				LookupAttributes: []types.LookupAttribute{
					{
						AttributeKey:   types.LookupAttributeKeyEventSource,
						AttributeValue: awsSDK.String(source),
					},
				},
				MaxResults: awsSDK.Int32(50),
				NextToken:  nextToken,
			})
			if err != nil {
				return RegionResult{}, err
			}
			summary.Pages++

			for _, ev := range out.Events {
				row, ok := i.normalizeEvent(region, ev, indexedAt)
				if !ok {
					continue
				}
				rows = append(rows, row)
				summary.Indexed++
				switch row.Action {
				case "create":
					summary.Create++
				case "delete":
					summary.Delete++
				}
				if len(summary.Samples) < 12 {
					label := strings.TrimSpace(fmt.Sprintf("%s %s", row.EventName, row.ResourceName))
					if label == "" {
						label = row.EventName
					}
					if label != "" {
						summary.Samples = append(summary.Samples, label)
					}
				}
				if len(rows) >= i.opts.MaxEventsPerRegion {
					truncated = true
					break
				}
			}
			if i.opts.OnPage != nil {
				i.opts.OnPage(PageProgress{
					Region:     region,
					Source:     source,
					Page:       summary.Pages,
					Indexed:    summary.Indexed,
					RawFetched: len(out.Events),
				})
			}

			if truncated {
				break
			}
			if out.NextToken == nil || strings.TrimSpace(awsSDK.ToString(out.NextToken)) == "" {
				break
			}

			next := strings.TrimSpace(awsSDK.ToString(out.NextToken))
			if next == "" {
				break
			}
			// Guard against pathological token loops.
			if _, seen := seenTokens[next]; seen {
				truncated = true
				break
			}
			seenTokens[next] = struct{}{}
			nextToken = out.NextToken
		}
		if truncated {
			break
		}
	}

	return RegionResult{
		Rows:      rows,
		Summary:   summary,
		Region:    region,
		Window:    i.opts.WindowDays,
		Truncated: truncated,
	}, nil
}

func (i *Indexer) normalizeEvent(region string, ev types.Event, indexedAt time.Time) (store.CloudTrailEventRow, bool) {
	eventSource := strings.TrimSpace(awsSDK.ToString(ev.EventSource))
	service, ok := normalizeService(eventSource)
	if !ok {
		return store.CloudTrailEventRow{}, false
	}

	eventName := strings.TrimSpace(awsSDK.ToString(ev.EventName))
	action, ok := classifyAction(eventName)
	if !ok {
		return store.CloudTrailEventRow{}, false
	}

	eventID := strings.TrimSpace(awsSDK.ToString(ev.EventId))
	if eventID == "" {
		return store.CloudTrailEventRow{}, false
	}

	var full map[string]any
	rawEvent := strings.TrimSpace(awsSDK.ToString(ev.CloudTrailEvent))
	if rawEvent != "" && json.Valid([]byte(rawEvent)) {
		_ = json.Unmarshal([]byte(rawEvent), &full)
	}
	if full == nil {
		full = map[string]any{}
	}

	eventTime := indexedAt
	if ev.EventTime != nil && !ev.EventTime.IsZero() {
		eventTime = ev.EventTime.UTC()
	} else if ts := strings.TrimSpace(strAt(full, "eventTime")); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			eventTime = t.UTC()
		}
	}

	resources := make([]EventResource, 0, len(ev.Resources))
	for _, r := range ev.Resources {
		name := strings.TrimSpace(awsSDK.ToString(r.ResourceName))
		arn := ""
		if strings.HasPrefix(name, "arn:") {
			arn = name
		}
		resources = append(resources, EventResource{
			ResourceType: strings.TrimSpace(awsSDK.ToString(r.ResourceType)),
			ResourceName: name,
			ResourceArn:  arn,
		})
	}

	rr := i.resolver.Resolve(ResolveInput{
		Service:   service,
		Region:    region,
		EventName: eventName,
		Event:     full,
		Resources: resources,
	})

	username := strings.TrimSpace(awsSDK.ToString(ev.Username))
	if username == "" {
		username = firstNonEmpty(strAt(full, "userIdentity", "userName"), strAt(full, "userIdentity", "principalId"))
	}
	principalArn := strings.TrimSpace(strAt(full, "userIdentity", "arn"))
	sourceIP := strings.TrimSpace(strAt(full, "sourceIPAddress"))
	userAgent := strings.TrimSpace(strAt(full, "userAgent"))
	readOnly := strings.TrimSpace(strAt(full, "readOnly"))
	errorCode := strings.TrimSpace(strAt(full, "errorCode"))
	errorMessage := strings.TrimSpace(strAt(full, "errorMessage"))

	compact := compactEventJSON(full, i.opts.CompactMaxBytes)
	return store.CloudTrailEventRow{
		EventID:      eventID,
		AccountID:    i.accountID,
		Partition:    i.partition,
		Region:       region,
		EventTime:    eventTime,
		EventSource:  eventSource,
		EventName:    eventName,
		Action:       action,
		Service:      service,
		ResourceKey:  rr.Key,
		ResourceType: rr.ResourceType,
		ResourceName: rr.ResourceName,
		ResourceArn:  rr.ResourceArn,
		Username:     username,
		PrincipalArn: principalArn,
		SourceIP:     sourceIP,
		UserAgent:    userAgent,
		ReadOnly:     readOnly,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		EventJSON:    compact,
		IndexedAt:    indexedAt,
	}, true
}

func compactEventJSON(full map[string]any, maxBytes int) []byte {
	compact := map[string]any{
		"eventTime":         anyAt(full, "eventTime"),
		"eventSource":       anyAt(full, "eventSource"),
		"eventName":         anyAt(full, "eventName"),
		"awsRegion":         anyAt(full, "awsRegion"),
		"userIdentity":      anyAt(full, "userIdentity"),
		"sourceIPAddress":   anyAt(full, "sourceIPAddress"),
		"userAgent":         anyAt(full, "userAgent"),
		"readOnly":          anyAt(full, "readOnly"),
		"errorCode":         anyAt(full, "errorCode"),
		"errorMessage":      anyAt(full, "errorMessage"),
		"requestParameters": anyAt(full, "requestParameters"),
		"responseElements":  anyAt(full, "responseElements"),
		"resources":         anyAt(full, "resources"),
	}
	b, err := json.Marshal(compact)
	if err != nil {
		return []byte(`{}`)
	}
	if maxBytes <= 0 || len(b) <= maxBytes {
		return b
	}

	compact["requestParameters"] = "[omitted]"
	compact["responseElements"] = "[omitted]"
	b, err = json.Marshal(compact)
	if err != nil {
		return []byte(`{}`)
	}
	if len(b) <= maxBytes {
		return b
	}
	compact["resources"] = "[omitted]"
	b, err = json.Marshal(compact)
	if err != nil {
		return []byte(`{}`)
	}
	if len(b) <= maxBytes {
		return b
	}
	return []byte(`{"truncated":true}`)
}

func IsAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 401, 403:
			return true
		}
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.TrimSpace(apiErr.ErrorCode()) {
		case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "UnrecognizedClientException":
			return true
		}
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "access denied") || strings.Contains(msg, "accessdenied") || strings.Contains(msg, "unauthorized")
}
