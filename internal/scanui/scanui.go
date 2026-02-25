package scanui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"awscope/internal/core"
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

type activeStep struct {
	Key        string
	Phase      core.ScanProgressPhase
	ProviderID string
	Region     string
	Message    string
	StartedAt  time.Time
	UpdatedAt  time.Time
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

	doneLines     []string
	active        map[string]activeStep
	activeOrder   []string
	marqueeOffset int

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
		ctx:      ctx,
		app:      app,
		opts:     opts,
		spin:     spin,
		progress: p,
		start:    time.Now(),
		active:   map[string]activeStep{},
	}

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

	// Render recent completed lines above the "sticky" progress line, package-manager style,
	// but without tea.Printf to avoid interleaved output.
	stickyCount := 2
	if m.height == 1 {
		stickyCount = 1
	}
	var lines []string
	if n := len(m.doneLines); n > 0 && m.height > 0 {
		avail := m.height - stickyCount
		if avail < 0 {
			avail = 0
		}
		if avail > 0 {
			if n > avail {
				lines = append(lines, fmt.Sprintf("... (%d more)", n-avail))
				lines = append(lines, m.doneLines[n-avail:]...)
			} else {
				lines = append(lines, m.doneLines...)
			}
		}
	}
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

func (m *model) trackProgressEvent(ev core.ScanProgressEvent, now time.Time) {
	if m.active == nil {
		m.active = map[string]activeStep{}
	}
	key := stepKey(ev)
	if key == "" {
		return
	}
	if strings.TrimSpace(ev.Message) == "done" {
		delete(m.active, key)
		return
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
	headerStyle        = lipgloss.NewStyle().Bold(true)
	phaseProviderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	phaseResolverStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	phaseAuditStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	phaseCostStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	activeDefaultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	activeElapsedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	activeMoreStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
)

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
