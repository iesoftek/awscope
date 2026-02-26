package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"awscope/internal/core"
	"awscope/internal/providers/registry"
	"awscope/internal/tui/components/navigator"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type scanRunner interface {
	ScanWithProgress(ctx context.Context, opts core.ScanOptions, fn core.ScanProgressFn) (core.ScanResult, error)
}

type refreshScope struct {
	Service     string
	ProviderIDs []string
	Regions     []string
	Label       string
}

func waitRefreshMsgCmd(seq int, ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return refreshClosedMsg{seq: seq}
		}
		return msg
	}
}

func refreshStepKey(ev core.ScanProgressEvent) string {
	return strings.TrimSpace(fmt.Sprintf("%s|%s|%s", ev.Phase, ev.ProviderID, ev.Region))
}

func (m *model) refreshSpinnerGlyph() string {
	if m == nil {
		return "↻"
	}
	g := strings.TrimSpace(m.refreshSpinner.View())
	if g == "" {
		return "↻"
	}
	return g
}

func (m model) resolveRefreshService() string {
	if m.focus == focusServices {
		if row, ok := m.nav.SelectedRow(); ok {
			if row.Kind == navigator.RowService && strings.TrimSpace(row.Service) != "" {
				return strings.TrimSpace(row.Service)
			}
			if row.Kind == navigator.RowType && strings.TrimSpace(row.Service) != "" {
				return strings.TrimSpace(row.Service)
			}
		}
	}
	if svc := strings.TrimSpace(m.selectedService); svc != "" {
		return svc
	}
	if s, ok := m.selectedSummary(); ok {
		if svc := strings.TrimSpace(s.Service); svc != "" {
			return svc
		}
	}
	if sel, ok := m.activeSelection(); ok {
		return strings.TrimSpace(sel.Service)
	}
	return ""
}

func (m model) resolveRefreshRegions() []string {
	regions := m.selectedRegionSlice()
	out := make([]string, 0, len(regions))
	for _, r := range regions {
		r = strings.TrimSpace(r)
		if r == "" || strings.EqualFold(r, "global") {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		for _, r := range m.knownRegions {
			r = strings.TrimSpace(r)
			if r == "" || strings.EqualFold(r, "global") {
				continue
			}
			out = append(out, r)
		}
	}
	out = dedupeStrings(out)
	sort.Strings(out)
	return out
}

func (m *model) startRefreshForCurrentScopeCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	service := strings.TrimSpace(m.resolveRefreshService())
	if service == "" {
		m.statusLine = "no service selected for refresh"
		return nil
	}
	if _, ok := registry.Get(service); !ok {
		m.statusLine = fmt.Sprintf("unknown service %q for refresh", service)
		return nil
	}
	regions := m.resolveRefreshRegions()
	if len(regions) == 0 {
		m.statusLine = "no regional scope selected for refresh"
		return nil
	}
	scope := refreshScope{
		Service:     service,
		ProviderIDs: []string{service},
		Regions:     regions,
		Label:       fmt.Sprintf("%s [%s]", service, strings.Join(regions, ",")),
	}
	return m.startRefreshScanCmd(scope)
}

func (m *model) refreshScanOptions(scope refreshScope) core.ScanOptions {
	return core.ScanOptions{
		Profile:                      effectiveProfile(m.profileName),
		Regions:                      append([]string(nil), scope.Regions...),
		ProviderIDs:                  append([]string(nil), scope.ProviderIDs...),
		StalePolicy:                  core.StalePolicyHide,
		MaxConcurrency:               intEnvOrDefault("AWSCOPE_SCAN_CONCURRENCY", 16),
		ResolverConcurrency:          intEnvOrDefault("AWSCOPE_RESOLVER_CONCURRENCY", 8),
		AuditRegionConcurrency:       intEnvOrDefault("AWSCOPE_AUDIT_REGION_CONCURRENCY", 10),
		AuditSourceConcurrency:       intEnvOrDefault("AWSCOPE_AUDIT_SOURCE_CONCURRENCY", 3),
		AuditLookupInterval:          time.Duration(intEnvOrDefault("AWSCOPE_AUDIT_LOOKUP_INTERVAL_MS", 0)) * time.Millisecond,
		ELBv2TargetHealthConcurrency: intEnvOrDefault("AWSCOPE_ELBV2_TARGETHEALTH_CONCURRENCY", 30),
		CostConcurrency:              intEnvOrDefault("AWSCOPE_COST_CONCURRENCY", 16),
		TargetDuration:               time.Duration(intEnvOrDefault("AWSCOPE_SCAN_TARGET_SECONDS", 60)) * time.Second,
	}
}

func intEnvOrDefault(key string, fallback int) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

