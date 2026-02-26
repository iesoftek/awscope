package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestStore_UpsertAndGetResource(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	key := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-abc123")
	n := graph.ResourceNode{
		Key:         key,
		DisplayName: "web-1",
		Service:     "ec2",
		Type:        "ec2:instance",
		Arn:         "arn:aws:ec2:us-east-1:123456789012:instance/i-abc123",
		PrimaryID:   "i-abc123",
		Tags:        map[string]string{"Name": "web-1"},
		Attributes:  map[string]any{"state": "running"},
		Raw:         []byte(`{"id":"i-abc123"}`),
		CollectedAt: time.Now().UTC(),
		Source:      "test",
	}

	if err := st.UpsertResources(ctx, []graph.ResourceNode{n}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	got, err := st.GetResource(ctx, key)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Key != n.Key {
		t.Fatalf("key: got %q want %q", got.Key, n.Key)
	}
	if got.DisplayName != "web-1" {
		t.Fatalf("display_name: got %q", got.DisplayName)
	}
	if got.Tags["Name"] != "web-1" {
		t.Fatalf("tags: got %#v", got.Tags)
	}
	if got.Attributes["state"] != "running" {
		t.Fatalf("attrs: got %#v", got.Attributes)
	}
}

func TestStore_UpsertResources_DoesNotClobberWithStubNode(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	key := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1")
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)

	full := graph.ResourceNode{
		Key:         key,
		DisplayName: "web-1",
		Service:     "ec2",
		Type:        "ec2:instance",
		Arn:         "arn:aws:ec2:us-east-1:123456789012:instance/i-1",
		PrimaryID:   "i-1",
		Tags:        map[string]string{"Name": "web-1"},
		Attributes:  map[string]any{"state": "running"},
		Raw:         []byte(`{"id":"i-1","state":"running"}`),
		CollectedAt: now,
		Source:      "test",
	}
	// A stub for the same key (empty tags/attrs/raw/arn) should not overwrite the full node.
	stub := graph.ResourceNode{
		Key:         key,
		DisplayName: "i-1",
		Service:     "ec2",
		Type:        "ec2:instance",
		Arn:         "",
		PrimaryID:   "i-1",
		Tags:        map[string]string{},
		Attributes:  map[string]any{},
		Raw:         []byte(`{}`),
		CollectedAt: now.Add(1 * time.Minute),
		Source:      "stub",
	}

	if err := st.UpsertResources(ctx, []graph.ResourceNode{full, stub}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	got, err := st.GetResource(ctx, key)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.DisplayName != "web-1" {
		t.Fatalf("display_name clobbered: got %q", got.DisplayName)
	}
	if got.Attributes["state"] != "running" {
		t.Fatalf("attrs clobbered: %#v", got.Attributes)
	}
}

func TestStore_UpsertAndQueryEdges(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	a := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-a")
	b := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:security-group", "sg-b")

	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{
		{From: a, To: b, Kind: "attached-to", Meta: map[string]any{"direct": true}},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	edges, err := st.EdgesFrom(ctx, a)
	if err != nil {
		t.Fatalf("EdgesFrom: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("edges len: got %d want 1", len(edges))
	}
	if edges[0].To != b || edges[0].Kind != "attached-to" {
		t.Fatalf("edge: %#v", edges[0])
	}
	if edges[0].Meta["direct"] != true {
		t.Fatalf("meta: %#v", edges[0].Meta)
	}
}

func TestStore_CountAndListPaged(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	for i, name := range []string{"a-web", "b-web", "c-web"} {
		key := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-"+name)
		nodes = append(nodes, graph.ResourceNode{
			Key:         key,
			DisplayName: name,
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-" + name,
			Attributes:  map[string]any{"state": "running", "order": i},
			CollectedAt: now,
			Source:      "test",
		})
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	total, err := st.CountResourceSummariesByServiceAndRegions(ctx, "ec2", []string{"us-east-1"}, "web")
	if err != nil {
		t.Fatalf("CountResourceSummariesByServiceAndRegions: %v", err)
	}
	if total != 3 {
		t.Fatalf("total: got %d want 3", total)
	}

	// Status/attribute search should work too (e.g. state=running stored in attributes_json).
	totalRunning, err := st.CountResourceSummariesByServiceAndRegions(ctx, "ec2", []string{"us-east-1"}, "running")
	if err != nil {
		t.Fatalf("CountResourceSummariesByServiceAndRegions(running): %v", err)
	}
	if totalRunning != 3 {
		t.Fatalf("totalRunning: got %d want 3", totalRunning)
	}

	page1, err := st.ListResourceSummariesByServiceAndRegionsPaged(ctx, "ec2", []string{"us-east-1"}, "web", 2, 0)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceAndRegionsPaged: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len: got %d want 2", len(page1))
	}
	if page1[0].DisplayName != "a-web" {
		t.Fatalf("page1[0]: got %q want %q", page1[0].DisplayName, "a-web")
	}

	page2, err := st.ListResourceSummariesByServiceAndRegionsPaged(ctx, "ec2", []string{"us-east-1"}, "web", 2, 2)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceAndRegionsPaged (page2): %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 len: got %d want 1", len(page2))
	}
	if page2[0].DisplayName != "c-web" {
		t.Fatalf("page2[0]: got %q want %q", page2[0].DisplayName, "c-web")
	}
}

func TestStore_GetResourceSummariesByKeys(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	k1 := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1")
	k2 := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-2")
	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: k1, DisplayName: "one", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: time.Now().UTC(), Source: "test"},
		{Key: k2, DisplayName: "two", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: time.Now().UTC(), Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	m, err := st.GetResourceSummariesByKeys(ctx, []graph.ResourceKey{k2, k1})
	if err != nil {
		t.Fatalf("GetResourceSummariesByKeys: %v", err)
	}
	if m[k1].DisplayName != "one" {
		t.Fatalf("k1: got %q want %q", m[k1].DisplayName, "one")
	}
	if m[k2].DisplayName != "two" {
		t.Fatalf("k2: got %q want %q", m[k2].DisplayName, "two")
	}
}

func TestStore_ListDistinctTypesByServiceAndRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", "vpc-1"), DisplayName: "vpc-1", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "ec2:subnet", "subnet-1"), DisplayName: "subnet-1", Service: "ec2", Type: "ec2:subnet", PrimaryID: "subnet-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "global", "iam:role", "arn:aws:iam::123456789012:role/r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", PrimaryID: "arn:aws:iam::123456789012:role/r1", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	all, err := st.ListDistinctTypesByServiceAndRegions(ctx, "", "ec2", nil)
	if err != nil {
		t.Fatalf("ListDistinctTypesByServiceAndRegions(all): %v", err)
	}
	wantAll := []string{"ec2:instance", "ec2:subnet", "ec2:vpc"}
	if strings.Join(all, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("all: got %v want %v", all, wantAll)
	}

	east, err := st.ListDistinctTypesByServiceAndRegions(ctx, "", "ec2", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("ListDistinctTypesByServiceAndRegions(east): %v", err)
	}
	wantEast := []string{"ec2:instance", "ec2:vpc"}
	if strings.Join(east, ",") != strings.Join(wantEast, ",") {
		t.Fatalf("east: got %v want %v", east, wantEast)
	}
}

func TestStore_ListNeighbors_IncludesOutgoingAndIncoming(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()

	a := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-a")
	b := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:security-group", "sg-b")
	c := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", "vpc-c")

	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: a, DisplayName: "a", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-a", CollectedAt: now, Source: "test"},
		{Key: b, DisplayName: "b", Service: "ec2", Type: "ec2:security-group", PrimaryID: "sg-b", CollectedAt: now, Source: "test"},
		{Key: c, DisplayName: "c", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-c", CollectedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{
		{From: a, To: b, Kind: "attached-to", Meta: map[string]any{"direct": true}},
		{From: c, To: a, Kind: "member-of", Meta: map[string]any{"direct": true}},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	neigh, err := st.ListNeighbors(ctx, a)
	if err != nil {
		t.Fatalf("ListNeighbors: %v", err)
	}
	if len(neigh) != 2 {
		t.Fatalf("neighbors len: got %d want 2", len(neigh))
	}

	var foundOut, foundIn bool
	for _, n := range neigh {
		switch {
		case n.Dir == "out" && n.OtherKey == b && n.Kind == "attached-to":
			foundOut = true
			if n.DisplayName != "b" || n.Type != "ec2:security-group" || n.Region != "us-east-1" {
				t.Fatalf("out neighbor unexpected: %#v", n)
			}
			if n.Meta["direct"] != true {
				t.Fatalf("out meta: %#v", n.Meta)
			}
		case n.Dir == "in" && n.OtherKey == c && n.Kind == "member-of":
			foundIn = true
			if n.DisplayName != "c" || n.Type != "ec2:vpc" {
				t.Fatalf("in neighbor unexpected: %#v", n)
			}
		}
	}
	if !foundOut || !foundIn {
		t.Fatalf("neighbors missing: out=%v in=%v %#v", foundOut, foundIn, neigh)
	}
}

func TestStore_ListServiceCountsByRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", "vpc-1"), DisplayName: "vpc-1", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "ecs:service", "s-1"), DisplayName: "s-1", Service: "ecs", Type: "ecs:service", PrimaryID: "s-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "global", "iam:role", "r-1"), DisplayName: "r-1", Service: "iam", Type: "iam:role", PrimaryID: "r-1", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	all, err := st.ListServiceCountsByRegions(ctx, "", nil)
	if err != nil {
		t.Fatalf("ListServiceCountsByRegions(all): %v", err)
	}
	got := map[string]int{}
	for _, sc := range all {
		got[sc.Service] = sc.Count
	}
	if got["ec2"] != 2 || got["ecs"] != 1 || got["iam"] != 1 {
		t.Fatalf("all counts: %#v", got)
	}

	east, err := st.ListServiceCountsByRegions(ctx, "", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("ListServiceCountsByRegions(east): %v", err)
	}
	gotEast := map[string]int{}
	for _, sc := range east {
		gotEast[sc.Service] = sc.Count
	}
	if gotEast["ec2"] != 2 || gotEast["ecs"] != 0 || gotEast["iam"] != 0 {
		t.Fatalf("east counts: %#v", gotEast)
	}
}

