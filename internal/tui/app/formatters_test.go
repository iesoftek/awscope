package app

import (
	"awscope/internal/catalog"
	"awscope/internal/store"
	"awscope/internal/tui/theme"
	"awscope/internal/tui/widgets/table"
	"strings"
	"testing"
)

func titleIndex(cols []table.Column, title string) int {
	for i := range cols {
		if cols[i].Title == title {
			return i
		}
	}
	return -1
}

func TestBuildResourceColumnsAndRowsAligned(t *testing.T) {
	preset := catalog.ResourceTablePreset("ec2", "ec2:key-pair")
	cols := buildResourceColumns(120, preset, false)
	rows := makeResourceRows([]store.ResourceSummary{{
		DisplayName: "deploy-key",
		PrimaryID:   "key-123",
		Region:      "us-west-2",
		Type:        "ec2:key-pair",
		Attributes: map[string]any{
			"keyType":        "ed25519",
			"keyFingerprint": "ab:cd:ef",
			"created_at":     "2026-02-20 10:00",
		},
	}}, preset, false, theme.Styles{}, nil)

	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if got, want := len(rows[0]), len(cols); got != want {
		t.Fatalf("row/column mismatch: row=%d cols=%d", got, want)
	}
	if titleIndex(cols, "Status") >= 0 {
		t.Fatalf("ec2:key-pair should not include Status column")
	}
}

func TestIAMAccessKeyColumnsRenderAgeAndLastUsed(t *testing.T) {
	preset := catalog.ResourceTablePreset("iam", "iam:access-key")
	cols := buildResourceColumns(140, preset, false)
	rows := makeResourceRows([]store.ResourceSummary{{
		DisplayName: "builder / AKIA...TEST",
		PrimaryID:   "AKIA123456789TEST",
		Region:      "global",
		Type:        "iam:access-key",
		Attributes: map[string]any{
			"status":           "Active",
			"age_days":         12,
			"last_used_at":     "2026-02-01 08:00",
			"last_used_region": "us-west-2",
			"created_at":       "2026-01-20 10:00",
		},
	}}, preset, false, theme.Styles{}, nil)

	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if got, want := len(rows[0]), len(cols); got != want {
		t.Fatalf("row/column mismatch: row=%d cols=%d", got, want)
	}
	ageIdx := titleIndex(cols, "Age")
	if ageIdx < 0 {
		t.Fatalf("missing Age column")
	}
	if rows[0][ageIdx] != "12d" {
		t.Fatalf("unexpected age value: %q", rows[0][ageIdx])
	}
	lastUsedIdx := titleIndex(cols, "LastUsed")
	if lastUsedIdx < 0 {
		t.Fatalf("missing LastUsed column")
	}
	if !strings.Contains(rows[0][lastUsedIdx], "2026-02-01") {
		t.Fatalf("unexpected last used value: %q", rows[0][lastUsedIdx])
	}
}

func TestLogsColumnsRenderStored(t *testing.T) {
	preset := catalog.ResourceTablePreset("logs", "logs:log-group")
	cols := buildResourceColumns(130, preset, false)
	rows := makeResourceRows([]store.ResourceSummary{{
		DisplayName: "app/logs",
		PrimaryID:   "arn:aws:logs:us-west-2:111111111111:log-group:app/logs",
		Region:      "us-west-2",
		Type:        "logs:log-group",
		Attributes: map[string]any{
			"class":         "STANDARD",
			"retentionDays": 30,
			"storedBytes":   int64(1024 * 1024 * 1024),
			"created_at":    "2026-01-01 00:00",
		},
	}}, preset, false, theme.Styles{}, nil)

	if got, want := len(rows[0]), len(cols); got != want {
		t.Fatalf("row/column mismatch: row=%d cols=%d", got, want)
	}
	storedIdx := titleIndex(cols, "Stored")
	if storedIdx < 0 {
		t.Fatalf("missing Stored column")
	}
	if !strings.Contains(rows[0][storedIdx], "GiB") {
		t.Fatalf("unexpected stored value: %q", rows[0][storedIdx])
	}
}

func TestPricingModeAppendsEstMoColumn(t *testing.T) {
	preset := catalog.ResourceTablePreset("ec2", "ec2:instance")
	usd := 67.74
	cols := buildResourceColumns(140, preset, true)
	rows := makeResourceRows([]store.ResourceSummary{{
		DisplayName:   "api-1",
		PrimaryID:     "i-0123456789abcdef0",
		Region:        "us-west-2",
		Type:          "ec2:instance",
		EstMonthlyUSD: &usd,
		Attributes: map[string]any{
			"state":        "running",
			"instanceType": "t3.medium",
			"az":           "us-west-2a",
			"created_at":   "2026-01-10 09:00",
		},
	}}, preset, true, theme.Styles{}, nil)

	if got, want := len(rows[0]), len(cols); got != want {
		t.Fatalf("row/column mismatch: row=%d cols=%d", got, want)
	}
	costIdx := titleIndex(cols, "Est/mo")
	if costIdx < 0 {
		t.Fatalf("missing Est/mo column")
	}
	if !strings.Contains(rows[0][costIdx], "$") {
		t.Fatalf("unexpected cost cell: %q", rows[0][costIdx])
	}
}

func TestUnknownTypeFallsBackCleanly(t *testing.T) {
	preset := catalog.ResourceTablePreset("unknown-service", "unknown:type")
	cols := buildResourceColumns(100, preset, false)
	rows := makeResourceRows([]store.ResourceSummary{{
		DisplayName: "x",
		PrimaryID:   "id-x",
		Region:      "us-west-2",
		Type:        "unknown:type",
	}}, preset, false, theme.Styles{}, nil)
	if len(cols) == 0 {
		t.Fatalf("expected fallback columns")
	}
	if got, want := len(rows[0]), len(cols); got != want {
		t.Fatalf("row/column mismatch: row=%d cols=%d", got, want)
	}
	if titleIndex(cols, "Status") < 0 {
		t.Fatalf("fallback columns should include Status")
	}
}
