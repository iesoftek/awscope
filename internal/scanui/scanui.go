package scanui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"awscope/internal/core"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
)

type Options struct {
	Profile     string
	Regions     []string
	ProviderIDs []string
	StalePolicy core.StalePolicy

	MaxConcurrency               int
	ResolverConcurrency          int
	AuditRegionConcurrency       int
	AuditSourceConcurrency       int
	AuditLookupInterval          time.Duration
	ELBv2TargetHealthConcurrency int
	CostConcurrency              int
	TargetDuration               time.Duration
}

type progressMsg struct {
	ev core.ScanProgressEvent
}

type doneMsg struct {
	res core.ScanResult
	err error
}

type stepState string

const (
	stepPending stepState = "pending"
	stepRunning stepState = "running"
	stepDone    stepState = "done"
	stepError   stepState = "error"
)

type activeStep struct {
	Key        string
	Phase      core.ScanProgressPhase
	ProviderID string
	Region     string
	Message    string
	StartedAt  time.Time
	UpdatedAt  time.Time
}

type stepItem struct {
	Key                string
	Phase              core.ScanProgressPhase
	ProviderID         string
	Region             string
	State              stepState
	Message            string
	StartedAt          time.Time
	EndedAt            time.Time
	StepResourcesAdded int
	StepEdgesAdded     int
	StepError          string
	TypeCounts         map[string]int
	SampleLabel        string
	SampleTotal        int
	SampleItems        []string
}

type checklistProviderGroup struct {
	ProviderID string
	Items      []*stepItem
	Expected   int
	Done       int
	Errors     int
	Running    int
}

type checklistGroup struct {
	ID        string
	Title     string
	Phase     core.ScanProgressPhase
	Providers []checklistProviderGroup
	Expected  int
	Done      int
	Errors    int
	Running   int
}

type collapsedSummary struct {
	Steps     int
	Resources int
	Edges     int
	Errors    int
}

type checklistRenderRow struct {
	ID    string
	Line  string
	Level int
}

type model struct {
	ctx context.Context
	app *core.App

	opts Options

	progressCh <-chan core.ScanProgressEvent
	doneCh     <-chan doneMsg

	spin     spinner.Model
	progress progress.Model

	width  int
	height int

	curEv       core.ScanProgressEvent
	total       int
	completed   int
	resSoFar    int
	edgesSoFar  int
	lastPrinted int

	doneLines      []string
	active         map[string]activeStep
	activeOrder    []string
	marqueeOffset  int
	checklistItems map[string]*stepItem
	checklistOrder []string
	groups         []checklistGroup
	expectedKeys   map[string]struct{}
	collapsed      map[string]bool
	manualOverride map[string]bool
	selectedRowID  string
	rowOrder       []string

	start time.Time
	done  bool
	err   error
	res   core.ScanResult
}

func Run(ctx context.Context, st *store.Store, opts Options) (core.ScanResult, error) {
	// Bubble Tea scan UI requires a TTY. If we don't have one, fall back to plain scan.
	if !isTTY() {
		// Keep a compact progress log for non-TTY usage (e.g. piping output).
		// Important counts are tab-separated to keep columns aligned even when labels vary.
		lg := log.New(os.Stdout)
		lg.SetReportTimestamp(false)
		lg.SetReportCaller(false)
		lg.SetPrefix("")
		lg.SetFormatter(log.TextFormatter)

		app := core.New(st)
		res, err := app.ScanWithProgress(ctx, core.ScanOptions{
			Profile:                      opts.Profile,
			Regions:                      opts.Regions,
			ProviderIDs:                  opts.ProviderIDs,
			StalePolicy:                  opts.StalePolicy,
			MaxConcurrency:               opts.MaxConcurrency,
			ResolverConcurrency:          opts.ResolverConcurrency,
			AuditRegionConcurrency:       opts.AuditRegionConcurrency,
			AuditSourceConcurrency:       opts.AuditSourceConcurrency,
			AuditLookupInterval:          opts.AuditLookupInterval,
			ELBv2TargetHealthConcurrency: opts.ELBv2TargetHealthConcurrency,
			CostConcurrency:              opts.CostConcurrency,
			TargetDuration:               opts.TargetDuration,
		}, func(ev core.ScanProgressEvent) {
			if ev.Message != "done" {
				return
			}
			lg.Print(formatDoneLinePlain(ev))
		})
		if err == nil && len(res.StepFailures) > 0 {
			lg.Print(formatFailureSummaryPlain(res.StepFailures))
		}
		return res, err
	}

	app := core.New(st)

	spin := spinner.New()
	spin.Spinner = spinner.Line

	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(40),
		progress.WithoutPercentage(),
	)

	m := &model{
		ctx:            ctx,
		app:            app,
		opts:           opts,
		spin:           spin,
		progress:       p,
		start:          time.Now(),
		active:         map[string]activeStep{},
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	m.seedExpectedChecklist()

	// Intentionally do not use AltScreen here: we use tea.Printf to show per-step lines,
	// matching the Bubble Tea "package-manager" example style.
	prog := tea.NewProgram(m, tea.WithContext(ctx))
	fin, err := prog.Run()
	if err != nil {
		return core.ScanResult{}, err
	}
	m2 := fin.(*model)
	if m2.err != nil {
		return core.ScanResult{}, m2.err
	}
	return m2.res, nil
}

