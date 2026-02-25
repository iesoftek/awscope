package core

import (
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestCollectInstanceTargetGroupsByRegion(t *testing.T) {
	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{
			Key:         graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "elbv2:target-group", "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-instance"),
			Service:     "elbv2",
			Type:        "elbv2:target-group",
			PrimaryID:   "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-instance",
			Attributes:  map[string]any{"targetType": "instance"},
			CollectedAt: now,
		},
		{
			Key:         graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "elbv2:target-group", "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-ip"),
			Service:     "elbv2",
			Type:        "elbv2:target-group",
			PrimaryID:   "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-ip",
			Attributes:  map[string]any{"targetType": "ip"},
			CollectedAt: now,
		},
		{
			Key:         graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "elbv2:target-group", "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/tg-unknown"),
			Service:     "elbv2",
			Type:        "elbv2:target-group",
			PrimaryID:   "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/tg-unknown",
			Attributes:  map[string]any{},
			CollectedAt: now,
		},
	}

	got := collectInstanceTargetGroupsByRegion(nodes)
	if len(got) != 2 {
		t.Fatalf("regions: got %d want 2 (%#v)", len(got), got)
	}
	if len(got["us-east-1"]) != 1 || got["us-east-1"][0] != "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-instance" {
		t.Fatalf("us-east-1 groups: %#v", got["us-east-1"])
	}
	if len(got["us-west-2"]) != 1 || got["us-west-2"][0] != "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/tg-unknown" {
		t.Fatalf("us-west-2 groups: %#v", got["us-west-2"])
	}
}
