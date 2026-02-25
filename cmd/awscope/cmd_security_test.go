package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"awscope/internal/core"
	"awscope/internal/store"
)

func TestFormatSecuritySummary_Renders(t *testing.T) {
	sec := core.ScanSecuritySummary{
		Findings: []core.ScanSecurityFinding{
			{
				CheckID:       "CT-001",
				Severity:      core.ScanSecuritySeverityCritical,
				Title:         "No logging CloudTrail trail found",
				ControlRef:    "CloudTrail.1",
				AffectedCount: 1,
				Regions:       []string{"us-east-1"},
				Samples:       []string{"trail-1"},
			},
		},
		AffectedBySeverity: map[core.ScanSecuritySeverity]int{
			core.ScanSecuritySeverityCritical: 1,
			core.ScanSecuritySeverityHigh:     2,
			core.ScanSecuritySeverityMedium:   3,
			core.ScanSecuritySeverityLow:      0,
		},
		Coverage: core.ScanSecurityCoverage{
			AssessedChecks:  10,
			SkippedChecks:   2,
			MissingServices: []string{"guardduty"},
		},
	}

	out := formatSecuritySummary(sec)
	mustContain(t, out, "security findings:")
	mustContain(t, out, "posture (affected): critical=1 high=2 medium=3 low=0")
	mustContain(t, out, "[CT-001]")
	mustContain(t, out, "coverage gaps: services not assessed: guardduty")
}

func TestNewSecurityCmd_ProfileRequired(t *testing.T) {
	dbPath := ""
	cmd := newSecurityCmd(&dbPath)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--profile is required") {
		t.Fatalf("expected profile-required error, got %v", err)
	}
}

func TestNewSecurityCmd_UnknownProfile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = st.Close()

	cmd := newSecurityCmd(&dbPath)
	cmd.SetArgs([]string{"--profile", "missing"})
	err = cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("expected unknown profile error, got %v", err)
	}
}

func TestRootCommand_RegistersSecurity(t *testing.T) {
	dbPath := ""
	offline := false
	root := newRootCommand(context.Background(), &dbPath, &offline)
	cmd, _, err := root.Find([]string{"security"})
	if err != nil {
		t.Fatalf("Find(security): %v", err)
	}
	if cmd == nil || cmd.Name() != "security" {
		t.Fatalf("security command not registered")
	}
}

func TestFormatSecuritySummary_SummaryViewCollapsesDetails(t *testing.T) {
	sec := core.ScanSecuritySummary{
		Findings: []core.ScanSecurityFinding{
			{
				CheckID:       "EC2-002",
				Severity:      core.ScanSecuritySeverityHigh,
				Title:         "EC2 security group has world-open ingress",
				AffectedCount: 2,
				Regions:       []string{"us-east-1"},
				Samples:       []string{"sg-aaa"},
			},
		},
		AffectedBySeverity: map[core.ScanSecuritySeverity]int{
			core.ScanSecuritySeverityHigh: 2,
		},
	}

	out := formatSecuritySummaryWithOptions(sec, securitySummaryFormatOptions{
		ShowDetails: false,
		Color:       false,
	})
	mustContain(t, out, "details: collapsed")
	mustContain(t, out, "[EC2-002]")
	if strings.Contains(out, "samples:") || strings.Contains(out, "regions:") {
		t.Fatalf("expected collapsed summary to omit samples/regions, got:\n%s", out)
	}
}

func TestParseSecurityDetailView_Invalid(t *testing.T) {
	if _, err := parseSecurityDetailView("weird"); err == nil {
		t.Fatalf("expected error for invalid security detail view")
	}
}

func TestResolveColorEnabled_Invalid(t *testing.T) {
	if _, err := resolveColorEnabled("weird", &bytes.Buffer{}); err == nil {
		t.Fatalf("expected error for invalid color mode")
	}
}