func (m *model) Init() tea.Cmd {
	progressCh := make(chan core.ScanProgressEvent, 256)
	doneCh := make(chan doneMsg, 1)

	m.progressCh = progressCh
	m.doneCh = doneCh

	go func() {
		defer close(progressCh)
		res, err := m.app.ScanWithProgress(m.ctx, core.ScanOptions{
			Profile:                      m.opts.Profile,
			Regions:                      m.opts.Regions,
			ProviderIDs:                  m.opts.ProviderIDs,
			StalePolicy:                  m.opts.StalePolicy,
			MaxConcurrency:               m.opts.MaxConcurrency,
			ResolverConcurrency:          m.opts.ResolverConcurrency,
			AuditRegionConcurrency:       m.opts.AuditRegionConcurrency,
			AuditSourceConcurrency:       m.opts.AuditSourceConcurrency,
			AuditLookupInterval:          m.opts.AuditLookupInterval,
			ELBv2TargetHealthConcurrency: m.opts.ELBv2TargetHealthConcurrency,
			CostConcurrency:              m.opts.CostConcurrency,
			TargetDuration:               m.opts.TargetDuration,
		}, func(ev core.ScanProgressEvent) {
			select {
			case progressCh <- ev:
			default:
				// Drop if UI can't keep up; later events will catch state up.
			}
		})
		doneCh <- doneMsg{res: res, err: err}
	}()

	return tea.Batch(m.spin.Tick, m.listen())
}

func (m *model) listen() tea.Cmd {
	return func() tea.Msg {
		// Prefer progress updates; fall back to done if progress channel is closed.
		if m.progressCh != nil {
			if ev, ok := <-m.progressCh; ok {
				return progressMsg{ev: ev}
			}
			m.progressCh = nil
		}
		if m.doneCh != nil {
			d, ok := <-m.doneCh
			if !ok {
				return doneMsg{}
			}
			return d
		}
		return doneMsg{}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "down", "j":
			m.moveSelection(+1)
		case "up", "k":
			m.moveSelection(-1)
		case "enter", " ":
			m.toggleSelectedCollapse()
		case "q", "ctrl+c", "esc":
			m.err = fmt.Errorf("scan cancelled")
			return m, tea.Quit
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)

	case progressMsg:
		ev := msg.ev
		m.total = ev.TotalSteps
		m.completed = ev.CompletedSteps
		m.resSoFar = ev.ResourcesSoFar
		m.edgesSoFar = ev.EdgesSoFar
		m.curEv = ev
		m.trackProgressEvent(ev, time.Now())
		if m.total > 0 {
			cmds = append(cmds, m.progress.SetPercent(float64(m.completed)/float64(m.total)))
		}
		if ev.Message == "done" && m.completed > m.lastPrinted {
			m.lastPrinted = m.completed
			m.doneLines = append(m.doneLines, formatDoneLineTTY(ev))
		}

	case doneMsg:
		m.err = msg.err
		m.res = msg.res
		m.done = true
		if msg.err != nil {
			return m, tea.Sequence(
				tea.Printf("%s %s", crossMark, msg.err.Error()),
				tea.Quit,
			)
		}
		m.doneLines = append(m.doneLines, formatScanCompleteLineTTY(msg.res))
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case progress.FrameMsg:
		newModel, cmd := m.progress.Update(msg)
		if pm, ok := newModel.(progress.Model); ok {
			m.progress = pm
		}
		cmds = append(cmds, cmd)
	}

	cmds = append(cmds, m.listen())
	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	elapsed := time.Since(m.start).Round(time.Second)

	if m.done {
		// Render the last few lines for context; full log would be noisy after exit.
		var out []string
		if n := len(m.doneLines); n > 0 {
			tail := n
			if tail > 12 {
				tail = 12
			}
			out = append(out, m.doneLines[n-tail:]...)
		}
		if len(m.res.StepFailures) > 0 {
			out = append(out, formatFailureSummaryTTY(m.res.StepFailures))
		}
		out = append(out, fmt.Sprintf("Done! resources=%d edges=%d (%s)", m.res.Resources, m.res.Edges, elapsed))
		return doneStyle.Render(strings.Join(out, "\n") + "\n")
	}

	w := lipgloss.Width(fmt.Sprintf("%d", m.total))
	stepCount := fmt.Sprintf(" %*d/%*d", w, m.completed, w, m.total)

	spin := m.spin.View() + " "
	prog := m.progress.View()

	phase := string(m.curEv.Phase)
	if phase == "" {
		phase = "scan"
	}
	label := strings.TrimSpace(fmt.Sprintf("%s %s %s", phase, m.curEv.ProviderID, m.curEv.Region))
	if label == "" {
		label = "starting"
	}
	inProgress, pCount, rCount, aCount, cCount := m.activePhaseCounts()
	cellsAvail := max(0, m.width-lipgloss.Width(spin+prog+stepCount))
	activeSummary := fmt.Sprintf("in-progress=%2d (%s:%2d %s:%2d %s:%2d %s:%2d)",
		inProgress,
		phaseProviderStyle.Render("p"), pCount,
		phaseResolverStyle.Render("r"), rCount,
		phaseAuditStyle.Render("a"), aCount,
		phaseCostStyle.Render("c"), cCount,
	)
	stepLabel := fitCell(label, 28)
	stepMsg := fitCell(m.curEv.Message, 32)
	headline := fmt.Sprintf("Scanning %s: %s | %6s | %s", currentStepStyle.Render(stepLabel), stepMsg, elapsed.String(), activeSummary)
	topInfo := lipgloss.NewStyle().MaxWidth(cellsAvail).Render(headline)
	gap := strings.Repeat(" ", max(0, m.width-lipgloss.Width(spin+topInfo+prog+stepCount)))
	topLine := spin + topInfo + gap + prog + stepCount

	secondPrefix := "  active: "
	secondAvail := max(0, m.width-lipgloss.Width(secondPrefix))
	roster := m.renderActiveRoster(secondAvail, time.Now())
	if roster == "" {
		roster = activeMoreStyle.Render("[none]")
	}
	bottomLine := secondPrefix + lipgloss.NewStyle().MaxWidth(secondAvail).Render(roster)

	// Render checklist body above sticky progress lines.
	stickyCount := 2
	if m.height == 1 {
		stickyCount = 1
	}
	bodyAvail := 0
	if m.height > 0 {
		bodyAvail = m.height - stickyCount
		if bodyAvail < 0 {
			bodyAvail = 0
		}
	}
	lines := m.renderChecklistBody(max(0, m.width), bodyAvail, time.Now())
	lines = append(lines, topLine)
	if stickyCount == 2 {
		lines = append(lines, bottomLine)
	}
	if m.height > 0 && len(lines) < m.height {
		pad := make([]string, m.height-len(lines))
		lines = append(pad, lines...)
	}
	return strings.Join(lines, "\n")
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func stepKey(ev core.ScanProgressEvent) string {
	return strings.TrimSpace(fmt.Sprintf("%s|%s|%s", ev.Phase, ev.ProviderID, ev.Region))
}

