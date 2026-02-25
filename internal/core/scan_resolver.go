package core

import (
	"context"
	"strings"
	"time"

	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdklb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

type elbv2API interface {
	DescribeTargetHealth(ctx context.Context, params *sdklb.DescribeTargetHealthInput, optFns ...func(*sdklb.Options)) (*sdklb.DescribeTargetHealthOutput, error)
}

func resolveInstanceTargetGroups(ctx context.Context, cfg awsSDK.Config, partition, accountID string, tgs []tgRef) ([]graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var out []graph.RelationshipEdge

	// v0: sequential calls; we'll add concurrency+rate limiting once behavior is proven.
	for _, tg := range tgs {
		if tg.region == "" || tg.arn == "" {
			continue
		}
		c := cfg
		c.Region = tg.region
		api := sdklb.NewFromConfig(c)

		resp, err := api.DescribeTargetHealth(ctx, &sdklb.DescribeTargetHealthInput{
			TargetGroupArn: &tg.arn,
		})
		if err != nil {
			return nil, err
		}

		tgKey := graph.EncodeResourceKey(partition, accountID, tg.region, "elbv2:target-group", tg.arn)
		for _, d := range resp.TargetHealthDescriptions {
			id := awsToString(d.Target.Id)
			if id == "" {
				continue
			}
			// Handle instance targets (i-*) only for now.
			if !strings.HasPrefix(id, "i-") {
				continue
			}
			instKey := graph.EncodeResourceKey(partition, accountID, tg.region, "ec2:instance", id)
			out = append(out, graph.RelationshipEdge{
				From:        instKey,
				To:          tgKey,
				Kind:        "targets",
				Meta:        map[string]any{"resolver": "elbv2", "direct": false, "targetType": "instance"},
				CollectedAt: now,
			})
		}
	}
	return out, nil
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
