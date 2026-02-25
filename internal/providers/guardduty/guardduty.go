package guardduty

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkguardduty "github.com/aws/aws-sdk-go-v2/service/guardduty"
	"github.com/aws/aws-sdk-go-v2/service/guardduty/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newGuardDuty func(cfg awsSDK.Config) guardDutyAPI
}

func New() *Provider {
	return &Provider{newGuardDuty: func(cfg awsSDK.Config) guardDutyAPI { return sdkguardduty.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "guardduty" }
func (p *Provider) DisplayName() string { return "GuardDuty" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type guardDutyAPI interface {
	ListDetectors(ctx context.Context, params *sdkguardduty.ListDetectorsInput, optFns ...func(*sdkguardduty.Options)) (*sdkguardduty.ListDetectorsOutput, error)
	GetDetector(ctx context.Context, params *sdkguardduty.GetDetectorInput, optFns ...func(*sdkguardduty.Options)) (*sdkguardduty.GetDetectorOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("guardduty provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("guardduty provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newGuardDuty(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api guardDutyAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var nextToken *string
	for {
		out, err := api.ListDetectors(ctx, &sdkguardduty.ListDetectorsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, detectorID := range out.DetectorIds {
			detectorID = strings.TrimSpace(detectorID)
			if detectorID == "" {
				continue
			}
			g, err := api.GetDetector(ctx, &sdkguardduty.GetDetectorInput{DetectorId: awsSDK.String(detectorID)})
			if err != nil {
				return nil, nil, err
			}
			n, stubs, es := normalizeDetector(partition, accountID, region, detectorID, g, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}

		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, edges, nil
}

func normalizeDetector(partition, accountID, region, detectorID string, d *sdkguardduty.GetDetectorOutput, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	primary := detectorID
	key := graph.EncodeResourceKey(partition, accountID, region, "guardduty:detector", primary)
	arn := fmt.Sprintf("arn:%s:guardduty:%s:%s:detector/%s", partition, region, accountID, detectorID)

	status := "unknown"
	if d != nil {
		status = strings.ToLower(strings.TrimSpace(string(d.Status)))
	}
	attrs := map[string]any{"status": status}
	if d != nil {
		attrs["findingPublishingFrequency"] = strings.TrimSpace(string(d.FindingPublishingFrequency))
		attrs["serviceRole"] = strings.TrimSpace(awsToString(d.ServiceRole))
		attrs["created_at"] = strings.TrimSpace(awsToString(d.CreatedAt))
		attrs["updated_at"] = strings.TrimSpace(awsToString(d.UpdatedAt))
		attrs["featuresCount"] = len(d.Features)
	}

	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: detectorID,
		Service:     "guardduty",
		Type:        "guardduty:detector",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "guardduty",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if d != nil {
		if roleArn := strings.TrimSpace(awsToString(d.ServiceRole)); roleArn != "" {
			toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
			stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "guardduty"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "guardduty.role"}, CollectedAt: now})
		}
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

var _ = types.DetectorStatusEnabled
