package securityhub

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
	sdksecurityhub "github.com/aws/aws-sdk-go-v2/service/securityhub"
	"github.com/aws/aws-sdk-go-v2/service/securityhub/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSecurityHub func(cfg awsSDK.Config) securityHubAPI
}

func New() *Provider {
	return &Provider{newSecurityHub: func(cfg awsSDK.Config) securityHubAPI { return sdksecurityhub.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "securityhub" }
func (p *Provider) DisplayName() string { return "Security Hub" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type securityHubAPI interface {
	DescribeHub(ctx context.Context, params *sdksecurityhub.DescribeHubInput, optFns ...func(*sdksecurityhub.Options)) (*sdksecurityhub.DescribeHubOutput, error)
	GetEnabledStandards(ctx context.Context, params *sdksecurityhub.GetEnabledStandardsInput, optFns ...func(*sdksecurityhub.Options)) (*sdksecurityhub.GetEnabledStandardsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("securityhub provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("securityhub provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newSecurityHub(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api securityHubAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	hub, err := api.DescribeHub(ctx, &sdksecurityhub.DescribeHubInput{})
	if err != nil {
		if isAPIErrorCode(err, "InvalidAccessException") || isAPIErrorCode(err, "ResourceNotFoundException") || isAPIErrorCode(err, "AccessDeniedException") {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	hubArn := strings.TrimSpace(awsToString(hub.HubArn))
	hubKey := graph.EncodeResourceKey(partition, accountID, region, "securityhub:hub", firstNonEmpty(hubArn, accountID+":"+region))
	attrs := map[string]any{
		"status":                  "enabled",
		"controlFindingGenerator": strings.TrimSpace(string(hub.ControlFindingGenerator)),
		"subscribed_at":           strings.TrimSpace(awsToString(hub.SubscribedAt)),
	}
	if hub.AutoEnableControls != nil {
		attrs["autoEnableControls"] = *hub.AutoEnableControls
	}
	rawHub, _ := json.Marshal(hub)
	nodes := []graph.ResourceNode{{
		Key:         hubKey,
		DisplayName: firstNonEmpty(shortArn(hubArn), "securityhub"),
		Service:     "securityhub",
		Type:        "securityhub:hub",
		Arn:         hubArn,
		PrimaryID:   firstNonEmpty(hubArn, accountID+":"+region),
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         rawHub,
		CollectedAt: now,
		Source:      "securityhub",
	}}

	var edges []graph.RelationshipEdge
	var nextToken *string
	for {
		out, err := api.GetEnabledStandards(ctx, &sdksecurityhub.GetEnabledStandardsInput{NextToken: nextToken})
		if err != nil {
			if isAPIErrorCode(err, "InvalidAccessException") || isAPIErrorCode(err, "ResourceNotFoundException") || isAPIErrorCode(err, "AccessDeniedException") {
				break
			}
			return nil, nil, err
		}
		for _, sub := range out.StandardsSubscriptions {
			subArn := strings.TrimSpace(awsToString(sub.StandardsSubscriptionArn))
			if subArn == "" {
				continue
			}
			key := graph.EncodeResourceKey(partition, accountID, region, "securityhub:standard-subscription", subArn)
			subAttrs := map[string]any{
				"status":             strings.TrimSpace(string(sub.StandardsStatus)),
				"standardsArn":       strings.TrimSpace(awsToString(sub.StandardsArn)),
				"controlsUpdatable":  strings.TrimSpace(string(sub.StandardsControlsUpdatable)),
				"statusReasonStatus": "",
			}
			if sub.StandardsStatusReason != nil {
				subAttrs["statusReasonStatus"] = strings.TrimSpace(string(sub.StandardsStatusReason.StatusReasonCode))
			}
			raw, _ := json.Marshal(sub)
			nodes = append(nodes, graph.ResourceNode{
				Key:         key,
				DisplayName: firstNonEmpty(shortArn(strings.TrimSpace(awsToString(sub.StandardsArn))), shortArn(subArn)),
				Service:     "securityhub",
				Type:        "securityhub:standard-subscription",
				Arn:         subArn,
				PrimaryID:   subArn,
				Tags:        map[string]string{},
				Attributes:  subAttrs,
				Raw:         raw,
				CollectedAt: now,
				Source:      "securityhub",
			})
			edges = append(edges, graph.RelationshipEdge{From: hubKey, To: key, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, edges, nil
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
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

var _ = types.StandardsStatusReady
