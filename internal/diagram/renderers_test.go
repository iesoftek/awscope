package diagram

import (
	"strings"
	"testing"
)

func TestGraphvizRenderer_Render(t *testing.T) {
	m := Model{
		Scope: Scope{AccountID: "1", Region: "us-east-1", IncludeGlobalLinked: true, Full: false},
		Nodes: []Node{
			{Key: "k1", Name: "inst", Type: "ec2:instance", Service: "ec2", Region: "us-east-1", Status: "running"},
			{Key: "k2", Name: "role", Type: "iam:role", Service: "iam", Region: "global"},
		},
		Edges: []Edge{
			{From: "k1", To: "k2", Kind: "uses"},
		},
	}
	b, err := (GraphvizRenderer{}).Render(m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "digraph awscope") {
		t.Fatalf("expected digraph header")
	}
	if !strings.Contains(out, "cluster_global") {
		t.Fatalf("expected global cluster")
	}
	if !strings.Contains(out, "->") {
		t.Fatalf("expected edge output")
	}
}

func TestMermaidRenderer_Render(t *testing.T) {
	m := Model{
		Scope: Scope{AccountID: "1", Region: "us-east-1", IncludeGlobalLinked: true, Full: false},
		Nodes: []Node{
			{Key: "k1", Name: "inst", Type: "ec2:instance", Service: "ec2", Region: "us-east-1", Status: "running"},
			{Key: "k2", Name: "role", Type: "iam:role", Service: "iam", Region: "global"},
		},
		Edges: []Edge{
			{From: "k1", To: "k2", Kind: "uses"},
		},
	}
	b, err := (MermaidRenderer{}).Render(m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "flowchart LR") {
		t.Fatalf("expected flowchart header")
	}
	if !strings.Contains(out, "subgraph") {
		t.Fatalf("expected subgraph output")
	}
	if !strings.Contains(out, "-->") {
		t.Fatalf("expected edge output")
	}
}