func makeStepKey(phase core.ScanProgressPhase, providerID, region string) string {
	return strings.TrimSpace(fmt.Sprintf("%s|%s|%s", phase, providerID, region))
}

func (m *model) seedExpectedChecklist() {
	if m.checklistItems == nil {
		m.checklistItems = map[string]*stepItem{}
	}
	if m.expectedKeys == nil {
		m.expectedKeys = map[string]struct{}{}
	}

	add := func(phase core.ScanProgressPhase, providerID, region string) {
		key := makeStepKey(phase, providerID, region)
		if key == "" {
			return
		}
		if _, ok := m.expectedKeys[key]; !ok {
			m.expectedKeys[key] = struct{}{}
		}
		if _, ok := m.checklistItems[key]; ok {
			return
		}
		m.checklistItems[key] = &stepItem{
			Key:        key,
			Phase:      phase,
			ProviderID: strings.TrimSpace(providerID),
			Region:     strings.TrimSpace(region),
			State:      stepPending,
		}
		m.checklistOrder = append(m.checklistOrder, key)
	}

	auditRegions := make([]string, 0, len(m.opts.Regions))
	regionSet := map[string]struct{}{}
	for _, r := range m.opts.Regions {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		regionSet[r] = struct{}{}
		if !strings.EqualFold(r, "global") {
			auditRegions = append(auditRegions, r)
		}
	}

	needsResolver := hasProviderID(m.opts.ProviderIDs, "ec2") && hasProviderID(m.opts.ProviderIDs, "elbv2")
	needsAudit := hasProviderID(m.opts.ProviderIDs, "cloudtrail") && len(auditRegions) > 0

	for _, pid := range m.opts.ProviderIDs {
		scope := providers.ScopeRegional
		if p, ok := registry.Get(pid); ok {
			scope = p.Scope()
		}
		switch scope {
		case providers.ScopeGlobal:
			add(core.PhaseProvider, pid, "global")
		case providers.ScopeAccount:
			add(core.PhaseProvider, pid, "account")
		default:
			for _, r := range m.opts.Regions {
				add(core.PhaseProvider, pid, r)
			}
		}
		add(core.PhaseCost, pid, "all")
	}
	if needsResolver {
		for r := range regionSet {
			add(core.PhaseResolver, "elbv2", r)
		}
	}
	if needsAudit {
		for _, r := range auditRegions {
			add(core.PhaseAudit, "cloudtrail", r)
		}
	}
}

func (m *model) trackProgressEvent(ev core.ScanProgressEvent, now time.Time) {
	if m.active == nil {
		m.active = map[string]activeStep{}
	}
	if m.checklistItems == nil {
		m.checklistItems = map[string]*stepItem{}
	}
	key := stepKey(ev)
	if key == "" {
		return
	}
	it, ok := m.checklistItems[key]
	if !ok {
		it = &stepItem{
			Key:        key,
			Phase:      ev.Phase,
			ProviderID: strings.TrimSpace(ev.ProviderID),
			Region:     strings.TrimSpace(ev.Region),
			State:      stepPending,
		}
		m.checklistItems[key] = it
		m.checklistOrder = append(m.checklistOrder, key)
	}

	if strings.TrimSpace(ev.Message) == "done" {
		delete(m.active, key)
		if strings.TrimSpace(ev.StepError) != "" {
			it.State = stepError
			it.StepError = strings.TrimSpace(ev.StepError)
			m.clearCollapseOverrideForStep(it)
		} else {
			it.State = stepDone
			it.StepError = ""
		}
		it.Message = strings.TrimSpace(ev.Message)
		if it.StartedAt.IsZero() {
			it.StartedAt = now
		}
		it.EndedAt = now
		it.StepResourcesAdded = ev.StepResourcesAdded
		it.StepEdgesAdded = ev.StepEdgesAdded
		it.SampleLabel = ev.StepSampleLabel
		it.SampleTotal = ev.StepSampleTotal
		if len(ev.StepSampleItems) > 0 {
			it.SampleItems = append([]string{}, ev.StepSampleItems...)
		} else {
			it.SampleItems = nil
		}
		if len(ev.StepTypeCounts) > 0 {
			it.TypeCounts = map[string]int{}
			for k, v := range ev.StepTypeCounts {
				it.TypeCounts[k] = v
			}
		} else {
			it.TypeCounts = nil
		}
		return
	}

	if it.State == stepPending {
		it.State = stepRunning
		if it.StartedAt.IsZero() {
			it.StartedAt = now
		}
		m.clearCollapseOverrideForStep(it)
	}
	if it.State == stepDone || it.State == stepError {
		it.State = stepRunning
		m.clearCollapseOverrideForStep(it)
	}
	it.Message = strings.TrimSpace(ev.Message)
	it.EndedAt = time.Time{}
	if len(ev.StepTypeCounts) > 0 {
		it.TypeCounts = map[string]int{}
		for k, v := range ev.StepTypeCounts {
			it.TypeCounts[k] = v
		}
	}
	if ev.StepSampleLabel != "" {
		it.SampleLabel = ev.StepSampleLabel
		it.SampleTotal = ev.StepSampleTotal
		it.SampleItems = append([]string{}, ev.StepSampleItems...)
	}

	if cur, ok := m.active[key]; ok {
		cur.Message = ev.Message
		cur.UpdatedAt = now
		m.active[key] = cur
		return
	}
	m.activeOrder = append(m.activeOrder, key)
	m.active[key] = activeStep{
		Key:        key,
		Phase:      ev.Phase,
		ProviderID: ev.ProviderID,
		Region:     ev.Region,
		Message:    ev.Message,
		StartedAt:  now,
		UpdatedAt:  now,
	}
}

