package core

import (
	"context"
	"strings"
	"sync"
	"time"

	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdklb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"golang.org/x/sync/errgroup"
)

type elbv2API interface {
	DescribeTargetHealth(ctx context.Context, params *sdklb.DescribeTargetHealthInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeTargetHealthOutput, error)
}

func resolveInstanceTargetGroups(ctx context.Context, cfg awsSDK.Config, partition, accountID string, tgs []tgRef, maxConcurrency int) ([]graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	if maxConcurrency <= 0 {
		maxConcurrency = 20
	}

	refs := make([]tgRef, 0, len(tgs))
	seenRef := make(map[string]struct{}, len(tgs))
	for _, tg := range tgs {
		if tg.region == "" || tg.arn == "" {
			continue
		}
		key := tg.region + "\x00" + tg.arn
		if _, ok := seenRef[key]; ok {
			continue
		}
		seenRef[key] = struct{}{}
		refs = append(refs, tg)
	}
	if len(refs) == 0 {
		return nil, nil
	}

	nw := maxConcurrency
	if nw > len(refs) {
		nw = len(refs)
	}
	if nw < 1 {
		nw = 1
	}

	jobs := make(chan tgRef, nw*2)
	for _, tg := range refs {
		jobs <- tg
	}
	close(jobs)

	var (
		out []graph.RelationshipEdge
		mu  sync.Mutex
	)

	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < nw; i++ {
		g.Go(func() error {
			for tg := range jobs {
				c := cfg
				c.Region = tg.region
				api := sdklb.NewFromConfig(c)

				resp, err := api.DescribeTargetHealth(gctx, &sdklb.DescribeTargetHealthInput{
					TargetGroupArn: &tg.arn,
				})
				if err != nil {
					return err
				}

				tgKey := graph.EncodeResourceKey(partition, accountID, tg.region, "elbv2:target-group", tg.arn)
				local := make([]graph.RelationshipEdge, 0, len(resp.TargetHealthDescriptions))
				for _, d := range resp.TargetHealthDescriptions {
					id := awsToString(d.Target.Id)
					if id == "" {
						continue
					}
					if !strings.HasPrefix(id, "i-") {
						continue
					}
					instKey := graph.EncodeResourceKey(partition, accountID, tg.region, "ec2:instance", id)
					local = append(local, graph.RelationshipEdge{
						From:        instKey,
						To:          tgKey,
						Kind:        "targets",
						Meta:        map[string]any{"resolver": "elbv2", "direct": false, "targetType": "instance"},
						CollectedAt: now,
					})
				}

				if len(local) == 0 {
					continue
				}
				mu.Lock()
				out = append(out, local...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	if len(out) == 0 {
		return out, nil
	}
	uniq := out[:0]
	seen := make(map[string]struct{}, len(out))
	for _, e := range out {
		k := string(e.From) + "\x00" + string(e.To) + "\x00" + e.Kind
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		uniq = append(uniq, e)
	}
	return uniq, nil
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
