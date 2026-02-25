package main

import (
	"strings"
	"testing"
	"time"

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

func TestFormatScanPerformanceSummary_Renders(t *testing.T) {
	res := core.ScanResult{
		Performance: core.ScanPerformanceSummary{
			TotalDuration:  92 * time.Second,
			TargetDuration: 60 * time.Second,
			TargetMet:      false,
			PhaseDurations: map[core.ScanProgressPhase]time.Duration{
				core.PhaseProvider: 18 * time.Second,
				core.PhaseResolver: 12 * time.Second,
				core.PhaseAudit:    58 * time.Second,
				core.PhaseCost:     4 * time.Second,
			},
			SlowSteps: []core.ScanSlowStep{
				{Phase: core.PhaseAudit, ProviderID: "cloudtrail", Region: "us-west-2", Duration: 21 * time.Second},
			},
		},
	}

	out := formatScanPerformanceSummary(res)
	mustContain(t, out, "performance: total=1m32s target=1m0s (missed by 32s)")
	mustContain(t, out, "phase provider=18s resolver=12s audit=58s cost=4s")
	mustContain(t, out, "audit")
	mustContain(t, out, "cloudtrail")
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("expected %q to contain %q", s, sub)
	}
}

func TestNewScanCmd_DefaultFlagsAndNoCloudTrail(t *testing.T) {
	t.Setenv("AWSCOPE_SCAN_CONCURRENCY", "")
	t.Setenv("AWSCOPE_RESOLVER_CONCURRENCY", "")
	t.Setenv("AWSCOPE_AUDIT_REGION_CONCURRENCY", "")
	t.Setenv("AWSCOPE_AUDIT_SOURCE_CONCURRENCY", "")
	t.Setenv("AWSCOPE_AUDIT_LOOKUP_INTERVAL_MS", "")
	t.Setenv("AWSCOPE_ELBV2_TARGETHEALTH_CONCURRENCY", "")
	t.Setenv("AWSCOPE_COST_CONCURRENCY", "")
	t.Setenv("AWSCOPE_SCAN_TARGET_SECONDS", "")

	dbPath := ""
	offline := false
	cmd := newScanCmd(&dbPath, &offline)

	if f := cmd.Flags().Lookup("no-cloudtrail"); f == nil {
		t.Fatalf("expected --no-cloudtrail flag")
	}
	if f := cmd.Flags().Lookup("stale-policy"); f == nil {
		t.Fatalf("expected --stale-policy flag")
	}
	if got := cmd.Flags().Lookup("stale-policy").DefValue; got != "hide" {
		t.Fatalf("stale-policy default: got %q want %q", got, "hide")
	}
	if got := cmd.Flags().Lookup("concurrency").DefValue; got != "16" {
		t.Fatalf("concurrency default: got %q want %q", got, "16")
	}
	if got := cmd.Flags().Lookup("resolver-concurrency").DefValue; got != "8" {
		t.Fatalf("resolver default: got %q want %q", got, "8")
	}
	if got := cmd.Flags().Lookup("elbv2-targethealth-concurrency").DefValue; got != "30" {
		t.Fatalf("elbv2 default: got %q want %q", got, "30")
	}
	if got := cmd.Flags().Lookup("cost-concurrency").DefValue; got != "16" {
		t.Fatalf("cost default: got %q want %q", got, "16")
	}
	if f := cmd.Flags().Lookup("aws-max-attempts"); f == nil {
		t.Fatalf("expected --aws-max-attempts flag")
	}
	if f := cmd.Flags().Lookup("aws-retry-mode"); f == nil {
		t.Fatalf("expected --aws-retry-mode flag")
	}
}