func (m *model) compactActiveOrder() {
	if len(m.activeOrder) == 0 {
		return
	}
	compacted := make([]string, 0, len(m.activeOrder))
	for _, k := range m.activeOrder {
		if _, ok := m.active[k]; !ok {
			continue
		}
		compacted = append(compacted, k)
	}
	m.activeOrder = compacted
}

func (m *model) activePhaseCounts() (total, providers, resolvers, audits, costs int) {
	for _, st := range m.active {
		total++
		switch st.Phase {
		case core.PhaseProvider:
			providers++
		case core.PhaseResolver:
			resolvers++
		case core.PhaseAudit:
			audits++
		case core.PhaseCost:
			costs++
		}
	}
	return total, providers, resolvers, audits, costs
}

func (m *model) rebuildChecklistGroups() []checklistGroup {
	phaseOrder := []core.ScanProgressPhase{
		core.PhaseProvider,
		core.PhaseResolver,
		core.PhaseAudit,
		core.PhaseCost,
	}

	groups := make([]checklistGroup, 0, len(phaseOrder))
	for _, phase := range phaseOrder {
		providersOrder := make([]string, 0)
		providersMap := map[string]*checklistProviderGroup{}

		for _, key := range m.checklistOrder {
			it, ok := m.checklistItems[key]
			if !ok || it == nil || it.Phase != phase {
				continue
			}
			pid := strings.TrimSpace(it.ProviderID)
			if pid == "" {
				pid = "-"
			}
			pg, ok := providersMap[pid]
			if !ok {
				pg = &checklistProviderGroup{ProviderID: pid}
				providersMap[pid] = pg
				providersOrder = append(providersOrder, pid)
			}
			pg.Items = append(pg.Items, it)
			if _, ok := m.expectedKeys[it.Key]; ok {
				pg.Expected++
			}
			switch it.State {
			case stepDone:
				pg.Done++
			case stepError:
				pg.Errors++
			case stepRunning:
				pg.Running++
			}
		}

		if len(providersOrder) == 0 {
			continue
		}

		g := checklistGroup{
			ID:    string(phase),
			Title: string(phase),
			Phase: phase,
		}
		for _, pid := range providersOrder {
			pg := providersMap[pid]
			if pg == nil {
				continue
			}
			g.Providers = append(g.Providers, *pg)
			g.Expected += pg.Expected
			g.Done += pg.Done
			g.Errors += pg.Errors
			g.Running += pg.Running
		}
		groups = append(groups, g)
	}
	return groups
}

func (m *model) renderChecklistBody(width, height int, now time.Time) []string {
	if height <= 0 || width <= 0 {
		return nil
	}
	groups := m.rebuildChecklistGroups()
	m.groups = groups
	autoCollapsed := m.computeAutoCollapsed(groups)
	effectiveCollapsed := m.effectiveCollapsed(autoCollapsed)
	rows, collapsedCount := m.flattenRows(groups, effectiveCollapsed, now)
	m.ensureSelectedRow()

	totalExpected := 0
	totalDone := 0
	totalRunning := 0
	totalErrors := 0
	for _, g := range groups {
		totalExpected += g.Expected
		totalDone += g.Done
		totalRunning += g.Running
		totalErrors += g.Errors
	}

	lines := make([]string, 0, height+8)
	header := fmt.Sprintf("%s done=%d/%d running=%d errors=%d collapsed=%d",
		headerStyle.Render("Scan Tasks"), totalDone, totalExpected, totalRunning, totalErrors, collapsedCount)
	lines = append(lines, lipgloss.NewStyle().MaxWidth(width).Render(header))

	for _, row := range rows {
		prefix := "  "
		if row.ID != "" && row.ID == m.selectedRowID {
			prefix = selectedMarkStyle.Render("› ")
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width).Render(prefix+row.Line))
	}

	if len(lines) <= height {
		return lines
	}
	if height == 1 {
		return []string{fmt.Sprintf("... (+%d more)", len(lines)-1)}
	}
	keep := height - 1
	return append(lines[:keep], fmt.Sprintf("... (+%d more)", len(lines)-keep))
}

func phaseRowID(phase core.ScanProgressPhase) string {
	return "phase|" + string(phase)
}

func providerRowID(phase core.ScanProgressPhase, providerID string) string {
	return "provider|" + string(phase) + "|" + strings.TrimSpace(providerID)
}

func (m *model) providerAutoCollapsed(pg checklistProviderGroup) bool {
	if pg.Running > 0 || pg.Errors > 0 || len(pg.Items) == 0 {
		return false
	}
	for _, it := range pg.Items {
		if it == nil || it.State != stepDone {
			return false
		}
	}
	return true
}

