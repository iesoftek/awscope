package sns

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
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSNS func(cfg awsSDK.Config) snsAPI
}

func New() *Provider {
	return &Provider{newSNS: func(cfg awsSDK.Config) snsAPI { return sdksns.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "sns" }
func (p *Provider) DisplayName() string { return "SNS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type snsAPI interface {
	ListTopics(ctx context.Context, params *sdksns.ListTopicsInput, optFns ...func(*sdksns.Options)) (*sdksns.ListTopicsOutput, error)
	ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("sns provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("sns provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newSNS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api snsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var token *string
	for {
		resp, err := api.ListTopics(ctx, &sdksns.ListTopicsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		for _, t := range resp.Topics {
			arn := awsToString(t.TopicArn)
			if arn == "" {
				continue
			}
			name := topicNameFromArn(arn)
			key := graph.EncodeResourceKey(partition, accountID, region, "sns:topic", arn)
			raw, _ := json.Marshal(t)
			topicNode := graph.ResourceNode{
				Key:         key,
				DisplayName: name,
				Service:     "sns",
				Type:        "sns:topic",
				Arn:         arn,
				PrimaryID:   arn,
				Tags:        map[string]string{},
				Attributes:  map[string]any{},
				Raw:         raw,
				CollectedAt: now,
				Source:      "sns",
			}
			nodes = append(nodes, topicNode)

			// Subscriptions per topic.
			var subTok *string
			for {
				sresp, err := api.ListSubscriptionsByTopic(ctx, &sdksns.ListSubscriptionsByTopicInput{
					TopicArn:  &arn,
					NextToken: subTok,
				})
				if err != nil {
					return nil, nil, err
				}
				for _, s := range sresp.Subscriptions {
					subArn := awsToString(s.SubscriptionArn)
					if subArn == "" {
						continue
					}
					subKey := graph.EncodeResourceKey(partition, accountID, region, "sns:subscription", subArn)
					desc := fmt.Sprintf("%s:%s", awsToString(s.Protocol), shortEndpoint(awsToString(s.Endpoint)))
					raw, _ := json.Marshal(s)
					subNode := graph.ResourceNode{
						Key:         subKey,
						DisplayName: desc,
						Service:     "sns",
						Type:        "sns:subscription",
						Arn:         subArn,
						PrimaryID:   subArn,
						Tags:        map[string]string{},
						Attributes: map[string]any{
							"protocol": awsToString(s.Protocol),
							"endpoint": awsToString(s.Endpoint),
						},
						Raw:         raw,
						CollectedAt: now,
						Source:      "sns",
					}
					nodes = append(nodes, subNode)
					edges = append(edges, graph.RelationshipEdge{
						From:        subKey,
						To:          key,
						Kind:        "member-of",
						Meta:        map[string]any{"direct": true},
						CollectedAt: now,
					})

					if ep := awsToString(s.Endpoint); strings.HasPrefix(ep, "arn:") {
						toKey, ok := endpointArnToKey(partition, accountID, region, ep)
						if ok {
							edges = append(edges, graph.RelationshipEdge{
								From:        subKey,
								To:          toKey,
								Kind:        "targets",
								Meta:        map[string]any{"direct": true},
								CollectedAt: now,
							})
						}
					}
				}
				if sresp.NextToken == nil || *sresp.NextToken == "" {
					break
				}
				subTok = sresp.NextToken
			}
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}

	return nodes, edges, nil
}

func topicNameFromArn(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func shortEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 48 {
		return s
	}
	return s[:45] + "..."
}

func endpointArnToKey(partition, accountID, fallbackRegion, arn string) (graph.ResourceKey, bool) {
	// arn:partition:service:region:account:resource
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return "", false
	}
	svc := parts[2]
	r := parts[3]
	if r == "" {
		r = fallbackRegion
	}
	switch svc {
	case "sqs":
		return graph.EncodeResourceKey(partition, accountID, r, "sqs:queue", arn), true
	case "lambda":
		return graph.EncodeResourceKey(partition, accountID, r, "lambda:function", arn), true
	default:
		return "", false
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
