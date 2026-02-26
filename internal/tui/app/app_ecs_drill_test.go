package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/graph"
	"awscope/internal/store"
	"awscope/internal/tui/components/navigator"

	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/textinput"
)

func TestECSDrillDownAndUpTransitions(t *testing.T) {
	clusterKey := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", "arn:aws:ecs:us-west-2:111111111111:cluster/a")
	serviceKey := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", "arn:aws:ecs:us-west-2:111111111111:service/a/orders")

	nav := navigator.New([]string{"ecs"}, func(string) []string {
		return []string{"ecs:cluster", "ecs:service", "ecs:task", "ecs:task-definition"}
	})
	nav.SetSelection("ecs", "ecs:cluster")

	m := model{
		nav:             nav,
		selectedService: "ecs",
		selectedType:    "ecs:cluster",
		resourceSummaries: []store.ResourceSummary{
			{Key: clusterKey, DisplayName: "cluster-a", Service: "ecs", Type: "ecs:cluster", Arn: "arn:aws:ecs:us-west-2:111111111111:cluster/a", Region: "us-west-2"},
		},
	}

	if cmd := m.drillECSDownCmd(); cmd == nil {
		t.Fatalf("expected drill down cmd from cluster")
	}
	if m.ecsDrill.Level != ecsDrillServices || m.selectedType != "ecs:service" {
		t.Fatalf("expected services drill level, got level=%v type=%s", m.ecsDrill.Level, m.selectedType)
	}

	m.resourceSummaries = []store.ResourceSummary{
		{Key: serviceKey, DisplayName: "orders", Service: "ecs", Type: "ecs:service", Arn: "arn:aws:ecs:us-west-2:111111111111:service/a/orders", Region: "us-west-2"},
	}
	if cmd := m.drillECSDownCmd(); cmd == nil {
		t.Fatalf("expected drill down cmd from service")
	}
	if m.ecsDrill.Level != ecsDrillTasks || m.selectedType != "ecs:task" {
		t.Fatalf("expected tasks drill level, got level=%v type=%s", m.ecsDrill.Level, m.selectedType)
	}

	if cmd := m.drillECSUpCmd(); cmd == nil {
		t.Fatalf("expected drill up cmd from tasks")
	}
	if m.ecsDrill.Level != ecsDrillServices || m.selectedType != "ecs:service" {
		t.Fatalf("expected services drill level after up, got level=%v type=%s", m.ecsDrill.Level, m.selectedType)
	}

	if cmd := m.drillECSUpCmd(); cmd == nil {
		t.Fatalf("expected drill up cmd from services")
	}
	if m.ecsDrill.Level != ecsDrillNone || m.selectedType != "ecs:cluster" {
		t.Fatalf("expected cluster level after second up, got level=%v type=%s", m.ecsDrill.Level, m.selectedType)
	}
}

func TestLoadResourcesCmd_UsesECSDrillScope(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	clusterArn := "arn:aws:ecs:us-west-2:111111111111:cluster/a"
	clusterKey := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", clusterArn)
	svcA := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", "arn:aws:ecs:us-west-2:111111111111:service/a/orders")
	svcB := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", "arn:aws:ecs:us-west-2:111111111111:service/b/payments")

	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: clusterKey, DisplayName: "a", Service: "ecs", Type: "ecs:cluster", Arn: clusterArn, PrimaryID: clusterArn, CollectedAt: now, Source: "test"},
		{Key: svcA, DisplayName: "orders", Service: "ecs", Type: "ecs:service", PrimaryID: string(svcA), Attributes: map[string]any{"runningCount": 4}, CollectedAt: now, Source: "test"},
		{Key: svcB, DisplayName: "payments", Service: "ecs", Type: "ecs:service", PrimaryID: string(svcB), Attributes: map[string]any{"runningCount": 1}, CollectedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{
		{From: svcA, To: clusterKey, Kind: "member-of", CollectedAt: now},
		{From: svcB, To: graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", "arn:aws:ecs:us-west-2:111111111111:cluster/b"), Kind: "member-of", CollectedAt: now},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	p := paginator.New()
	p.PerPage = 50
	m := model{
		ctx:             ctx,
		st:              st,
		accountID:       "111111111111",
		selectedService: "ecs",
		selectedType:    "ecs:service",
		selectedRegions: map[string]bool{"us-west-2": true},
		knownRegions:    []string{"us-west-2"},
		pager:           p,
		filter:          textinput.New(),
		ecsDrill:        ecsDrillState{Level: ecsDrillServices, ClusterKey: clusterKey, ClusterArn: clusterArn, ClusterName: "a", Region: "us-west-2"},
	}

	cmd := m.loadResourcesCmd()
	if cmd == nil {
		t.Fatalf("expected loadResourcesCmd")
	}
	msg, ok := cmd().(resourcesLoadedMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("resourcesLoadedMsg err: %v", msg.err)
	}
	if msg.total != 1 || len(msg.summaries) != 1 || msg.summaries[0].DisplayName != "orders" {
		t.Fatalf("unexpected drill results: total=%d summaries=%#v", msg.total, msg.summaries)
	}
}

func TestApplyECSSelectionClearsDrillForNonECS(t *testing.T) {
	m := model{
		ecsDrill: ecsDrillState{Level: ecsDrillTasks, ClusterName: "a", ServiceName: "orders", Region: "us-west-2"},
	}
	m.applyECSSelection("s3", "s3:bucket", true)
	if m.ecsDrill.Level != ecsDrillNone {
		t.Fatalf("expected drill cleared for non-ecs selection")
	}
}

func TestClearECSDrillIfRegionOutOfScope(t *testing.T) {
	m := model{
		selectedService: "ecs",
		selectedType:    "ecs:service",
		selectedRegions: map[string]bool{"us-east-1": true},
		knownRegions:    []string{"us-east-1", "us-west-2"},
		ecsDrill:        ecsDrillState{Level: ecsDrillServices, Region: "us-west-2"},
	}
	m.clearECSDrillIfRegionOutOfScope()
	if m.ecsDrill.Level != ecsDrillNone {
		t.Fatalf("expected drill cleared when region out of scope")
	}
}