func (m *model) computeAutoCollapsed(groups []checklistGroup) map[string]bool {
	auto := map[string]bool{}
	for _, g := range groups {
		phaseKey := phaseRowID(g.Phase)
		phaseCollapsed := len(g.Providers) > 0
		if g.Running > 0 || g.Errors > 0 {
			phaseCollapsed = false
		}
		for _, pg := range g.Providers {
			pKey := providerRowID(g.Phase, pg.ProviderID)
			pCollapsed := m.providerAutoCollapsed(pg)
			auto[pKey] = pCollapsed
			if !pCollapsed {
				phaseCollapsed = false
			}
		}
		auto[phaseKey] = phaseCollapsed
	}
	return auto
}

func (m *model) effectiveCollapsed(auto map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range auto {
		out[k] = v
	}
	if m.manualOverride != nil {
		for k, v := range m.manualOverride {
			out[k] = v
		}
	}
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed = out
	return out
}

func providerPendingCount(pg checklistProviderGroup) int {
	n := 0
	for _, it := range pg.Items {
		if it != nil && it.State == stepPending {
			n++
		}
	}
	if n == 0 && pg.Expected > 0 {
		left := pg.Expected - (pg.Done + pg.Running + pg.Errors)
		if left > 0 {
			n = left
		}
	}
	return n
}

func orderProvidersByState(g checklistGroup, collapsed map[string]bool) []checklistProviderGroup {
	activeExpanded := make([]checklistProviderGroup, 0, len(g.Providers))
	pendingExpanded := make([]checklistProviderGroup, 0, len(g.Providers))
	doneExpanded := make([]checklistProviderGroup, 0, len(g.Providers))
	doneCollapsed := make([]checklistProviderGroup, 0, len(g.Providers))

	for _, pg := range g.Providers {
		pKey := providerRowID(g.Phase, pg.ProviderID)
		if collapsed[pKey] {
			doneCollapsed = append(doneCollapsed, pg)
			continue
		}
		if pg.Running > 0 || pg.Errors > 0 {
			activeExpanded = append(activeExpanded, pg)
			continue
		}
		if providerPendingCount(pg) > 0 {
			pendingExpanded = append(pendingExpanded, pg)
			continue
		}
		doneExpanded = append(doneExpanded, pg)
	}

	ordered := make([]checklistProviderGroup, 0, len(g.Providers))
	ordered = append(ordered, activeExpanded...)
	ordered = append(ordered, pendingExpanded...)
	ordered = append(ordered, doneExpanded...)
	ordered = append(ordered, doneCollapsed...)
	return ordered
}

func aggregateCollapsedProviderSummary(items []*stepItem) collapsedSummary {
	sum := collapsedSummary{Steps: len(items)}
	for _, it := range items {
		if it == nil {
			continue
		}
		sum.Resources += it.StepResourcesAdded
		sum.Edges += it.StepEdgesAdded
		if it.State == stepError {
			sum.Errors++
		}
	}
	return sum
}

type providerBuckets struct {
	running []*stepItem
	errors  []*stepItem
	pending []*stepItem
	done    []*stepItem
}

func splitProviderItems(items []*stepItem) providerBuckets {
	out := providerBuckets{
		running: make([]*stepItem, 0, len(items)),
		errors:  make([]*stepItem, 0, len(items)),
		pending: make([]*stepItem, 0, len(items)),
		done:    make([]*stepItem, 0, len(items)),
	}
	for _, it := range items {
		if it == nil {
			continue
		}
		switch it.State {
		case stepRunning:
			out.running = append(out.running, it)
		case stepError:
			out.errors = append(out.errors, it)
		case stepDone:
			out.done = append(out.done, it)
		default:
			out.pending = append(out.pending, it)
		}
	}
	return out
}