func (m *model) startRefreshScanCmd(scope refreshScope) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.refreshActive {
		m.statusLine = "refresh already running"
		return nil
	}
	if m.scanner == nil {
		m.scanner = core.New(m.st)
	}

	seq := m.refreshSeq + 1
	m.refreshSeq = seq
	runCtx, cancel := context.WithCancel(m.ctx)
	ch := make(chan tea.Msg, 512)
	scanner := m.scanner
	opts := m.refreshScanOptions(scope)

	m.refreshActive = true
	m.refreshScope = scope
	m.refreshStartedAt = time.Now()
	m.refreshCancel = cancel
	m.refreshCh = ch
	m.refreshCurrent = core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: scope.Service,
		Region:     strings.Join(scope.Regions, ","),
		Message:    "starting",
	}
	m.refreshTotal = 0
	m.refreshCompleted = 0
	m.refreshResSoFar = 0
	m.refreshEdgesSoFar = 0
	m.refreshStepFailures = 0
	m.refreshActiveStepKeys = map[string]string{}
	m.refreshFailureStepKeys = map[string]bool{}
	m.refreshBusyServiceCounts = map[string]int{scope.Service: 1}
	m.refreshBusyServices = map[string]bool{scope.Service: true}
	m.statusLine = "refresh started: " + scope.Label
	m.nav.SetBusyServices(m.refreshBusyServices, m.refreshSpinnerGlyph())
	m.syncResourceHeight()

	go func() {
		defer close(ch)
		res, err := scanner.ScanWithProgress(runCtx, opts, func(ev core.ScanProgressEvent) {
			select {
			case ch <- refreshProgressMsg{seq: seq, ev: ev}:
			default:
			}
		})
		ch <- refreshDoneMsg{seq: seq, res: res, err: err}
	}()

	return tea.Batch(waitRefreshMsgCmd(seq, ch), m.refreshSpinner.Tick)
}

func (m *model) stopRefreshScan() {
	if m == nil {
		return
	}
	if m.refreshCancel != nil {
		m.refreshCancel()
		m.refreshCancel = nil
	}
	m.refreshCh = nil
}

func (m *model) clearRefreshBusy() {
	m.refreshBusyServiceCounts = map[string]int{}
	m.refreshBusyServices = map[string]bool{}
	m.refreshActiveStepKeys = map[string]string{}
	m.refreshFailureStepKeys = map[string]bool{}
	m.nav.SetBusyServices(nil, "")
}

func (m *model) updateRefreshBusyFromEvent(ev core.ScanProgressEvent) {
	if m == nil {
		return
	}
	if m.refreshActiveStepKeys == nil {
		m.refreshActiveStepKeys = map[string]string{}
	}
	if m.refreshBusyServiceCounts == nil {
		m.refreshBusyServiceCounts = map[string]int{}
	}
	key := refreshStepKey(ev)
	if key == "" {
		return
	}
	if strings.TrimSpace(ev.Message) == "done" {
		if providerID, ok := m.refreshActiveStepKeys[key]; ok {
			delete(m.refreshActiveStepKeys, key)
			if n := m.refreshBusyServiceCounts[providerID] - 1; n > 0 {
				m.refreshBusyServiceCounts[providerID] = n
			} else {
				delete(m.refreshBusyServiceCounts, providerID)
			}
		}
	} else {
		if _, ok := m.refreshActiveStepKeys[key]; !ok {
			providerID := strings.TrimSpace(ev.ProviderID)
			if providerID == "" {
				providerID = strings.TrimSpace(m.refreshScope.Service)
			}
			m.refreshActiveStepKeys[key] = providerID
			m.refreshBusyServiceCounts[providerID] = m.refreshBusyServiceCounts[providerID] + 1
		}
	}

	busy := map[string]bool{}
	for svc, n := range m.refreshBusyServiceCounts {
		if strings.TrimSpace(svc) != "" && n > 0 {
			busy[svc] = true
		}
	}
	if len(busy) == 0 && m.refreshActive && strings.TrimSpace(m.refreshScope.Service) != "" {
		busy[m.refreshScope.Service] = true
	}
	m.refreshBusyServices = busy
	m.nav.SetBusyServices(m.refreshBusyServices, m.refreshSpinnerGlyph())
}

func (m *model) loadAllAfterRefreshCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	var cmds []tea.Cmd
	m.loading = true
	m.err = nil
	if !m.offline {
		m.identityLoading = true
		m.identityErr = nil
		cmds = append(cmds, m.loadIdentityCmd())
	}
	cmds = append(cmds,
		m.loadRegionsCmd(),
		m.loadRegionCountsCmd(),
		m.loadServiceCountsCmd(),
		m.loadTypeCountsCmd(m.nav.ExpandedService()),
		m.loadResourcesCmd(),
		m.loadNeighborsCmd(),
	)
	if m.pricingMode {
		cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
	}
	if m.auditOpen {
		m.auditLoading = true
		m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
	}
	return tea.Batch(cmds...)
}

