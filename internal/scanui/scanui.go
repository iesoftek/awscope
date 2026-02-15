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

	MaxConcurrency      int
	ResolverConcurrency int
}

type progressMsg struct {
	ev core.ScanProgressEvent
}

type doneMsg struct {
	res core.ScanResult
	err error
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

	doneLines []string

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
			Profile:             opts.Profile,
			Regions:             opts.Regions,
			ProviderIDs:         opts.ProviderIDs,
			MaxConcurrency:      opts.MaxConcurrency,
			ResolverConcurrency: opts.ResolverConcurrency,
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
			Profile:             m.opts.Profile,
			Regions:             m.opts.Regions,
			ProviderIDs:         m.opts.ProviderIDs,
			MaxConcurrency:      m.opts.MaxConcurrency,
			ResolverConcurrency: m.opts.ResolverConcurrency,
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
	info := fmt.Sprintf("Scanning %s: %s | resources=%d edges=%d | %s", currentStepStyle.Render(label), m.curEv.Message, m.resSoFar, m.edgesSoFar, elapsed)

	cellsAvail := max(0, m.width-lipgloss.Width(spin+prog+stepCount))
	info = lipgloss.NewStyle().MaxWidth(cellsAvail).Render(info)
	gap := strings.Repeat(" ", max(0, m.width-lipgloss.Width(spin+info+prog+stepCount)))

	progressLine := spin + info + gap + prog + stepCount

	// Render recent completed lines above the "sticky" progress line, package-manager style,
	// but without tea.Printf to avoid interleaved output.
	var lines []string
	if n := len(m.doneLines); n > 0 && m.height > 0 {
		avail := m.height - 2
		if avail < 1 {
			avail = 1
		}
		if n > avail {
			lines = append(lines, fmt.Sprintf("... (%d more)", n-avail))
			lines = append(lines, m.doneLines[n-avail:]...)
		} else {
			lines = append(lines, m.doneLines...)
		}
	}
	lines = append(lines, progressLine)
	return strings.Join(lines, "\n")
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func formatScanLabel(ev core.ScanProgressEvent) string {
	label := strings.TrimSpace(fmt.Sprintf("%s %s %s", ev.Phase, ev.ProviderID, ev.Region))
	if label == "" {
		label = "provider"
	}
	return label
}

func fitLabel(s string, w int) string {
	s = strings.TrimSpace(s)
	if w <= 0 {
		return s
	}
	if len(s) == w {
		return s
	}
	if len(s) < w {
		return s + strings.Repeat(" ", w-len(s))
	}
	if w <= 3 {
		return s[:w]
	}
	return s[:w-3] + "..."
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

var (
	currentStepStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("211"))
	doneStyle        = lipgloss.NewStyle().Margin(1, 2)
	checkMark        = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
	crossMark        = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).SetString("✗")
	headerStyle      = lipgloss.NewStyle().Bold(true)
)

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
