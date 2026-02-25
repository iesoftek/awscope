package main

import (
	"strings"
	"testing"

	"awscope/internal/core"
)

func TestFormatDetailedScanSummary_RendersSections(t *testing.T) {
	res := core.ScanResult{
		Summary: core.ScanSummary{
			ServiceCounts: []core.ScanServiceCount{
				{Service: "ec2", Resources: 163},
				{Service: "ecs", Resources: 55},
			},
			ImportantRegions: []core.ScanRegionCount{
				{Region: "us-east-1", Resources: 140, SharePct: 42.9},
				{Region: "ap-south-1", Resources: 71, SharePct: 21.8},
			},
			Pricing: core.ScanPricingSummary{KnownUSD: 1234.56, UnknownCount: 27, Currency: "USD"},
		},
	}

	out := formatDetailedScanSummary(res)
	mustContain(t, out, "summary:")
	mustContain(t, out, "resources by service:")
	mustContain(t, out, "important regions (top 5 by resource count):")
	mustContain(t, out, "estimated monthly pricing:")
	mustContain(t, out, "ec2")
	mustContain(t, out, "us-east-1")
	mustContain(t, out, "$1234.56/mo")
	mustContain(t, out, "unknown resources: 27")
}

func TestFormatDetailedScanSummary_TruncatesServiceList(t *testing.T) {
	serviceCounts := make([]core.ScanServiceCount, 0, 13)
	for i := 0; i < 13; i++ {
		serviceCounts = append(serviceCounts, core.ScanServiceCount{Service: "svc" + string(rune('A'+i)), Resources: 100 - i})
	}
	res := core.ScanResult{Summary: core.ScanSummary{ServiceCounts: serviceCounts}}

	out := formatDetailedScanSummary(res)
	mustContain(t, out, "... (+1 more)")
}

func TestFormatDetailedScanSummary_EmptySections(t *testing.T) {
	out := formatDetailedScanSummary(core.ScanResult{})
	mustContain(t, out, "resources by service:")
	mustContain(t, out, "important regions (top 5 by resource count):")
	mustContain(t, out, "- none")
	mustContain(t, out, "known total: $0.00/mo")
	mustContain(t, out, "unknown resources: 0")
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q to contain %q", s, sub)
	}
}