func TestStore_CountAndList_ByServiceTypeAndRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	nodes := []graph.ResourceNode{
		{
			Key:         graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1"),
			DisplayName: "web-1",
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-1",
			Attributes:  map[string]any{"state": "running"},
			CollectedAt: now,
			Source:      "test",
		},
		{
			Key:         graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ec2:instance", "i-2"),
			DisplayName: "web-2",
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-2",
			Attributes:  map[string]any{"state": "stopped"},
			CollectedAt: now,
			Source:      "test",
		},
		{
			Key:         graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:security-group", "sg-1"),
			DisplayName: "sg-1",
			Service:     "ec2",
			Type:        "ec2:security-group",
			PrimaryID:   "sg-1",
			CollectedAt: now,
			Source:      "test",
		},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	// Count and list should agree and should not error due to aliasing issues.
	total, err := st.CountResourceSummariesByServiceTypeAndRegions(ctx, "111111111111", "ec2", "ec2:instance", []string{"us-east-1"}, "")
	if err != nil {
		t.Fatalf("CountResourceSummariesByServiceTypeAndRegions: %v", err)
	}
	if total != 1 {
		t.Fatalf("count: got %d want 1", total)
	}

	ss, err := st.ListResourceSummariesByServiceTypeAndRegionsPaged(ctx, "111111111111", "ec2", "ec2:instance", []string{"us-east-1"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceTypeAndRegionsPaged: %v", err)
	}
	if len(ss) != 1 || ss[0].PrimaryID != "i-1" {
		t.Fatalf("list: got %#v", ss)
	}
}

func TestStore_ListResourceSummariesByServiceTypeAndRegionsPaged_LogsOrderedByStoredBytesDesc(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "logs:log-group", "a"), DisplayName: "a", Service: "logs", Type: "logs:log-group", PrimaryID: "a", Attributes: map[string]any{"storedBytes": int64(10)}, CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "logs:log-group", "b"), DisplayName: "b", Service: "logs", Type: "logs:log-group", PrimaryID: "b", Attributes: map[string]any{"storedBytes": int64(30)}, CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "logs:log-group", "c"), DisplayName: "c", Service: "logs", Type: "logs:log-group", PrimaryID: "c", Attributes: map[string]any{"storedBytes": int64(20)}, CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	ss, err := st.ListResourceSummariesByServiceTypeAndRegionsPaged(ctx, "111111111111", "logs", "logs:log-group", []string{"us-west-2"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceTypeAndRegionsPaged: %v", err)
	}
	if len(ss) != 3 {
		t.Fatalf("len: got %d want 3", len(ss))
	}
	if ss[0].DisplayName != "b" || ss[1].DisplayName != "c" || ss[2].DisplayName != "a" {
		t.Fatalf("order: got %q,%q,%q", ss[0].DisplayName, ss[1].DisplayName, ss[2].DisplayName)
	}
}

