package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestStore_CloudTrailEvents_CRUDAndFilters(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	k1 := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1")

	rows := []CloudTrailEventRow{
		{
			EventID:      "e-1",
			AccountID:    "111111111111",
			Partition:    "aws",
			Region:       "us-east-1",
			EventTime:    now.Add(-time.Hour),
			EventSource:  "ec2.amazonaws.com",
			EventName:    "RunInstances",
			Action:       "create",
			Service:      "ec2",
			ResourceKey:  k1,
			ResourceType: "ec2:instance",
			ResourceName: "i-1",
			Username:     "alice",
			EventJSON:    []byte(`{"eventName":"RunInstances"}`),
			IndexedAt:    now,
		},
		{
			EventID:      "e-2",
			AccountID:    "111111111111",
			Partition:    "aws",
			Region:       "us-west-2",
			EventTime:    now.Add(-2 * time.Hour),
			EventSource:  "iam.amazonaws.com",
			EventName:    "DeleteRole",
			Action:       "delete",
			Service:      "iam",
			ResourceType: "iam:role",
			ResourceName: "old-role",
			Username:     "bob",
			EventJSON:    []byte(`{"eventName":"DeleteRole"}`),
			IndexedAt:    now,
		},
	}
	if err := st.UpsertCloudTrailEvents(ctx, rows); err != nil {
		t.Fatalf("UpsertCloudTrailEvents: %v", err)
	}

	// Upsert dedupe/update.
	if err := st.UpsertCloudTrailEvents(ctx, []CloudTrailEventRow{
		{
			EventID:      "e-2",
			AccountID:    "111111111111",
			Partition:    "aws",
			Region:       "us-west-2",
			EventTime:    now.Add(-30 * time.Minute),
			EventSource:  "iam.amazonaws.com",
			EventName:    "DeleteRole",
			Action:       "delete",
			Service:      "iam",
			ResourceType: "iam:role",
			ResourceName: "old-role",
			Username:     "carol",
			EventJSON:    []byte(`{"eventName":"DeleteRole","updated":true}`),
			IndexedAt:    now,
		},
	}); err != nil {
		t.Fatalf("UpsertCloudTrailEvents(update): %v", err)
	}

	total, err := st.CountCloudTrailEvents(ctx, "111111111111", []string{"us-east-1", "us-west-2"}, "")
	if err != nil {
		t.Fatalf("CountCloudTrailEvents: %v", err)
	}
	if total != 2 {
		t.Fatalf("count: got %d want 2", total)
	}

	filtered, err := st.CountCloudTrailEvents(ctx, "111111111111", []string{"us-east-1", "us-west-2"}, "runinstances")
	if err != nil {
		t.Fatalf("CountCloudTrailEvents(filter): %v", err)
	}
	if filtered != 1 {
		t.Fatalf("filtered count: got %d want 1", filtered)
	}

	list, err := st.ListCloudTrailEventsPaged(ctx, "111111111111", []string{"us-east-1", "us-west-2"}, "", 10, 0)
	if err != nil {
		t.Fatalf("ListCloudTrailEventsPaged: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len: got %d want 2", len(list))
	}
	// Newer updated e-2 should be first.
	if list[0].EventID != "e-2" {
		t.Fatalf("list order: first=%s want e-2", list[0].EventID)
	}

	got, ok, err := st.GetCloudTrailEventByID(ctx, "111111111111", "e-2")
	if err != nil {
		t.Fatalf("GetCloudTrailEventByID: %v", err)
	}
	if !ok {
		t.Fatalf("GetCloudTrailEventByID: not found")
	}
	if got.Username != "carol" {
		t.Fatalf("updated row not returned: username=%q", got.Username)
	}

	// Prune by account should remove old rows.
	n, err := st.PruneCloudTrailEventsOlderThan(ctx, "111111111111", now.Add(-45*time.Minute))
	if err != nil {
		t.Fatalf("PruneCloudTrailEventsOlderThan: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned: got %d want 1", n)
	}
}

