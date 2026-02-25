package scanui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"awscope/internal/core"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
)

func TestTrackProgressEventLifecycle(t *testing.T) {
	m := &model{active: map[string]activeStep{}}
	t0 := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	start := core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: "iam",
		Region:     "global",
		Message:    "listing",
	}
	k := stepKey(start)
	m.trackProgressEvent(start, t0)
	if len(m.active) != 1 {
		t.Fatalf("active len: got %d want 1", len(m.active))
	}
	if len(m.activeOrder) != 1 || m.activeOrder[0] != k {
		t.Fatalf("active order: %#v", m.activeOrder)
	}
	s0 := m.active[k]

	hb := start
	hb.Message = "listing page=2"
	m.trackProgressEvent(hb, t0.Add(5*time.Second))
	if len(m.active) != 1 {
		t.Fatalf("active len after heartbeat: got %d want 1", len(m.active))
	}
	s1 := m.active[k]
	if !s1.StartedAt.Equal(s0.StartedAt) {
		t.Fatalf("startedAt changed: before=%s after=%s", s0.StartedAt, s1.StartedAt)
	}
	if !s1.UpdatedAt.After(s0.UpdatedAt) {
		t.Fatalf("updatedAt did not advance: before=%s after=%s", s0.UpdatedAt, s1.UpdatedAt)
	}
	if s1.Message != "listing page=2" {
		t.Fatalf("message: got %q", s1.Message)
	}

	done := start
	done.Message = "done"
	m.trackProgressEvent(done, t0.Add(6*time.Second))
	if len(m.active) != 0 {
		t.Fatalf("active not cleared on done: %#v", m.active)
	}
	m.compactActiveOrder()
	if len(m.activeOrder) != 0 {
		t.Fatalf("activeOrder not compacted: %#v", m.activeOrder)
	}
}

func TestActivePhaseCounts(t *testing.T) {
	m := &model{
		active: map[string]activeStep{
			"a": {Phase: core.PhaseProvider},
			"b": {Phase: core.PhaseProvider},
			"c": {Phase: core.PhaseResolver},
			"d": {Phase: core.PhaseAudit},
			"e": {Phase: core.PhaseCost},
		},
	}
	total, p, r, a, c := m.activePhaseCounts()
	if total != 5 || p != 2 || r != 1 || a != 1 || c != 1 {
		t.Fatalf("counts mismatch: total=%d p=%d r=%d a=%d c=%d", total, p, r, a, c)
	}
}

func TestRenderActiveRosterTruncatesWithMore(t *testing.T) {
	now := time.Date(2026, 2, 25, 10, 2, 0, 0, time.UTC)
	m := &model{
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "iam", Region: "global", StartedAt: now.Add(-60 * time.Second)},
			"k2": {Key: "k2", Phase: core.PhaseProvider, ProviderID: "ecs", Region: "us-west-2", StartedAt: now.Add(-20 * time.Second)},
			"k3": {Key: "k3", Phase: core.PhaseAudit, ProviderID: "cloudtrail", Region: "us-east-1", StartedAt: now.Add(-120 * time.Second)},
		},
		activeOrder: []string{"k1", "k2", "k3"},
	}
	full := m.renderActiveRoster(200, now)
	fullPlain := stripANSI(full)
	if !strings.Contains(fullPlain, "[iam/global") || !strings.Contains(fullPlain, "[ecs/us-west-2") || !strings.Contains(fullPlain, "[cloudtrail/us-east-1") {
		t.Fatalf("full roster missing tokens: %q", full)
	}

	short := m.renderActiveRoster(40, now)
	shortPlain := stripANSI(short)
	if !strings.Contains(shortPlain, "(+1 more)") && !strings.Contains(shortPlain, "(+2 more)") {
		t.Fatalf("expected overflow marker in short roster, got %q", short)
	}
}

func TestRenderActiveRosterDisambiguatesSameProviderRegionAcrossPhases(t *testing.T) {
	now := time.Date(2026, 2, 25, 10, 5, 0, 0, time.UTC)
	m := &model{
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "elbv2", Region: "us-east-1", StartedAt: now.Add(-3 * time.Second)},
			"k2": {Key: "k2", Phase: core.PhaseResolver, ProviderID: "elbv2", Region: "us-east-1", StartedAt: now.Add(-4 * time.Second)},
		},
		activeOrder: []string{"k1", "k2"},
	}
	out := m.renderActiveRoster(200, now)
	outPlain := stripANSI(out)
	if !strings.Contains(outPlain, "[provider:elbv2/us-east-1") || !strings.Contains(outPlain, "[resolver:elbv2/us-east-1") {
		t.Fatalf("expected phase-prefixed labels, got %q", out)
	}
}

func TestViewAnchorsProgressLineAtBottom(t *testing.T) {
	m := &model{
		width:     80,
		height:    6,
		start:     time.Now().Add(-5 * time.Second),
		spin:      spinner.New(),
		progress:  progress.New(),
		total:     10,
		completed: 3,
		curEv: core.ScanProgressEvent{
			Phase:      core.PhaseProvider,
			ProviderID: "iam",
			Region:     "global",
			Message:    "listing",
		},
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "iam", Region: "global", StartedAt: time.Now().Add(-2 * time.Second)},
		},
		activeOrder: []string{"k1"},
	}
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("expected %d lines, got %d", m.height, len(lines))
	}
	if !strings.Contains(stripANSI(lines[len(lines)-2]), "Scanning") {
		t.Fatalf("second-last line is not progress headline: %q", lines[len(lines)-2])
	}
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "active:") {
		t.Fatalf("last line is not active roster line: %q", lines[len(lines)-1])
	}
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}