func TestStore_ListResourceSummariesByServiceTypeAndRegionsPaged_IAMKeysOrderedByAgeDaysDesc(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "111111111111", "global", "iam:access-key", "AKIA1"), DisplayName: "u/AKIA1", Service: "iam", Type: "iam:access-key", PrimaryID: "AKIA1", Attributes: map[string]any{"age_days": 10, "status": "Active"}, CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "global", "iam:access-key", "AKIA2"), DisplayName: "u/AKIA2", Service: "iam", Type: "iam:access-key", PrimaryID: "AKIA2", Attributes: map[string]any{"age_days": 30, "status": "Active"}, CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "global", "iam:access-key", "AKIA3"), DisplayName: "u/AKIA3", Service: "iam", Type: "iam:access-key", PrimaryID: "AKIA3", Attributes: map[string]any{"age_days": 20, "status": "Inactive"}, CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	ss, err := st.ListResourceSummariesByServiceTypeAndRegionsPaged(ctx, "111111111111", "iam", "iam:access-key", []string{"global"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceTypeAndRegionsPaged: %v", err)
	}
	if len(ss) != 3 {
		t.Fatalf("len: got %d want 3", len(ss))
	}
	if ss[0].PrimaryID != "AKIA2" || ss[1].PrimaryID != "AKIA3" || ss[2].PrimaryID != "AKIA1" {
		t.Fatalf("order: got %q,%q,%q", ss[0].PrimaryID, ss[1].PrimaryID, ss[2].PrimaryID)
	}
}

