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
