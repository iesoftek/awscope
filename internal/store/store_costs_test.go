package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestStore_Costs_CRUDAndAgg(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	k1 := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1")
	k2 := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-2")
	k3 := graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "ec2:vpc", "vpc-1")

	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: k1, DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: k2, DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: now, Source: "test"},
		{Key: k3, DisplayName: "vpc-1", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-1", CollectedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	v := 10.0
	if err := st.UpsertResourceCosts(ctx, []ResourceCostRow{
		{ResourceKey: k1, AccountID: "123456789012", Partition: "aws", Region: "us-east-1", Service: "ec2", Type: "ec2:instance", EstMonthlyUSD: &v, Basis: "test", Breakdown: map[string]any{"x": 1}, ComputedAt: now.Add(-time.Hour), Source: "test"},
		{ResourceKey: k2, AccountID: "123456789012", Partition: "aws", Region: "us-east-1", Service: "ec2", Type: "ec2:instance", EstMonthlyUSD: nil, Basis: "unknown", Breakdown: map[string]any{}, ComputedAt: now.Add(-time.Hour), Source: "test"},
		// k3: missing row to ensure it counts as unknown in agg.
	}); err != nil {
		t.Fatalf("UpsertResourceCosts: %v", err)
	}

	got, ok, err := st.GetResourceCost(ctx, k1)
	if err != nil {
		t.Fatalf("GetResourceCost: %v", err)
	}
	if !ok || got.EstMonthlyUSD == nil || *got.EstMonthlyUSD != 10.0 {
		t.Fatalf("GetResourceCost: got=%#v ok=%v", got, ok)
	}

	agg, err := st.ListServiceCostAggByRegions(ctx, "123456789012", []string{"us-east-1", "us-west-2"})
	if err != nil {
		t.Fatalf("ListServiceCostAggByRegions: %v", err)
	}
	if len(agg) == 0 {
		t.Fatalf("agg empty")
	}
	var ec2 CostAgg
	for _, a := range agg {
		if a.Key == "ec2" {
			ec2 = a
		}
	}
	if ec2.Count != 3 {
		t.Fatalf("count: got %d want 3", ec2.Count)
	}
	if ec2.KnownUSD != 10.0 {
		t.Fatalf("known: got %v want 10", ec2.KnownUSD)
	}
	if ec2.UnknownCount != 2 {
		t.Fatalf("unknown: got %d want 2", ec2.UnknownCount)
	}

	typeAgg, err := st.ListTypeCostAggByServiceAndRegions(ctx, "123456789012", "ec2", []string{"us-east-1", "us-west-2"})
	if err != nil {
		t.Fatalf("ListTypeCostAggByServiceAndRegions: %v", err)
	}
	m := map[string]CostAgg{}
	for _, a := range typeAgg {
		m[a.Key] = a
	}
	if m["ec2:instance"].Count != 2 || m["ec2:instance"].KnownUSD != 10.0 || m["ec2:instance"].UnknownCount != 1 {
		t.Fatalf("instance agg: %#v", m["ec2:instance"])
	}
	if m["ec2:vpc"].Count != 1 || m["ec2:vpc"].KnownUSD != 0 || m["ec2:vpc"].UnknownCount != 1 {
		t.Fatalf("vpc agg: %#v", m["ec2:vpc"])
	}
}

func TestStore_ListCostIndexTargets(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	k1 := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1")
	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: k1, DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	// No cost row => should be returned.
	targets, err := st.ListCostIndexTargets(ctx, "123456789012", "ec2", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("ListCostIndexTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].Key != k1 {
		t.Fatalf("targets: %#v", targets)
	}

	// Cost computed in the future => should not be returned.
	z := 0.0
	if err := st.UpsertResourceCosts(ctx, []ResourceCostRow{
		{ResourceKey: k1, AccountID: "123456789012", Partition: "aws", Region: "us-east-1", Service: "ec2", Type: "ec2:instance", EstMonthlyUSD: &z, Basis: "test", Breakdown: map[string]any{}, ComputedAt: now.Add(24 * time.Hour), Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResourceCosts: %v", err)
	}

	targets, err = st.ListCostIndexTargets(ctx, "123456789012", "ec2", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("ListCostIndexTargets(2): %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("targets expected empty: %#v", targets)
	}
}