func TestStore_ECSDrill_ServicesUnderCluster(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	clusterA := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", "arn:aws:ecs:us-west-2:111111111111:cluster/a")
	clusterB := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", "arn:aws:ecs:us-west-2:111111111111:cluster/b")
	svcA := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", "arn:aws:ecs:us-west-2:111111111111:service/a/orders")
	svcB := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", "arn:aws:ecs:us-west-2:111111111111:service/b/payments")

	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: clusterA, DisplayName: "a", Service: "ecs", Type: "ecs:cluster", PrimaryID: string(clusterA), CollectedAt: now, Source: "test"},
		{Key: clusterB, DisplayName: "b", Service: "ecs", Type: "ecs:cluster", PrimaryID: string(clusterB), CollectedAt: now, Source: "test"},
		{Key: svcA, DisplayName: "orders", Service: "ecs", Type: "ecs:service", PrimaryID: string(svcA), Attributes: map[string]any{"runningCount": 3}, CollectedAt: now, Source: "test"},
		{Key: svcB, DisplayName: "payments", Service: "ecs", Type: "ecs:service", PrimaryID: string(svcB), Attributes: map[string]any{"runningCount": 1}, CollectedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{
		{From: svcA, To: clusterA, Kind: "member-of", CollectedAt: now},
		{From: svcB, To: clusterB, Kind: "member-of", CollectedAt: now},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	scope := ECSDrillScope{Level: "services", ClusterKey: clusterA, Region: "us-west-2"}
	total, err := st.CountECSDrillResourceSummaries(ctx, "111111111111", "ecs:service", []string{"us-west-2"}, "", scope)
	if err != nil {
		t.Fatalf("CountECSDrillResourceSummaries: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d want 1", total)
	}
	ss, err := st.ListECSDrillResourceSummariesPaged(ctx, "111111111111", "ecs:service", []string{"us-west-2"}, "", scope, 20, 0)
	if err != nil {
		t.Fatalf("ListECSDrillResourceSummariesPaged: %v", err)
	}
	if len(ss) != 1 || ss[0].DisplayName != "orders" {
		t.Fatalf("summaries: %#v", ss)
	}
}

func TestStore_ECSDrill_TasksByService_EdgeAndFallback(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	clusterArn := "arn:aws:ecs:us-west-2:111111111111:cluster/a"
	clusterKey := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:cluster", clusterArn)
	serviceArn := "arn:aws:ecs:us-west-2:111111111111:service/a/orders"
	serviceKey := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:service", serviceArn)
	taskEdge := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:task", "arn:aws:ecs:us-west-2:111111111111:task/a/edge")
	taskFallback := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:task", "arn:aws:ecs:us-west-2:111111111111:task/a/fallback")
	taskOther := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ecs:task", "arn:aws:ecs:us-west-2:111111111111:task/a/other")

	if err := st.UpsertResources(ctx, []graph.ResourceNode{
		{Key: clusterKey, DisplayName: "a", Service: "ecs", Type: "ecs:cluster", Arn: clusterArn, PrimaryID: clusterArn, CollectedAt: now, Source: "test"},
		{Key: serviceKey, DisplayName: "orders", Service: "ecs", Type: "ecs:service", Arn: serviceArn, PrimaryID: serviceArn, CollectedAt: now, Source: "test"},
		{
			Key:         taskEdge,
			DisplayName: "edge",
			Service:     "ecs",
			Type:        "ecs:task",
			PrimaryID:   string(taskEdge),
			Attributes:  map[string]any{"created_at": "2026-02-25 10:00", "serviceName": "orders", "clusterArn": clusterArn},
			CollectedAt: now,
			Source:      "test",
		},
		{
			Key:         taskFallback,
			DisplayName: "fallback",
			Service:     "ecs",
			Type:        "ecs:task",
			PrimaryID:   string(taskFallback),
			Attributes:  map[string]any{"created_at": "2026-02-25 09:00", "serviceName": "orders", "clusterArn": clusterArn},
			CollectedAt: now,
			Source:      "test",
		},
		{
			Key:         taskOther,
			DisplayName: "other",
			Service:     "ecs",
			Type:        "ecs:task",
			PrimaryID:   string(taskOther),
			Attributes:  map[string]any{"created_at": "2026-02-25 11:00", "serviceName": "payments", "clusterArn": clusterArn},
			CollectedAt: now,
			Source:      "test",
		},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{
		{From: serviceKey, To: clusterKey, Kind: "member-of", CollectedAt: now},
		{From: taskEdge, To: serviceKey, Kind: "belongs-to", CollectedAt: now},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	scope := ECSDrillScope{
		Level:       "tasks",
		ClusterKey:  clusterKey,
		ClusterArn:  clusterArn,
		ServiceKey:  serviceKey,
		ServiceName: "orders",
		Region:      "us-west-2",
	}
	total, err := st.CountECSDrillResourceSummaries(ctx, "111111111111", "ecs:task", []string{"us-west-2"}, "", scope)
	if err != nil {
		t.Fatalf("CountECSDrillResourceSummaries: %v", err)
	}
	if total != 2 {
		t.Fatalf("total: got %d want 2", total)
	}
	ss, err := st.ListECSDrillResourceSummariesPaged(ctx, "111111111111", "ecs:task", []string{"us-west-2"}, "", scope, 20, 0)
	if err != nil {
		t.Fatalf("ListECSDrillResourceSummariesPaged: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("len: got %d want 2", len(ss))
	}
	if ss[0].DisplayName != "edge" || ss[1].DisplayName != "fallback" {
		t.Fatalf("order/results: %#v", ss)
	}
}

func TestStore_DiagramQueries_RegionAndLinkedGlobal(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	r1 := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1")
	r2 := graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ec2:instance", "i-2")
	g1 := graph.EncodeResourceKey("aws", "111111111111", "global", "iam:role", "arn:aws:iam::111111111111:role/r1")
	g2 := graph.EncodeResourceKey("aws", "111111111111", "global", "kms:key", "k-1")

	nodes := []graph.ResourceNode{
		{Key: r1, DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: r2, DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: now, Source: "test"},
		{Key: g1, DisplayName: "r1", Service: "iam", Type: "iam:role", PrimaryID: "arn:aws:iam::111111111111:role/r1", CollectedAt: now, Source: "test"},
		{Key: g2, DisplayName: "k1", Service: "kms", Type: "kms:key", PrimaryID: "k-1", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	edges := []graph.RelationshipEdge{
		{From: r1, To: g1, Kind: "uses", CollectedAt: now},
		{From: g1, To: g2, Kind: "uses", CollectedAt: now},
		{From: r2, To: g2, Kind: "uses", CollectedAt: now},
	}
	if err := st.UpsertEdges(ctx, edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	regional, err := st.ListResourcesByAccountAndRegion(ctx, "111111111111", "us-east-1")
	if err != nil {
		t.Fatalf("ListResourcesByAccountAndRegion: %v", err)
	}
	if len(regional) != 1 || regional[0].Key != r1 {
		t.Fatalf("regional mismatch: %#v", regional)
	}

	linkedGlobal, cross, err := st.ListLinkedGlobalResourcesForRegion(ctx, "111111111111", "us-east-1")
	if err != nil {
		t.Fatalf("ListLinkedGlobalResourcesForRegion: %v", err)
	}
	if len(linkedGlobal) != 1 || linkedGlobal[0].Key != g1 {
		t.Fatalf("linked global mismatch: %#v", linkedGlobal)
	}
	if len(cross) != 1 || cross[0].From != r1 || cross[0].To != g1 {
		t.Fatalf("cross edges mismatch: %#v", cross)
	}

	keys := []graph.ResourceKey{r1, g1, g2}
	selectedEdges, err := st.ListEdgesByResourceKeys(ctx, keys)
	if err != nil {
		t.Fatalf("ListEdgesByResourceKeys: %v", err)
	}
	if len(selectedEdges) != 2 {
		t.Fatalf("selectedEdges len: got %d want 2", len(selectedEdges))
	}
}

func TestStore_ListTypeCountsByServiceAndRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-2"), DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", "vpc-1"), DisplayName: "vpc-1", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "ec2:subnet", "subnet-1"), DisplayName: "subnet-1", Service: "ec2", Type: "ec2:subnet", PrimaryID: "subnet-1", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	all, err := st.ListTypeCountsByServiceAndRegions(ctx, "", "ec2", nil)
	if err != nil {
		t.Fatalf("ListTypeCountsByServiceAndRegions(all): %v", err)
	}
	gotAll := map[string]int{}
	for _, tc := range all {
		gotAll[tc.Type] = tc.Count
	}
	if gotAll["ec2:instance"] != 2 || gotAll["ec2:vpc"] != 1 || gotAll["ec2:subnet"] != 1 {
		t.Fatalf("all type counts: %#v", gotAll)
	}

	east, err := st.ListTypeCountsByServiceAndRegions(ctx, "", "ec2", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("ListTypeCountsByServiceAndRegions(east): %v", err)
	}
	gotEast := map[string]int{}
	for _, tc := range east {
		gotEast[tc.Type] = tc.Count
	}
	if gotEast["ec2:instance"] != 2 || gotEast["ec2:vpc"] != 1 || gotEast["ec2:subnet"] != 0 {
		t.Fatalf("east type counts: %#v", gotEast)
	}
}

func TestStore_ListRegionCountsByServiceAndType(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:vpc", "vpc-1"), DisplayName: "vpc-1", Service: "ec2", Type: "ec2:vpc", PrimaryID: "vpc-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "us-west-2", "ec2:instance", "i-2"), DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "123456789012", "global", "iam:role", "r1"), DisplayName: "r1", Service: "iam", Type: "iam:role", PrimaryID: "r1", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	svc, err := st.ListRegionCountsByService(ctx, "", "ec2")
	if err != nil {
		t.Fatalf("ListRegionCountsByService: %v", err)
	}
	gotSvc := map[string]int{}
	for _, r := range svc {
		gotSvc[r.Region] = r.Count
	}
	if gotSvc["us-east-1"] != 2 || gotSvc["us-west-2"] != 1 {
		t.Fatalf("service counts: got %#v", gotSvc)
	}

	typ, err := st.ListRegionCountsByServiceType(ctx, "", "ec2", "ec2:instance")
	if err != nil {
		t.Fatalf("ListRegionCountsByServiceType: %v", err)
	}
	gotTyp := map[string]int{}
	for _, r := range typ {
		gotTyp[r.Region] = r.Count
	}
	if gotTyp["us-east-1"] != 1 || gotTyp["us-west-2"] != 1 {
		t.Fatalf("type counts: got %#v", gotTyp)
	}
}

func TestStore_UpsertResourcesWithScan_SetsLifecycleAndReactivates(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	key := graph.EncodeResourceKey("aws", "123456789012", "us-east-1", "ec2:instance", "i-1")
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: "web-1",
		Service:     "ec2",
		Type:        "ec2:instance",
		PrimaryID:   "i-1",
		CollectedAt: time.Now().UTC(),
		Source:      "test",
	}
	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{node}, "scan-a"); err != nil {
		t.Fatalf("UpsertResourcesWithScan(scan-a): %v", err)
	}

	var state, lastSeen string
	var missing any
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state, COALESCE(last_seen_scan_id, ''), missing_since FROM resources WHERE resource_key = ?`, string(key)).Scan(&state, &lastSeen, &missing); err != nil {
		t.Fatalf("select lifecycle (1): %v", err)
	}
	if state != "active" || lastSeen != "scan-a" || missing != nil {
		t.Fatalf("unexpected lifecycle(1): state=%q lastSeen=%q missing=%v", state, lastSeen, missing)
	}

	if _, err := st.MarkResourcesStaleNotSeenInScopes(ctx, "123456789012", "scan-b", []ScanScope{{Service: "ec2", Regions: []string{"us-east-1"}}}, time.Now().UTC()); err != nil {
		t.Fatalf("MarkResourcesStaleNotSeenInScopes: %v", err)
	}

	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state FROM resources WHERE resource_key = ?`, string(key)).Scan(&state); err != nil {
		t.Fatalf("select lifecycle (2): %v", err)
	}
	if state != "stale" {
		t.Fatalf("expected stale after mark, got %q", state)
	}

	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{node}, "scan-c"); err != nil {
		t.Fatalf("UpsertResourcesWithScan(scan-c): %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state, COALESCE(last_seen_scan_id, ''), missing_since FROM resources WHERE resource_key = ?`, string(key)).Scan(&state, &lastSeen, &missing); err != nil {
		t.Fatalf("select lifecycle (3): %v", err)
	}
	if state != "active" || lastSeen != "scan-c" || missing != nil {
		t.Fatalf("unexpected lifecycle(3): state=%q lastSeen=%q missing=%v", state, lastSeen, missing)
	}
}

