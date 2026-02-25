package diagram

import (
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/store"
)

func TestCondenseDefault_FiltersNoisyTypesAndKinds(t *testing.T) {
	now := time.Now().UTC()
	rs := []store.ResourceSummary{
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "global", "iam:role", "r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", Region: "global", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "global", "iam:policy", "p1"), DisplayName: "p1", Service: "iam", Type: "iam:policy", Region: "global", CollectedAt: now, UpdatedAt: now},
	}
	edges := []graph.RelationshipEdge{
		{From: rs[0].Key, To: rs[1].Key, Kind: "uses", CollectedAt: now},
		{From: rs[1].Key, To: rs[2].Key, Kind: "attached-policy", CollectedAt: now},
	}

	m := BuildModel(Scope{AccountID: "1", Region: "us-east-1", IncludeGlobalLinked: true, Full: false}, rs, edges)
	c := CondenseDefault(m, CondenseOptions{MaxNodes: 100, MaxEdges: 100})

	if len(c.Nodes) != 2 {
		t.Fatalf("nodes len: got %d want 2", len(c.Nodes))
	}
	for _, n := range c.Nodes {
		if n.Type == "iam:policy" {
			t.Fatalf("unexpected iam:policy in condensed nodes")
		}
	}
	if len(c.Edges) != 1 || c.Edges[0].Kind != "uses" {
		t.Fatalf("condensed edges mismatch: %#v", c.Edges)
	}
}

func TestCondenseDefault_RespectsCaps(t *testing.T) {
	now := time.Now().UTC()
	rs := []store.ResourceSummary{
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "ec2:instance", "i-2"), DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "global", "iam:role", "r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", Region: "global", CollectedAt: now, UpdatedAt: now},
	}
	edges := []graph.RelationshipEdge{
		{From: rs[0].Key, To: rs[2].Key, Kind: "uses", CollectedAt: now},
		{From: rs[1].Key, To: rs[2].Key, Kind: "uses", CollectedAt: now},
	}
	m := BuildModel(Scope{AccountID: "1", Region: "us-east-1", IncludeGlobalLinked: true, Full: false}, rs, edges)
	c := CondenseDefault(m, CondenseOptions{MaxNodes: 2, MaxEdges: 1})
	if len(c.Nodes) != 2 {
		t.Fatalf("nodes len: got %d want 2", len(c.Nodes))
	}
	if c.OmittedNodes <= 0 {
		t.Fatalf("expected omitted node counter, got nodes=%d edges=%d", c.OmittedNodes, c.OmittedEdges)
	}
}
