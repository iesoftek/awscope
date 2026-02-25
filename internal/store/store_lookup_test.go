package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestStore_ListResourceLookupsByAccountAndRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", Arn: "arn:aws:ec2:us-east-1:111111111111:instance/i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "global", "iam:role", "arn:aws:iam::111111111111:role/r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", PrimaryID: "arn:aws:iam::111111111111:role/r1", Arn: "arn:aws:iam::111111111111:role/r1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "222222222222", "us-east-1", "ec2:instance", "i-2"), DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", Arn: "arn:aws:ec2:us-east-1:222222222222:instance/i-2", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	lookups, err := st.ListResourceLookupsByAccountAndRegions(ctx, "111111111111", []string{"us-east-1", "global"})
	if err != nil {
		t.Fatalf("ListResourceLookupsByAccountAndRegions: %v", err)
	}
	if len(lookups) != 2 {
		t.Fatalf("len: got %d want 2", len(lookups))
	}
	got := map[string]bool{}
	for _, r := range lookups {
		got[string(r.Key)] = true
	}
	if !got[string(nodes[0].Key)] || !got[string(nodes[1].Key)] {
		t.Fatalf("unexpected lookup set: %#v", got)
	}
}
