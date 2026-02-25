package cloudtrail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newCloudTrail func(cfg awsSDK.Config) cloudTrailAPI
}

func New() *Provider {
	return &Provider{newCloudTrail: func(cfg awsSDK.Config) cloudTrailAPI { return sdkcloudtrail.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "cloudtrail" }
func (p *Provider) DisplayName() string { return "CloudTrail" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type cloudTrailAPI interface {
	DescribeTrails(ctx context.Context, params *sdkcloudtrail.DescribeTrailsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.DescribeTrailsOutput, error)
	GetTrailStatus(ctx context.Context, params *sdkcloudtrail.GetTrailStatusInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.GetTrailStatusOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("cloudtrail provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("cloudtrail provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newCloudTrail(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api cloudTrailAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	out, err := api.DescribeTrails(ctx, &sdkcloudtrail.DescribeTrailsInput{IncludeShadowTrails: awsSDK.Bool(false)})
	if err != nil {
		return nil, nil, err
	}

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	for _, t := range out.TrailList {
		n, stubs, es := normalizeTrail(partition, accountID, region, t, nil, now)
		arn := strings.TrimSpace(awsToString(t.TrailARN))
		if arn != "" {
			if status, err := api.GetTrailStatus(ctx, &sdkcloudtrail.GetTrailStatusInput{Name: awsSDK.String(arn)}); err == nil {
				n, stubs, es = normalizeTrail(partition, accountID, region, t, status, now)
			} else if !isAPIErrorCode(err, "TrailNotFoundException") && !isAPIErrorCode(err, "AccessDenied") && !isAPIErrorCode(err, "AccessDeniedException") {
				return nil, nil, err
			}
		}
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}
	return nodes, edges, nil
}

func normalizeTrail(partition, accountID, fallbackRegion string, t types.Trail, status *sdkcloudtrail.GetTrailStatusOutput, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	trailARN := strings.TrimSpace(awsToString(t.TrailARN))
	homeRegion := strings.TrimSpace(awsToString(t.HomeRegion))
	if homeRegion == "" {
		homeRegion = fallbackRegion
	}
	name := strings.TrimSpace(awsToString(t.Name))
	primary := firstNonEmpty(trailARN, name)
	if primary == "" {
		primary = fmt.Sprintf("trail-%d", now.UnixNano())
	}
	key := graph.EncodeResourceKey(partition, accountID, homeRegion, "cloudtrail:trail", primary)

	statusText := "unknown"
	if status != nil && status.IsLogging != nil {
		if *status.IsLogging {
			statusText = "logging"
		} else {
			statusText = "stopped"
		}
	}
	attrs := map[string]any{
		"status":                     statusText,
		"homeRegion":                 strings.TrimSpace(awsToString(t.HomeRegion)),
		"isMultiRegionTrail":         awsToBool(t.IsMultiRegionTrail),
		"isOrganizationTrail":        awsToBool(t.IsOrganizationTrail),
		"includeGlobalServiceEvents": awsToBool(t.IncludeGlobalServiceEvents),
		"logFileValidationEnabled":   awsToBool(t.LogFileValidationEnabled),
		"s3Bucket":                   strings.TrimSpace(awsToString(t.S3BucketName)),
		"s3KeyPrefix":                strings.TrimSpace(awsToString(t.S3KeyPrefix)),
	}
	if status != nil {
		if status.LatestDeliveryTime != nil {
			attrs["latestDeliveryTime"] = status.LatestDeliveryTime.UTC().Format("2006-01-02 15:04")
		}
		if status.StartLoggingTime != nil {
			attrs["startLoggingTime"] = status.StartLoggingTime.UTC().Format("2006-01-02 15:04")
		}
	}

	raw, _ := json.Marshal(map[string]any{"trail": t, "status": status})
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, trailARN),
		Service:     "cloudtrail",
		Type:        "cloudtrail:trail",
		Arn:         trailARN,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "cloudtrail",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	if b := strings.TrimSpace(awsToString(t.S3BucketName)); b != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, homeRegion, "s3:bucket", b)
		stubs = append(stubs, stubNode(toKey, "s3", "s3:bucket", b, now, "cloudtrail"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudtrail.s3"}, CollectedAt: now})
	}
	if snsArn := strings.TrimSpace(awsToString(t.SnsTopicARN)); snsArn != "" {
		region := arnRegion(snsArn, homeRegion)
		toKey := graph.EncodeResourceKey(partition, accountID, region, "sns:topic", snsArn)
		stubs = append(stubs, stubNode(toKey, "sns", "sns:topic", shortArn(snsArn), now, "cloudtrail"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudtrail.sns"}, CollectedAt: now})
	}
	if kmsArn := strings.TrimSpace(awsToString(t.KmsKeyId)); kmsArn != "" {
		region := arnRegion(kmsArn, homeRegion)
		toKey := graph.EncodeResourceKey(partition, accountID, region, "kms:key", kmsArn)
		stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kmsArn), now, "cloudtrail"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudtrail.kms"}, CollectedAt: now})
	}
	if lgArn := strings.TrimSpace(awsToString(t.CloudWatchLogsLogGroupArn)); lgArn != "" {
		region := arnRegion(lgArn, homeRegion)
		toKey := graph.EncodeResourceKey(partition, accountID, region, "logs:log-group", lgArn)
		stubs = append(stubs, stubNode(toKey, "logs", "logs:log-group", shortArn(lgArn), now, "cloudtrail"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudtrail.logs"}, CollectedAt: now})
	}
	if roleArn := strings.TrimSpace(awsToString(t.CloudWatchLogsRoleArn)); roleArn != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "cloudtrail"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "cloudtrail.logs-role"}, CollectedAt: now})
	}

	return node, stubs, edges
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{"stub": true},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func awsToBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func isAPIErrorCode(err error, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ae.ErrorCode()), strings.TrimSpace(code))
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
}

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}