func TestStore_CloudTrailEvents_CursorAndFacets(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	rows := []CloudTrailEventRow{
		{EventID: "e-1", AccountID: "111111111111", Partition: "aws", Region: "us-east-1", EventTime: now.Add(-1 * time.Minute), EventSource: "ec2.amazonaws.com", EventName: "RunInstances", Action: "create", Service: "ec2", Username: "alice", EventJSON: []byte(`{}`), IndexedAt: now},
		{EventID: "e-2", AccountID: "111111111111", Partition: "aws", Region: "us-east-1", EventTime: now.Add(-2 * time.Minute), EventSource: "ec2.amazonaws.com", EventName: "TerminateInstances", Action: "delete", Service: "ec2", Username: "alice", EventJSON: []byte(`{}`), IndexedAt: now},
		{EventID: "e-3", AccountID: "111111111111", Partition: "aws", Region: "us-east-1", EventTime: now.Add(-3 * time.Minute), EventSource: "iam.amazonaws.com", EventName: "CreateRole", Action: "create", Service: "iam", Username: "bob", EventJSON: []byte(`{}`), IndexedAt: now},
		{EventID: "e-4", AccountID: "111111111111", Partition: "aws", Region: "us-east-1", EventTime: now.Add(-4 * time.Minute), EventSource: "iam.amazonaws.com", EventName: "DeleteRole", Action: "delete", Service: "iam", Username: "carol", ErrorCode: "AccessDenied", EventJSON: []byte(`{}`), IndexedAt: now},
		{EventID: "e-5", AccountID: "111111111111", Partition: "aws", Region: "us-west-2", EventTime: now.Add(-5 * time.Minute), EventSource: "ecs.amazonaws.com", EventName: "CreateService", Action: "create", Service: "ecs", Username: "dan", EventJSON: []byte(`{}`), IndexedAt: now},
	}
	if err := st.UpsertCloudTrailEvents(ctx, rows); err != nil {
		t.Fatalf("UpsertCloudTrailEvents: %v", err)
	}

	q := CloudTrailEventQuery{
		Regions: []string{"us-east-1", "us-west-2"},
		Limit:   2,
	}
	p1, err := st.ListCloudTrailEventsByCursor(ctx, "111111111111", q, nil, nil)
	if err != nil {
		t.Fatalf("ListCloudTrailEventsByCursor(p1): %v", err)
	}
	if len(p1.Events) != 2 || !p1.HasNext || p1.HasPrev {
		t.Fatalf("p1 unexpected page meta: len=%d hasNext=%v hasPrev=%v", len(p1.Events), p1.HasNext, p1.HasPrev)
	}
	if p1.Events[0].EventID != "e-1" || p1.Events[1].EventID != "e-2" {
		t.Fatalf("p1 order unexpected: %#v", []string{p1.Events[0].EventID, p1.Events[1].EventID})
	}

	p2, err := st.ListCloudTrailEventsByCursor(ctx, "111111111111", q, p1.NextCursor, nil)
	if err != nil {
		t.Fatalf("ListCloudTrailEventsByCursor(p2): %v", err)
	}
	if len(p2.Events) != 2 || !p2.HasPrev {
		t.Fatalf("p2 unexpected page meta: len=%d hasPrev=%v", len(p2.Events), p2.HasPrev)
	}
	if p2.Events[0].EventID != "e-3" || p2.Events[1].EventID != "e-4" {
		t.Fatalf("p2 order unexpected: %#v", []string{p2.Events[0].EventID, p2.Events[1].EventID})
	}

	p1b, err := st.ListCloudTrailEventsByCursor(ctx, "111111111111", q, nil, p2.PrevCursor)
	if err != nil {
		t.Fatalf("ListCloudTrailEventsByCursor(prev): %v", err)
	}
	if len(p1b.Events) != 2 || p1b.Events[0].EventID != "e-1" || p1b.Events[1].EventID != "e-2" {
		t.Fatalf("p1b order unexpected: %#v", p1b.Events)
	}

	cnt, err := st.CountCloudTrailEventsByQuery(ctx, "111111111111", CloudTrailEventQuery{
		Regions:    []string{"us-east-1"},
		Actions:    []string{"delete"},
		Services:   []string{"iam"},
		EventNames: []string{"DeleteRole"},
		OnlyErrors: true,
	})
	if err != nil {
		t.Fatalf("CountCloudTrailEventsByQuery: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("filtered count unexpected: got=%d want=1", cnt)
	}

	facets, err := st.ListCloudTrailEventFacets(ctx, "111111111111", CloudTrailEventQuery{
		Regions: []string{"us-east-1", "us-west-2"},
		Limit:   20,
	}, 20)
	if err != nil {
		t.Fatalf("ListCloudTrailEventFacets: %v", err)
	}
	if len(facets.Actions) == 0 || len(facets.Services) == 0 || len(facets.EventNames) == 0 {
		t.Fatalf("facets unexpectedly empty: %#v", facets)
	}
}