func (m *model) finishRefreshState() {
	if m == nil {
		return
	}
	m.refreshActive = false
	m.refreshCurrent = core.ScanProgressEvent{}
	m.stopRefreshScan()
	m.clearRefreshBusy()
	m.syncResourceHeight()
}

func (m *model) handleRefreshMessage(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	switch rm := msg.(type) {
	case refreshProgressMsg:
		if rm.seq != m.refreshSeq {
			return nil
		}
		m.refreshCurrent = rm.ev
		m.refreshTotal = rm.ev.TotalSteps
		m.refreshCompleted = rm.ev.CompletedSteps
		m.refreshResSoFar = rm.ev.ResourcesSoFar
		m.refreshEdgesSoFar = rm.ev.EdgesSoFar
		if strings.TrimSpace(rm.ev.Message) == "done" && strings.TrimSpace(rm.ev.StepError) != "" {
			k := refreshStepKey(rm.ev)
			if k != "" {
				if m.refreshFailureStepKeys == nil {
					m.refreshFailureStepKeys = map[string]bool{}
				}
				if !m.refreshFailureStepKeys[k] {
					m.refreshFailureStepKeys[k] = true
					m.refreshStepFailures++
				}
			}
		}
		m.updateRefreshBusyFromEvent(rm.ev)
		return waitRefreshMsgCmd(rm.seq, m.refreshCh)

	case refreshDoneMsg:
		if rm.seq != m.refreshSeq {
			return nil
		}
		elapsed := time.Since(m.refreshStartedAt).Round(time.Second)
		m.finishRefreshState()
		if rm.err != nil {
			if rm.err == context.Canceled {
				m.statusLine = "refresh cancelled"
			} else {
				m.statusLine = "refresh failed: " + rm.err.Error()
			}
			return nil
		}

		warnCount := len(rm.res.StepFailures)
		if warnCount > 0 {
			m.statusLine = fmt.Sprintf("refreshed %s in %s (+res=%d +edges=%d, %d step warnings)", m.refreshScope.Service, elapsed, rm.res.Resources, rm.res.Edges, warnCount)
		} else {
			m.statusLine = fmt.Sprintf("refreshed %s in %s (+res=%d +edges=%d)", m.refreshScope.Service, elapsed, rm.res.Resources, rm.res.Edges)
		}
		return m.loadAllAfterRefreshCmd()

	case refreshClosedMsg:
		if rm.seq != m.refreshSeq {
			return nil
		}
		if m.refreshActive {
			m.finishRefreshState()
			m.statusLine = "refresh stream closed unexpectedly"
		}
	}
	return nil
}

func (m model) refreshStripVisible() bool {
	if !m.refreshActive {
		return false
	}
	if m.actionStreamOpen || m.auditOpen || m.showHelp {
		return false
	}
	if m.focus == focusRegions || m.focus == focusActions || m.focus == focusConfirm {
		return false
	}
	if m.graphMode {
		return false
	}
	return true
}

func (m *model) syncResourceHeight() {
	if m == nil || m.height <= 0 {
		return
	}
	target := m.paneInnerHeightBudget()
	if m.refreshStripVisible() {
		target = max(4, target-1)
	}
	if m.resources.Height() != target {
		m.resources.SetHeight(target)
	}
}

func (m model) renderRefreshProgressStrip(width int) string {
	width = max(20, width)
	spin := m.refreshSpinnerGlyph()

	scope := strings.TrimSpace(m.refreshScope.Service)
	if scope == "" {
		scope = "refresh"
	}
	step := strings.TrimSpace(fmt.Sprintf("%s/%s/%s", m.refreshCurrent.Phase, m.refreshCurrent.ProviderID, m.refreshCurrent.Region))
	if step == "//" || step == "" {
		step = "starting"
	}
	elapsed := time.Since(m.refreshStartedAt).Round(time.Second)
	left := fmt.Sprintf("%s %s %s %d/%d +res=%d +edges=%d %s", spin, scope, step, m.refreshCompleted, m.refreshTotal, m.refreshResSoFar, m.refreshEdgesSoFar, elapsed)
	if m.refreshStepFailures > 0 {
		left += fmt.Sprintf(" warn=%d", m.refreshStepFailures)
	}

	percent := 0.0
	if m.refreshTotal > 0 {
		percent = float64(m.refreshCompleted) / float64(m.refreshTotal)
	}
	barModel := m.refreshProgress
	barW := min(24, max(8, width/5))
	barModel.Width = barW
	bar := barModel.ViewAs(percent)

	leftW := max(0, width-lipgloss.Width(bar)-1)
	left = ansi.Truncate(left, leftW, "…")
	gap := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(bar)))
	return m.styles.Dim.Render(left) + gap + bar
}