func summarizeRegions(items []*stepItem, limit int) string {
	if len(items) == 0 {
		return "-"
	}
	if limit <= 0 {
		limit = 3
	}
	regions := make([]string, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		r := strings.TrimSpace(it.Region)
		if r == "" {
			r = "-"
		}
		regions = append(regions, r)
	}
	if len(regions) == 0 {
		return "-"
	}
	sort.Strings(regions)
	if len(regions) <= limit {
		return strings.Join(regions, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(regions[:limit], ", "), len(regions)-limit)
}

func summarizeDoneTotals(items []*stepItem) (res int, edges int) {
	for _, it := range items {
		if it == nil {
			continue
		}
		res += it.StepResourcesAdded
		edges += it.StepEdgesAdded
	}
	return res, edges
}

func renderStepLine(it *stepItem, now time.Time, spinGlyph string) string {
	if it == nil {
		return ""
	}
	meta := ""
	switch it.State {
	case stepRunning:
		elapsed := now.Sub(it.StartedAt)
		if it.StartedAt.IsZero() || elapsed < 0 {
			elapsed = 0
		}
		meta = fmt.Sprintf("%s | %s", formatStepElapsed(elapsed), trimOneLine(it.Message, 48))
	case stepDone:
		meta = fmt.Sprintf("+res=%d +edges=%d", it.StepResourcesAdded, it.StepEdgesAdded)
	case stepError:
		meta = trimOneLine(it.StepError, 64)
	default:
		meta = trimOneLine(it.Message, 48)
	}
	region := strings.TrimSpace(it.Region)
	if region == "" {
		region = "-"
	}
	return fmt.Sprintf("    %s %s  %s", stepStateMark(it.State, spinGlyph), fitCell(region, 14), meta)
}

func (m *model) flattenRows(groups []checklistGroup, collapsed map[string]bool, now time.Time) ([]checklistRenderRow, int) {
	rows := make([]checklistRenderRow, 0, 256)
	rowOrder := make([]string, 0, 64)
	collapsedCount := 0
	spinGlyph := m.spin.View()

	for _, g := range groups {
		phaseKey := phaseRowID(g.Phase)
		phaseCollapsed := collapsed[phaseKey]
		if phaseCollapsed {
			collapsedCount++
		}
		phaseTwisty := "▾"
		if phaseCollapsed {
			phaseTwisty = "▸"
		}
		gLine := fmt.Sprintf("%s %s %s [%d/%d] (running=%d error=%d)",
			phaseTwisty,
			stepStateMark(aggregateState(g.Done, g.Expected, g.Running, g.Errors), spinGlyph),
			phaseStyle(g.Phase).Render(g.Title),
			g.Done, g.Expected, g.Running, g.Errors)
		rows = append(rows, checklistRenderRow{ID: phaseKey, Line: gLine, Level: 0})
		rowOrder = append(rowOrder, phaseKey)
		if phaseCollapsed {
			continue
		}

		for _, pg := range orderProvidersByState(g, collapsed) {
			pKey := providerRowID(g.Phase, pg.ProviderID)
			pCollapsed := collapsed[pKey]
			if pCollapsed {
				collapsedCount++
			}
			pTwisty := "▾"
			if pCollapsed {
				pTwisty = "▸"
			}
			pState := aggregateState(pg.Done, pg.Expected, pg.Running, pg.Errors)
			pLine := fmt.Sprintf("  %s %s %s [%d/%d]",
				pTwisty,
				stepStateMark(pState, spinGlyph),
				phaseStyle(g.Phase).Render(pg.ProviderID),
				pg.Done, pg.Expected)
			if pCollapsed {
				sum := aggregateCollapsedProviderSummary(pg.Items)
				pLine += fmt.Sprintf(" (%d steps, +res=%d, +edges=%d)", sum.Steps, sum.Resources, sum.Edges)
			}
			rows = append(rows, checklistRenderRow{ID: pKey, Line: pLine, Level: 1})
			rowOrder = append(rowOrder, pKey)
			if pCollapsed {
				continue
			}
			buckets := splitProviderItems(pg.Items)

			for _, it := range buckets.errors {
				rows = append(rows, checklistRenderRow{Line: renderStepLine(it, now, spinGlyph), Level: 2})
			}
			for _, it := range buckets.running {
				rows = append(rows, checklistRenderRow{Line: renderStepLine(it, now, spinGlyph), Level: 2})
				extra := formatStepExtraInline(it)
				if extra != "" {
					rows = append(rows, checklistRenderRow{Line: "      " + trimOneLine(extra, 120), Level: 3})
				}
			}

			if len(buckets.pending) > 0 {
				pendingLine := fmt.Sprintf("    %s pending regions=%d | %s",
					stepStateMark(stepPending, spinGlyph),
					len(buckets.pending),
					summarizeRegions(buckets.pending, 4),
				)
				rows = append(rows, checklistRenderRow{Line: pendingLine, Level: 2})
			}

			if len(buckets.done) > 0 {
				doneRes, doneEdges := summarizeDoneTotals(buckets.done)
				doneLine := fmt.Sprintf("    %s done regions=%d +res=%d +edges=%d | %s",
					stepStateMark(stepDone, spinGlyph),
					len(buckets.done),
					doneRes,
					doneEdges,
					summarizeRegions(buckets.done, 4),
				)
				rows = append(rows, checklistRenderRow{Line: doneLine, Level: 2})
			}
		}
	}

	m.rowOrder = rowOrder
	return rows, collapsedCount
}

func (m *model) ensureSelectedRow() {
	if len(m.rowOrder) == 0 {
		m.selectedRowID = ""
		return
	}
	if m.selectedRowID == "" {
		m.selectedRowID = m.rowOrder[0]
		return
	}
	for _, id := range m.rowOrder {
		if id == m.selectedRowID {
			return
		}
	}
	m.selectedRowID = m.rowOrder[0]
}

func (m *model) moveSelection(delta int) {
	if len(m.rowOrder) == 0 || delta == 0 {
		return
	}
	m.ensureSelectedRow()
	idx := 0
	for i, id := range m.rowOrder {
		if id == m.selectedRowID {
			idx = i
			break
		}
	}
	next := idx + delta
	if next < 0 {
		next = 0
	}
	if next >= len(m.rowOrder) {
		next = len(m.rowOrder) - 1
	}
	m.selectedRowID = m.rowOrder[next]
}

func (m *model) toggleSelectedCollapse() {
	if m.selectedRowID == "" {
		return
	}
	if m.manualOverride == nil {
		m.manualOverride = map[string]bool{}
	}
	cur := false
	if m.collapsed != nil {
		cur = m.collapsed[m.selectedRowID]
	}
	m.manualOverride[m.selectedRowID] = !cur
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed[m.selectedRowID] = !cur
}

func (m *model) clearCollapseOverrideForStep(it *stepItem) {
	if it == nil {
		return
	}
	pKey := providerRowID(it.Phase, it.ProviderID)
	phKey := phaseRowID(it.Phase)
	if m.manualOverride != nil {
		delete(m.manualOverride, pKey)
		delete(m.manualOverride, phKey)
	}
	if m.collapsed != nil {
		m.collapsed[pKey] = false
		m.collapsed[phKey] = false
	}
}

func aggregateState(done, expected, running, errors int) stepState {
	if errors > 0 {
		return stepError
	}
	if running > 0 {
		return stepRunning
	}
	if expected > 0 && done >= expected {
		return stepDone
	}
	return stepPending
}

func stepStateMark(state stepState, spinGlyph string) string {
	switch state {
	case stepDone:
		return checkMark.String()
	case stepError:
		return crossMark.String()
	case stepRunning:
		g := strings.TrimSpace(spinGlyph)
		if g == "" {
			g = "…"
		}
		return runningMarkStyle.Render(g)
	default:
		return pendingMarkStyle.Render("◌")
	}
}

func formatStepExtraInline(it *stepItem) string {
	if it == nil {
		return ""
	}
	if len(it.TypeCounts) > 0 {
		type kv struct {
			k string
			v int
		}
		xs := make([]kv, 0, len(it.TypeCounts))
		for k, v := range it.TypeCounts {
			xs = append(xs, kv{k: k, v: v})
		}
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].v != xs[j].v {
				return xs[i].v > xs[j].v
			}
			return xs[i].k < xs[j].k
		})
		limit := 3
		if len(xs) < limit {
			limit = len(xs)
		}
		parts := make([]string, 0, limit)
		for i := 0; i < limit; i++ {
			parts = append(parts, fmt.Sprintf("%s=%d", xs[i].k, xs[i].v))
		}
		return "types: " + strings.Join(parts, " ")
	}
	if it.SampleLabel != "" && it.SampleTotal > 0 {
		if len(it.SampleItems) == 0 {
			return fmt.Sprintf("sample: %s=%d", it.SampleLabel, it.SampleTotal)
		}
		return fmt.Sprintf("sample: %s=%d: %s", it.SampleLabel, it.SampleTotal, strings.Join(it.SampleItems, ", "))
	}
	return ""
}

