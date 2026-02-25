package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/graph"
)

func TestStore_ListResourceNodesByAccountAndScope(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	nodes := []graph.ResourceNode{
		{
			Key:         graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-1"),
			DisplayName: "i-1",
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-1",
			Attributes:  map[string]any{"state": "running"},
			Raw:         []byte(`{"id":"i-1"}`),
			CollectedAt: now,
		},
		{
			Key:         graph.EncodeResourceKey("aws", "111111111111", "global", "iam:user", "u-1"),
			DisplayName: "u-1",
			Service:     "iam",
			Type:        "iam:user",
			PrimaryID:   "u-1",
			Attributes:  map[string]any{"password_enabled": true},
			Raw:         []byte(`{"id":"u-1"}`),
			CollectedAt: now,
		},
		{
			Key:         graph.EncodeResourceKey("aws", "222222222222", "us-east-1", "ec2:instance", "i-2"),
			DisplayName: "i-2",
			Service:     "ec2",
			Type:        "ec2:instance",
			PrimaryID:   "i-2",
			Attributes:  map[string]any{"state": "running"},
			Raw:         []byte(`{"id":"i-2"}`),
			CollectedAt: now,
		},
	}
	if err := st.UpsertResources(ctx, nodes); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	// Mark IAM row stale; query should exclude it.
	if _, err := st.db.ExecContext(ctx, `UPDATE resources SET lifecycle_state='stale' WHERE service='iam'`); err != nil {
		t.Fatalf("mark stale: %v", err)
	}

	got, err := st.ListResourceNodesByAccountAndScope(ctx, "111111111111", []string{"us-east-1", "global"}, []string{"ec2", "iam"})
	if err != nil {
		t.Fatalf("ListResourceNodesByAccountAndScope: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows: got %d want 1", len(got))
	}
	if got[0].Service != "ec2" || got[0].Type != "ec2:instance" {
		t.Fatalf("unexpected node: %+v", got[0])
	}
	if got[0].Attributes["state"] != "running" {
		t.Fatalf("unexpected attrs: %#v", got[0].Attributes)
	}
}

func TestStore_GetLatestSuccessfulScanRunByProfile(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := Open(OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	t1 := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Hour)
	t3 := t1.Add(2 * time.Hour)

	if err := st.StartScanRun(ctx, "scan-1", "default", `{"provider_ids":["ec2","iam"],"regions":["us-east-1","global"]}`, t1); err != nil {
		t.Fatalf("StartScanRun(scan-1): %v", err)
	}
	if err := st.FinishScanRun(ctx, "scan-1", "success", t1.Add(10*time.Minute)); err != nil {
		t.Fatalf("FinishScanRun(scan-1): %v", err)
	}

	if err := st.StartScanRun(ctx, "scan-2", "default", `{"provider_ids":["s3"],"regions":["us-west-2"]}`, t2); err != nil {
		t.Fatalf("StartScanRun(scan-2): %v", err)
	}
	if err := st.FinishScanRun(ctx, "scan-2", "failed", t2.Add(5*time.Minute)); err != nil {
		t.Fatalf("FinishScanRun(scan-2): %v", err)
	}

	if err := st.StartScanRun(ctx, "scan-3", "default", `{"provider_ids":["cloudtrail"],"regions":["us-east-1","us-west-2"]}`, t3); err != nil {
		t.Fatalf("StartScanRun(scan-3): %v", err)
	}
	if err := st.FinishScanRun(ctx, "scan-3", "success", t3.Add(5*time.Minute)); err != nil {
		t.Fatalf("FinishScanRun(scan-3): %v", err)
	}

	got, ok, err := st.GetLatestSuccessfulScanRunByProfile(ctx, "default")
	if err != nil {
		t.Fatalf("GetLatestSuccessfulScanRunByProfile: %v", err)
	}
	if !ok {
		t.Fatalf("expected scan run")
	}
	if got.ScanID != "scan-3" {
		t.Fatalf("scan_id: got %q want scan-3", got.ScanID)
	}
	if len(got.ProviderIDs) != 1 || got.ProviderIDs[0] != "cloudtrail" {
		t.Fatalf("provider ids: %#v", got.ProviderIDs)
	}
	if len(got.Regions) != 2 || got.Regions[0] != "us-east-1" || got.Regions[1] != "us-west-2" {
		t.Fatalf("regions: %#v", got.Regions)
	}
}