func TestStore_MarkResourcesStaleNotSeenInScopes_Scoped(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1"), DisplayName: "i-1", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-west-2", "ec2:instance", "i-2"), DisplayName: "i-2", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-2", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ecs:service", "svc-1"), DisplayName: "svc-1", Service: "ecs", Type: "ecs:service", PrimaryID: "svc-1", CollectedAt: now, Source: "test"},
		{Key: graph.EncodeResourceKey("aws", "222222222222", "us-east-1", "ec2:instance", "i-3"), DisplayName: "i-3", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-3", CollectedAt: now, Source: "test"},
	}
	if err := st.UpsertResourcesWithScan(ctx, nodes, "scan-a"); err != nil {
		t.Fatalf("UpsertResourcesWithScan: %v", err)
	}
	// Only east ec2 resource is seen in scan-b.
	if err := st.UpsertResourcesWithScan(ctx, nodes[:1], "scan-b"); err != nil {
		t.Fatalf("UpsertResourcesWithScan(scan-b): %v", err)
	}

	n, err := st.MarkResourcesStaleNotSeenInScopes(ctx, "111111111111", "scan-b", []ScanScope{{Service: "ec2", Regions: []string{"us-east-1", "us-west-2"}}}, now)
	if err != nil {
		t.Fatalf("MarkResourcesStaleNotSeenInScopes: %v", err)
	}
	if n != 1 {
		t.Fatalf("stale count: got %d want 1", n)
	}

	var stateEast, stateWest, stateECS, stateOther string
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state FROM resources WHERE resource_key = ?`, string(nodes[0].Key)).Scan(&stateEast); err != nil {
		t.Fatalf("state east: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state FROM resources WHERE resource_key = ?`, string(nodes[1].Key)).Scan(&stateWest); err != nil {
		t.Fatalf("state west: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state FROM resources WHERE resource_key = ?`, string(nodes[2].Key)).Scan(&stateECS); err != nil {
		t.Fatalf("state ecs: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_state FROM resources WHERE resource_key = ?`, string(nodes[3].Key)).Scan(&stateOther); err != nil {
		t.Fatalf("state other: %v", err)
	}
	if stateEast != "active" || stateWest != "stale" || stateECS != "active" || stateOther != "active" {
		t.Fatalf("unexpected states east=%s west=%s ecs=%s other=%s", stateEast, stateWest, stateECS, stateOther)
	}
}

func TestStore_ActiveQueriesExcludeStaleByDefault(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	active := graph.ResourceNode{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-active"), DisplayName: "active", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-active", CollectedAt: now, Source: "test"}
	stale := graph.ResourceNode{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-stale"), DisplayName: "stale", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-stale", CollectedAt: now, Source: "test"}
	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{active, stale}, "scan-a"); err != nil {
		t.Fatalf("UpsertResourcesWithScan: %v", err)
	}
	if _, err := st.MarkResourcesStaleNotSeenInScopes(ctx, "111111111111", "scan-b", []ScanScope{{Service: "ec2", Regions: []string{"us-east-1"}}}, now); err != nil {
		t.Fatalf("MarkResourcesStaleNotSeenInScopes: %v", err)
	}
	// Re-seen active node for scan-b.
	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{active}, "scan-b"); err != nil {
		t.Fatalf("UpsertResourcesWithScan(scan-b): %v", err)
	}

	total, err := st.CountResourceSummariesByServiceTypeAndRegions(ctx, "111111111111", "ec2", "ec2:instance", []string{"us-east-1"}, "")
	if err != nil {
		t.Fatalf("CountResourceSummariesByServiceTypeAndRegions: %v", err)
	}
	if total != 1 {
		t.Fatalf("count: got %d want 1", total)
	}

	ss, err := st.ListResourceSummariesByServiceTypeAndRegionsPaged(ctx, "111111111111", "ec2", "ec2:instance", []string{"us-east-1"}, "", 20, 0)
	if err != nil {
		t.Fatalf("ListResourceSummariesByServiceTypeAndRegionsPaged: %v", err)
	}
	if len(ss) != 1 || ss[0].PrimaryID != "i-active" {
		t.Fatalf("summaries: %#v", ss)
	}
}

