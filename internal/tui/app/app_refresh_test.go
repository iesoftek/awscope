package app

import (
	"strings"
	"testing"
	"time"

	"awscope/internal/core"
	"awscope/internal/tui/components/navigator"
	"awscope/internal/tui/widgets/table"

	"github.com/charmbracelet/bubbles/progress"
)

func TestResolveRefreshServiceFromNavigatorServiceRow(t *testing.T) {
	nav := navigator.New([]string{"ec2", "s3"}, func(string) []string { return nil })
	nav.SetSelection("ec2", "")

	m := model{
		focus:           focusServices,
		nav:             nav,
		selectedService: "s3",
	}

	if got := m.resolveRefreshService(); got != "ec2" {
		t.Fatalf("resolveRefreshService() = %q, want ec2", got)
	}
}

func TestResolveRefreshRegionsExcludesGlobalAndFallsBack(t *testing.T) {
	m := model{
		selectedService: "ec2",
		selectedRegions: map[string]bool{
			"global":    true,
			"us-west-2": true,
			"us-east-1": false,
		},
		knownRegions: []string{"global", "us-east-1", "us-west-2"},
	}

	got := m.resolveRefreshRegions()
	if len(got) != 1 || got[0] != "us-west-2" {
		t.Fatalf("resolveRefreshRegions() = %v, want [us-west-2]", got)
	}

	m.selectedRegions = map[string]bool{"global": true}
	got = m.resolveRefreshRegions()
	if len(got) != 2 || got[0] != "us-east-1" || got[1] != "us-west-2" {
		t.Fatalf("resolveRefreshRegions() fallback = %v, want [us-east-1 us-west-2]", got)
	}
}

func TestSyncResourceHeightAdjustsForRefreshStrip(t *testing.T) {
	m := model{
		height:        40,
		resources:     table.New(table.WithColumns(nil), table.WithRows(nil)),
		focus:         focusResources,
		refreshActive: true,
	}

	m.syncResourceHeight()
	if got := m.resources.Height(); got != 28 {
		t.Fatalf("height with refresh strip = %d, want 28", got)
	}

	m.refreshActive = false
	m.syncResourceHeight()
	if got := m.resources.Height(); got != 29 {
		t.Fatalf("height without refresh strip = %d, want 29", got)
	}
}

func TestRenderRefreshProgressStripIncludesScopeAndProgress(t *testing.T) {
	m := model{
		refreshScope:      refreshScope{Service: "ec2"},
		refreshCurrent:    core.ScanProgressEvent{Phase: core.PhaseProvider, ProviderID: "ec2", Region: "us-west-2"},
		refreshStartedAt:  time.Now().Add(-5 * time.Second),
		refreshCompleted:  3,
		refreshTotal:      10,
		refreshResSoFar:   25,
		refreshEdgesSoFar: 12,
		refreshProgress:   progress.New(progress.WithDefaultGradient()),
	}

	line := m.renderRefreshProgressStrip(80)
	if !strings.Contains(line, "ec2") {
		t.Fatalf("progress strip missing scope: %q", line)
	}
	if !strings.Contains(line, "3/10") {
		t.Fatalf("progress strip missing completed/total: %q", line)
	}
	if !strings.Contains(line, "+res=25") {
		t.Fatalf("progress strip missing resource delta: %q", line)
	}
}
