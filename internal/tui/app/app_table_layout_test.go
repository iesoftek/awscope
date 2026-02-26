package app

import (
	"awscope/internal/store"
	"awscope/internal/tui/theme"
	"awscope/internal/tui/widgets/table"
	"testing"
)

func TestApplyResourceTableLayoutAcrossTypeSwitches(t *testing.T) {
	m := model{
		resources: table.New(table.WithColumns(nil), table.WithRows(nil)),
		styles:    theme.Styles{},
	}

	cases := []struct {
		name       string
		service    string
		typ        string
		pricing    bool
		summaries  []store.ResourceSummary
		expectCols int
	}{
		{
			name:    "ec2 key pair no status",
			service: "ec2",
			typ:     "ec2:key-pair",
			pricing: false,
			summaries: []store.ResourceSummary{{
				DisplayName: "deploy",
				PrimaryID:   "kp-1",
				Region:      "us-west-2",
				Type:        "ec2:key-pair",
				Attributes: map[string]any{
					"keyType":        "ed25519",
					"keyFingerprint": "ab:cd",
					"created_at":     "2026-02-20 10:00",
				},
			}},
			expectCols: 1,
		},
		{
			name:    "logs stored column",
			service: "logs",
			typ:     "logs:log-group",
			pricing: false,
			summaries: []store.ResourceSummary{{
				DisplayName: "app/logs",
				PrimaryID:   "arn:aws:logs:us-west-2:111111111111:log-group:app/logs",
				Region:      "us-west-2",
				Type:        "logs:log-group",
				Attributes: map[string]any{
					"class":         "STANDARD",
					"retentionDays": 14,
					"storedBytes":   int64(1024 * 1024 * 1024),
					"created_at":    "2026-01-01 00:00",
				},
			}},
			expectCols: 1,
		},
		{
			name:    "iam key pricing",
			service: "iam",
			typ:     "iam:access-key",
			pricing: true,
			summaries: []store.ResourceSummary{{
				DisplayName: "builder / AKIA...",
				PrimaryID:   "AKIA123456789TEST",
				Region:      "global",
				Type:        "iam:access-key",
				Attributes: map[string]any{
					"status":       "Active",
					"age_days":     42,
					"last_used_at": "2026-02-01 08:00",
					"created_at":   "2025-12-20 08:00",
				},
			}},
			expectCols: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.selectedService = tc.service
			m.selectedType = tc.typ
			m.pricingMode = tc.pricing

			m.applyResourceTableLayout(120, tc.summaries)

			cols := m.resources.Columns()
			if len(cols) < tc.expectCols {
				t.Fatalf("expected at least %d columns, got %d", tc.expectCols, len(cols))
			}
			rows := m.resources.Rows()
			for i := range rows {
				if got, want := len(rows[i]), len(cols); got != want {
					t.Fatalf("row %d/column mismatch: row=%d cols=%d", i, got, want)
				}
			}
		})
	}
}