func (m *model) renderActiveRoster(width int, now time.Time) string {
	if width <= 0 {
		return ""
	}
	m.compactActiveOrder()
	if len(m.activeOrder) == 0 {
		return ""
	}

	phaseByPR := map[string]int{}
	for _, k := range m.activeOrder {
		st, ok := m.active[k]
		if !ok {
			continue
		}
		pr := fmt.Sprintf("%s/%s", strings.TrimSpace(st.ProviderID), strings.TrimSpace(st.Region))
		phaseByPR[pr]++
	}

	tokens := make([]string, 0, len(m.activeOrder))
	for _, k := range m.activeOrder {
		st, ok := m.active[k]
		if !ok {
			continue
		}
		provider := strings.TrimSpace(st.ProviderID)
		if provider == "" {
			provider = "-"
		}
		region := strings.TrimSpace(st.Region)
		if region == "" {
			region = "-"
		}
		label := provider + "/" + region
		if phaseByPR[label] > 1 {
			label = string(st.Phase) + ":" + label
		}
		elapsed := now.Sub(st.StartedAt).Round(time.Second)
		if elapsed < 0 {
			elapsed = 0
		}
		labelFixed := fitCell(label, 24)
		elapsedFixed := fmt.Sprintf("%6s", formatStepElapsed(elapsed))
		tokens = append(tokens, fmt.Sprintf("[%s %s]", phaseStyle(st.Phase).Render(labelFixed), activeElapsedStyle.Render(elapsedFixed)))
	}
	if len(tokens) == 0 {
		return ""
	}

	full := strings.Join(tokens, " ")
	if lipgloss.Width(full) <= width {
		return full
	}

	var outParts []string
	used := 0
	for i, tok := range tokens {
		tokW := lipgloss.Width(tok)
		sepW := 0
		if len(outParts) > 0 {
			sepW = 1
		}
		if used+sepW+tokW > width {
			hidden := len(tokens) - i
			more := fmt.Sprintf("(+%d more)", hidden)
			moreW := lipgloss.Width(more)
			if len(outParts) == 0 {
				return lipgloss.NewStyle().MaxWidth(width).Render(activeMoreStyle.Render(more))
			}
			for len(outParts) > 0 && used+1+moreW > width {
				last := outParts[len(outParts)-1]
				lastW := lipgloss.Width(last)
				outParts = outParts[:len(outParts)-1]
				used -= lastW
				if len(outParts) > 0 {
					used -= 1
				}
			}
			if len(outParts) == 0 {
				return lipgloss.NewStyle().MaxWidth(width).Render(activeMoreStyle.Render(more))
			}
			return strings.Join(outParts, " ") + " " + activeMoreStyle.Render(more)
		}
		if sepW > 0 {
			used += sepW
		}
		outParts = append(outParts, tok)
		used += tokW
	}
	return strings.Join(outParts, " ")
}

func phaseStyle(phase core.ScanProgressPhase) lipgloss.Style {
	switch phase {
	case core.PhaseProvider:
		return phaseProviderStyle
	case core.PhaseResolver:
		return phaseResolverStyle
	case core.PhaseAudit:
		return phaseAuditStyle
	case core.PhaseCost:
		return phaseCostStyle
	default:
		return activeDefaultStyle
	}
}

func formatScanLabel(ev core.ScanProgressEvent) string {
	label := strings.TrimSpace(fmt.Sprintf("%s %s %s", ev.Phase, ev.ProviderID, ev.Region))
	if label == "" {
		label = "provider"
	}
	return label
}

func fitLabel(s string, w int) string {
	return fitCell(s, w)
}

func fitCell(s string, w int) string {
	s = strings.TrimSpace(s)
	if w <= 0 {
		return s
	}
	if lipgloss.Width(s) == w {
		return s
	}
	if lipgloss.Width(s) < w {
		return s + strings.Repeat(" ", w-lipgloss.Width(s))
	}
	if w <= 1 {
		return s[:1]
	}
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		next := string(append(out, r))
		if lipgloss.Width(next) > w-1 {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return "…"
	}
	return string(out) + "…"
}

