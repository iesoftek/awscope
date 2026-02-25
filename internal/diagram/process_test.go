package diagram

import (
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/store"
)

func TestParseView(t *testing.T) {
	if _, err := ParseView("overview"); err != nil {
		t.Fatalf("ParseView overview: %v", err)
	}
	if _, err := ParseView("full"); err != nil {
		t.Fatalf("ParseView full: %v", err)
	}
	if _, err := ParseView("unknown"); err == nil {
		t.Fatalf("expected error for unknown view")
	}
}

func TestProcessModel_OverviewSummarizesIsolated(t *testing.T) {
	now := time.Now().UTC()
	rs := []store.ResourceSummary{
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "global", "iam:role", "r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", Region: "global", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "logs:log-group", "/aws/lambda/x"), DisplayName: "/aws/lambda/x", Service: "logs", Type: "logs:log-group", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
	}
	edges := []graph.RelationshipEdge{
		{From: rs[0].Key, To: rs[1].Key, Kind: "uses", CollectedAt: now},
	}
	m := BuildModel(Scope{AccountID: "1", Region: "us-east-1"}, rs, edges)
	p := DefaultProcessOptions(ViewOverview)
	p.MaxNodes = 0
	p.MaxEdges = 0
	out := ProcessModel(m, p)

	hasSummary := false
	hasRawLog := false
	for _, n := range out.Nodes {
		if n.Key == "summary:isolated:logs:logs:log-group" {
			hasSummary = true
		}
		if n.Type == "logs:log-group" && n.Name == "/aws/lambda/x" {
			hasRawLog = true
		}
	}
	if !hasSummary {
		t.Fatalf("expected isolated summary node for logs")
	}
	if hasRawLog {
		t.Fatalf("expected raw isolated log-group to be summarized")
	}
}

func TestProcessModel_NetworkViewDropsEventingOnlyTypes(t *testing.T) {
	now := time.Now().UTC()
	rs := []store.ResourceSummary{
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
		{Key: graph.EncodeResourceKey("aws", "1", "us-east-1", "sns:topic", "t1"), DisplayName: "t1", Service: "sns", Type: "sns:topic", Region: "us-east-1", CollectedAt: now, UpdatedAt: now},
	}
	m := BuildModel(Scope{AccountID: "1", Region: "us-east-1"}, rs, nil)
	p := DefaultProcessOptions(ViewNetwork)
	p.IncludeIsolated = IncludeIsolatedFull
	out := ProcessModel(m, p)
	if len(out.Nodes) != 1 {
		t.Fatalf("nodes len: got %d want 1", len(out.Nodes))
	}
	if out.Nodes[0].Type != "ec2:instance" {
		t.Fatalf("unexpected type %s", out.Nodes[0].Type)
	}
}
