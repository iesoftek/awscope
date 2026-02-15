package logs

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
	sdklogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newLogs func(cfg awsSDK.Config) logsAPI
}

func New() *Provider {
	return &Provider{newLogs: func(cfg awsSDK.Config) logsAPI { return sdklogs.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "logs" }
func (p *Provider) DisplayName() string { return "CloudWatch Logs" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type logsAPI interface {
	DescribeLogGroups(ctx context.Context, params *sdklogs.DescribeLogGroupsInput, optFns ...func(*sdklogs.Options)) (*sdklogs.DescribeLogGroupsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("logs provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("logs provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newLogs(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api logsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var token *string
	for {
		out, err := api.DescribeLogGroups(ctx, &sdklogs.DescribeLogGroupsInput{
			NextToken: token,
			Limit:     awsSDK.Int32(50),
		})
		if err != nil {
			return nil, nil, err
		}
		for _, lg := range out.LogGroups {
			n, stubs, es := normalizeLogGroup(partition, accountID, region, lg, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		token = out.NextToken
	}

	return nodes, edges, nil
}

func normalizeLogGroup(partition, accountID, region string, lg types.LogGroup, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(lg.LogGroupName)
	arn := awsToString(lg.Arn)
	display := name
	if display == "" {
		display = arn
	}
	primary := arn
	if primary == "" {
		primary = name
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "logs:log-group", primary)

	attrs := map[string]any{
		"retentionDays": int32(0),
		"class":         strings.TrimSpace(string(lg.LogGroupClass)),
	}
	if lg.RetentionInDays != nil {
		attrs["retentionDays"] = *lg.RetentionInDays
	}
	if lg.StoredBytes != nil {
		attrs["storedBytes"] = *lg.StoredBytes
		attrs["storedGiB"] = float64(*lg.StoredBytes) / float64(1024*1024*1024)
	}
	if lg.CreationTime != nil && *lg.CreationTime > 0 {
		// ms since epoch
		t := time.UnixMilli(*lg.CreationTime).UTC()
		attrs["created_at"] = t.Format("2006-01-02 15:04")
	}
	if kms := strings.TrimSpace(awsToString(lg.KmsKeyId)); kms != "" {
		attrs["kmsKeyId"] = kms
	}

	raw, _ := json.Marshal(lg)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "logs",
		Type:        "logs:log-group",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "logs",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if kms := strings.TrimSpace(awsToString(lg.KmsKeyId)); kms != "" {
		toKey, ok := kmsRefToKey(partition, accountID, region, kms)
		if ok {
			stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "logs"))
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "uses",
				Meta:        map[string]any{"direct": true, "source": "logs.kms"},
				CollectedAt: now,
			})
		}
	}

	return node, stubs, edges
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		Arn:         "",
		PrimaryID:   display,
		Tags:        map[string]string{},
		Attributes:  map[string]any{"stub": true},
		Raw:         []byte("{}"),
		CollectedAt: now,
		Source:      source,
	}
}

func kmsRefToKey(partition, accountID, fallbackRegion, ref string) (graph.ResourceKey, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	// ARN form: arn:aws:kms:region:acct:key/<id>
	if strings.HasPrefix(ref, "arn:") {
		parts := strings.Split(ref, ":")
		if len(parts) >= 6 {
			region := parts[3]
			if strings.TrimSpace(region) == "" {
				region = fallbackRegion
			}
			return graph.EncodeResourceKey(partition, accountID, region, "kms:key", ref), true
		}
	}
	// Key ID form.
	return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:key", ref), true
}

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return arn
	}
	res := parts[5]
	if i := strings.LastIndex(res, "/"); i >= 0 && i+1 < len(res) {
		return res[i+1:]
	}
	return res
}

func awsToString(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}