func TestStore_PurgeStaleResources(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	active := graph.ResourceNode{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-active"), DisplayName: "active", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-active", CollectedAt: now, Source: "test"}
	stale := graph.ResourceNode{Key: graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-stale"), DisplayName: "stale", Service: "ec2", Type: "ec2:instance", PrimaryID: "i-stale", CollectedAt: now, Source: "test"}
	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{active, stale}, "scan-a"); err != nil {
		t.Fatalf("UpsertResourcesWithScan: %v", err)
	}
	if _, err := st.MarkResourcesStaleNotSeenInScopes(ctx, "111111111111", "scan-b", []ScanScope{{Service: "ec2", Regions: []string{"us-east-1"}}}, now); err != nil {
		t.Fatalf("MarkResourcesStaleNotSeenInScopes: %v", err)
	}
	if err := st.UpsertResourcesWithScan(ctx, []graph.ResourceNode{active}, "scan-b"); err != nil {
		t.Fatalf("UpsertResourcesWithScan(scan-b): %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.RelationshipEdge{{From: active.Key, To: stale.Key, Kind: "uses", CollectedAt: now}}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
	usd := 10.0
	if err := st.UpsertResourceCosts(ctx, []ResourceCostRow{
		{ResourceKey: active.Key, AccountID: "111111111111", Partition: "aws", Region: "us-east-1", Service: "ec2", Type: "ec2:instance", EstMonthlyUSD: &usd, Currency: "USD", Basis: "test", ComputedAt: now, Source: "test"},
		{ResourceKey: stale.Key, AccountID: "111111111111", Partition: "aws", Region: "us-east-1", Service: "ec2", Type: "ec2:instance", EstMonthlyUSD: &usd, Currency: "USD", Basis: "test", ComputedAt: now, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertResourceCosts: %v", err)
	}

	deletedResources, deletedEdges, deletedCosts, err := st.PurgeStaleResources(ctx, "111111111111", nil)
	if err != nil {
		t.Fatalf("PurgeStaleResources: %v", err)
	}
	if deletedResources != 1 || deletedEdges != 1 || deletedCosts != 1 {
		t.Fatalf("deleted counts: resources=%d edges=%d costs=%d", deletedResources, deletedEdges, deletedCosts)
	}

	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM resources WHERE resource_key = ?`, string(stale.Key)).Scan(&n); err != nil {
		t.Fatalf("count stale resource: %v", err)
	}
	if n != 0 {
		t.Fatalf("stale resource not deleted")
	}
}
