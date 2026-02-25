package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSQS func(cfg awsSDK.Config) sqsAPI
}

func New() *Provider {
	return &Provider{newSQS: func(cfg awsSDK.Config) sqsAPI { return sdksqs.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "sqs" }
func (p *Provider) DisplayName() string { return "SQS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type sqsAPI interface {
	ListQueues(ctx context.Context, params *sdksqs.ListQueuesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueuesOutput, error)
	GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("sqs provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("sqs provider requires account identity")
	}
	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newSQS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api sqsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	resp, err := api.ListQueues(ctx, &sdksqs.ListQueuesInput{})
	if err != nil {
		return nil, nil, err
	}

	type queueResult struct {
		node  *graph.ResourceNode
		edges []graph.RelationshipEdge
		err   error
	}
	urls := make([]string, 0, len(resp.QueueUrls))
	for _, url := range resp.QueueUrls {
		url = strings.TrimSpace(url)
		if url != "" {
			urls = append(urls, url)
		}
	}
	if len(urls) == 0 {
		return nil, nil, nil
	}

	results := make([]queueResult, len(urls))
	jobs := make(chan int, len(urls))
	for i := range urls {
		jobs <- i
	}
	close(jobs)

	workers := envIntOr("AWSCOPE_SQS_QUEUE_CONCURRENCY", 24)
	if workers > len(urls) {
		workers = len(urls)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			url := urls[idx]
			attrsOut, err := api.GetQueueAttributes(ctx, &sdksqs.GetQueueAttributesInput{
				QueueUrl: &url,
				AttributeNames: []sqstypes.QueueAttributeName{
					sqstypes.QueueAttributeNameQueueArn,
					sqstypes.QueueAttributeNameCreatedTimestamp,
					sqstypes.QueueAttributeNameKmsMasterKeyId,
					sqstypes.QueueAttributeNameSqsManagedSseEnabled,
				},
			})
			if err != nil {
				results[idx] = queueResult{err: err}
				continue
			}
			arn := strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameQueueArn)])
			if arn == "" {
				continue
			}
			name := queueNameFromArn(arn)
			if name == "" {
				name = arn
			}
			key := graph.EncodeResourceKey(partition, accountID, region, "sqs:queue", arn)
			raw, _ := json.Marshal(attrsOut.Attributes)
			node := graph.ResourceNode{
				Key:         key,
				DisplayName: name,
				Service:     "sqs",
				Type:        "sqs:queue",
				Arn:         arn,
				PrimaryID:   arn,
				Tags:        map[string]string{},
				Attributes:  map[string]any{"url": url},
				Raw:         raw,
				CollectedAt: now,
				Source:      "sqs",
			}
			if ts := strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameCreatedTimestamp)]); ts != "" {
				if sec, err := strconv.ParseInt(ts, 10, 64); err == nil && sec > 0 {
					node.Attributes["created_at"] = time.Unix(sec, 0).UTC().Format("2006-01-02 15:04")
				}
			}
			var edges []graph.RelationshipEdge
			if kms := strings.TrimSpace(attrsOut.Attributes[string(sqstypes.QueueAttributeNameKmsMasterKeyId)]); kms != "" {
				toKey, ok := kmsRefToKey(partition, accountID, region, kms)
				if ok {
					edges = append(edges, graph.RelationshipEdge{
						From:        key,
						To:          toKey,
						Kind:        "uses",
						Meta:        map[string]any{"direct": true, "source": "sqs.kms"},
						CollectedAt: now,
					})
				}
				node.Attributes["kms_master_key_id"] = kms
			}
			results[idx] = queueResult{node: &node, edges: edges}
		}
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	wg.Wait()

	nodes := make([]graph.ResourceNode, 0, len(results))
	edges := make([]graph.RelationshipEdge, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		if r.node == nil {
			continue
		}
		nodes = append(nodes, *r.node)
		edges = append(edges, r.edges...)
	}
	return nodes, edges, nil
}

func queueNameFromArn(arn string) string {
	// arn:aws:sqs:region:acct:queueName
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func kmsRefToKey(partition, accountID, fallbackRegion, ref string) (graph.ResourceKey, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:alias", ref), true
	}
	if strings.HasPrefix(ref, "arn:") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:key", ref), true
	}
	if strings.HasPrefix(ref, "alias/") {
		arn := fmt.Sprintf("arn:%s:kms:%s:%s:%s", partition, fallbackRegion, accountID, ref)
		return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:alias", arn), true
	}
	return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:key", ref), true
}

func arnRegion(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

func envIntOr(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