func formatScanExtras(ev core.ScanProgressEvent) string {
	// Show a short type breakdown (top few) and an optional sample list (instances, LBs, etc.).
	typeLine := ""
	if len(ev.StepTypeCounts) > 0 {
		type typeCount struct {
			typ string
			n   int
		}
		var xs []typeCount
		for t, n := range ev.StepTypeCounts {
			xs = append(xs, typeCount{typ: t, n: n})
		}
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].n != xs[j].n {
				return xs[i].n > xs[j].n
			}
			return xs[i].typ < xs[j].typ
		})
		limit := 4
		if len(xs) < limit {
			limit = len(xs)
		}
		var parts []string
		for i := 0; i < limit; i++ {
			parts = append(parts, fmt.Sprintf("%s=%d", xs[i].typ, xs[i].n))
		}
		typeLine = strings.Join(parts, " ")
	}

	sample := ""
	if ev.StepSampleLabel != "" && ev.StepSampleTotal > 0 {
		if len(ev.StepSampleItems) > 0 {
			more := ""
			if ev.StepSampleTotal > len(ev.StepSampleItems) {
				more = fmt.Sprintf(" (+%d more)", ev.StepSampleTotal-len(ev.StepSampleItems))
			}
			sample = fmt.Sprintf("%s=%d: %s%s", ev.StepSampleLabel, ev.StepSampleTotal, strings.Join(ev.StepSampleItems, ", "), more)
		} else {
			sample = fmt.Sprintf("%s=%d", ev.StepSampleLabel, ev.StepSampleTotal)
		}
	}

	extra := ""
	if typeLine != "" {
		extra += " | " + typeLine
	}
	if sample != "" {
		extra += " | " + sample
	}
	return extra
}

func formatStepMark(ev core.ScanProgressEvent) string {
	if strings.TrimSpace(ev.StepError) != "" {
		return "✗"
	}
	return "✓"
}

func formatStepMarkStyled(ev core.ScanProgressEvent) string {
	if strings.TrimSpace(ev.StepError) != "" {
		return crossMark.String()
	}
	return checkMark.String()
}

func formatDoneLinePlain(ev core.ScanProgressEvent) string {
	label := formatScanLabel(ev)
	// Fixed-width label makes the counts line up in plain terminal output.
	line := fmt.Sprintf("%s %s  res=%6d  edges=%6d  +res=%6d  +edges=%6d%s",
		formatStepMark(ev),
		fitLabel(label, 32),
		ev.ResourcesSoFar, ev.EdgesSoFar,
		ev.StepResourcesAdded, ev.StepEdgesAdded,
		formatScanExtras(ev),
	)
	if strings.TrimSpace(ev.StepError) != "" {
		line += " | error=" + strings.TrimSpace(ev.StepError)
	}
	return line
}

func formatDoneLineTTY(ev core.ScanProgressEvent) string {
	label := formatScanLabel(ev)
	line := fmt.Sprintf("%s %s  res=%6d  edges=%6d  +res=%6d  +edges=%6d%s",
		formatStepMarkStyled(ev),
		fitLabel(label, 32),
		ev.ResourcesSoFar, ev.EdgesSoFar,
		ev.StepResourcesAdded, ev.StepEdgesAdded,
		formatScanExtras(ev),
	)
	if strings.TrimSpace(ev.StepError) != "" {
		line += " | error=" + strings.TrimSpace(ev.StepError)
	}
	return line
}

func formatScanCompleteLineTTY(res core.ScanResult) string {
	return fmt.Sprintf("%s %s  res=%6d  edges=%6d", checkMark, fitLabel("scan complete", 32), res.Resources, res.Edges)
}

func formatFailureSummaryTTY(fs []core.ScanStepFailure) string {
	if len(fs) == 0 {
		return ""
	}
	// Keep it compact and aligned; avoid printing huge error strings.
	var lines []string
	lines = append(lines, fmt.Sprintf("%s %s", crossMark, headerStyle.Render(fmt.Sprintf("Errors (%d)", len(fs)))))
	for i, f := range fs {
		if i >= 20 {
			lines = append(lines, fmt.Sprintf("... (+%d more)", len(fs)-i))
			break
		}
		label := strings.TrimSpace(fmt.Sprintf("%s %s %s", f.Phase, f.ProviderID, f.Region))
		lines = append(lines, fmt.Sprintf("%s %s  %s",
			crossMark,
			fitLabel(label, 32),
			trimOneLine(f.Error, 140),
		))
	}
	return strings.Join(lines, "\n")
}

func formatFailureSummaryPlain(fs []core.ScanStepFailure) string {
	if len(fs) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Errors (%d):", len(fs)))
	for i, f := range fs {
		if i >= 50 {
			lines = append(lines, fmt.Sprintf("... (+%d more)", len(fs)-i))
			break
		}
		label := strings.TrimSpace(fmt.Sprintf("%s %s %s", f.Phase, f.ProviderID, f.Region))
		lines = append(lines, fmt.Sprintf("  - %-32s  %s", label, trimOneLine(f.Error, 200)))
	}
	return strings.Join(lines, "\n")
}

func trimOneLine(s string, maxLen int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func formatStepElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%02dm", h, m)
}

var (
	currentStepStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("211"))
	doneStyle          = lipgloss.NewStyle().Margin(1, 2)
	checkMark          = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
	crossMark          = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).SetString("✗")
	runningMarkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	pendingMarkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	headerStyle        = lipgloss.NewStyle().Bold(true)
	phaseProviderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	phaseResolverStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	phaseAuditStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	phaseCostStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	activeDefaultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	activeElapsedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeMoreStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	selectedMarkStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("221")).Bold(true)
)

func hasProviderID(ids []string, needle string) bool {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" {
		return false
	}
	for _, id := range ids {
		if strings.TrimSpace(strings.ToLower(id)) == needle {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
