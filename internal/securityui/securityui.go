package securityui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"awscope/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Options struct {
	Profile       string
	AccountID     string
	ResourceCount int
	DBPath        string
	SourceLine    string
	Summary       core.ScanSecuritySummary
	ShowDetails   bool
	Color         bool
}

type model struct {
	opts        Options
	width       int
	height      int
	findings    []core.ScanSecurityFinding
	filtered    []int
	cursor      int
	offset      int
	filtering   bool
	filterValue string
	showDetails bool
	noColor     bool
}

func Run(ctx context.Context, opts Options) error {
	m := newModel(opts)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func newModel(opts Options) model {
	findings := append([]core.ScanSecurityFinding(nil), opts.Summary.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		ri := severityRank(findings[i].Severity)
		rj := severityRank(findings[j].Severity)
		if ri != rj {
			return ri > rj
		}
		if findings[i].AffectedCount != findings[j].AffectedCount {
			return findings[i].AffectedCount > findings[j].AffectedCount
		}
		return findings[i].CheckID < findings[j].CheckID
	})
	m := model{
		opts:        opts,
		findings:    findings,
		showDetails: opts.ShowDetails,
		noColor:     !opts.Color,
	}
	m.applyFilter()
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if m.filtering {
			switch key {
			case "enter":
				m.filtering = false
				return m, nil
			case "esc":
				m.filtering = false
				return m, nil
			case "backspace":
				if len(m.filterValue) > 0 {
					m.filterValue = m.filterValue[:len(m.filterValue)-1]
					m.applyFilter()
				}
				return m, nil
			default:
				if isPrintableKey(key) {
					m.filterValue += key
					m.applyFilter()
					return m, nil
				}
			}
		}

		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.filtering {
				m.filtering = false
				return m, nil
			}
			return m, tea.Quit
		case "/":
			m.filtering = true
			return m, nil
		case "j", "down":
			m.moveCursor(1)
			return m, nil
		case "k", "up":
			m.moveCursor(-1)
			return m, nil
		case "g", "home":
			m.cursor = 0
			m.ensureCursorVisible()
			return m, nil
		case "G", "end":
			if n := len(m.filtered); n > 0 {
				m.cursor = n - 1
				m.ensureCursorVisible()
			}
			return m, nil
		case "e", " ":
			m.showDetails = !m.showDetails
			return m, nil
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "loading..."
	}
	hdr := m.renderHeader()
	help := m.renderHelp()

	bodyHeight := m.height - lipgloss.Height(hdr) - lipgloss.Height(help)
	if bodyHeight < 4 {
		bodyHeight = 4
	}

	leftW := max(42, m.width/2)
	if leftW > m.width-30 {
		leftW = m.width - 30
	}
	if leftW < 30 {
		leftW = 30
	}
	rightW := max(20, m.width-leftW-1)

	left := m.renderFindingsPane(leftW, bodyHeight)
	right := m.renderDetailPane(rightW, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return lipgloss.JoinVertical(lipgloss.Left, hdr, body, help)
}

func (m *model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filterValue))
	m.filtered = m.filtered[:0]
	for i, f := range m.findings {
		if q == "" || strings.Contains(strings.ToLower(searchBlob(f)), q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureCursorVisible()
}

func (m *model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.ensureCursorVisible()
}

func (m *model) ensureCursorVisible() {
	visible := m.bodyRows()
	if visible <= 0 {
		visible = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m model) bodyRows() int {
	rows := m.height - 7
	if rows < 3 {
		return 3
	}
	return rows
}

func (m model) renderHeader() string {
	title := "Security Findings (Interactive)"
	if m.noColor {
		title = "Security Findings (Interactive)"
	} else {
		title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render(title)
	}
	crit := m.opts.Summary.AffectedBySeverity[core.ScanSecuritySeverityCritical]
	high := m.opts.Summary.AffectedBySeverity[core.ScanSecuritySeverityHigh]
	med := m.opts.Summary.AffectedBySeverity[core.ScanSecuritySeverityMedium]
	low := m.opts.Summary.AffectedBySeverity[core.ScanSecuritySeverityLow]

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", title)
	fmt.Fprintf(&b, "profile=%s account=%s resources=%d findings=%d assessed=%d skipped=%d\n",
		emptyDash(m.opts.Profile), emptyDash(m.opts.AccountID), m.opts.ResourceCount, len(m.opts.Summary.Findings),
		m.opts.Summary.Coverage.AssessedChecks, m.opts.Summary.Coverage.SkippedChecks)
	fmt.Fprintf(&b, "posture: %s=%d %s=%d %s=%d %s=%d",
		m.severityTag(core.ScanSecuritySeverityCritical), crit,
		m.severityTag(core.ScanSecuritySeverityHigh), high,
		m.severityTag(core.ScanSecuritySeverityMedium), med,
		m.severityTag(core.ScanSecuritySeverityLow), low,
	)
	if s := strings.TrimSpace(m.opts.SourceLine); s != "" {
		fmt.Fprintf(&b, "\nsource: %s", s)
	}
	if len(m.opts.Summary.Coverage.MissingServices) > 0 {
		fmt.Fprintf(&b, "\ncoverage gaps: %s", strings.Join(m.opts.Summary.Coverage.MissingServices, ","))
	}
	return b.String()
}

func (m model) renderFindingsPane(width, height int) string {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(width).Height(height)
	header := fmt.Sprintf("Findings (%d)", len(m.filtered))
	if q := strings.TrimSpace(m.filterValue); q != "" {
		header += fmt.Sprintf(" filter=%q", q)
	}

	rows := max(1, height-2)
	if len(m.filtered) == 0 {
		return border.Render(header + "\n\nNo findings match filter.")
	}

	start := m.offset
	if start > len(m.filtered)-1 {
		start = len(m.filtered) - 1
	}
	if start < 0 {
		start = 0
	}
	end := min(len(m.filtered), start+rows)

	lines := make([]string, 0, rows)
	for i := start; i < end; i++ {
		f := m.findings[m.filtered[i]]
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		sev := m.severityTag(f.Severity)
		line := fmt.Sprintf("%s %-8s %-8s %5d  %s", cursor, sev, f.CheckID, f.AffectedCount, strings.TrimSpace(f.Title))
		lines = append(lines, truncate(line, width-4))
	}
	if end < len(m.filtered) {
		lines = append(lines, truncate(fmt.Sprintf("... (+%d more)", len(m.filtered)-end), width-4))
	}
	return border.Render(header + "\n" + strings.Join(lines, "\n"))
}

func (m model) renderDetailPane(width, height int) string {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(width).Height(height)
	header := "Details"
	if len(m.filtered) == 0 {
		return border.Render(header + "\n\nNo finding selected.")
	}
	f := m.findings[m.filtered[m.cursor]]

	lines := []string{
		fmt.Sprintf("ID: %s", f.CheckID),
		fmt.Sprintf("Severity: %s", m.severityTag(f.Severity)),
		fmt.Sprintf("Service: %s", emptyDash(f.Service)),
		fmt.Sprintf("Affected: %d", f.AffectedCount),
	}
	if s := strings.TrimSpace(f.ControlRef); s != "" {
		lines = append(lines, "Control: "+s)
	}
	if s := strings.TrimSpace(f.GuidanceURL); s != "" {
		lines = append(lines, "Guidance: "+s)
	}

	if m.showDetails {
		if len(f.Regions) > 0 {
			lines = append(lines, "Regions:")
			lines = append(lines, "  "+strings.Join(f.Regions, ", "))
		}
		if len(f.Samples) > 0 {
			lines = append(lines, "Samples:")
			for _, sample := range f.Samples {
				lines = append(lines, "  - "+sample)
			}
		}
	} else {
		lines = append(lines, "Details collapsed (press e to expand).")
	}

	content := strings.Join(lines, "\n")
	return border.Render(header + "\n" + wrap(content, width-4, height-2))
}

func (m model) renderHelp() string {
	mode := "normal"
	if m.filtering {
		mode = "filter"
	}
	filterLine := ""
	if m.filtering {
		filterLine = fmt.Sprintf(" | filter: %s", m.filterValue)
	}
	return fmt.Sprintf("mode=%s | j/k move | g/G top/bottom | / filter | e expand/collapse | q quit%s", mode, filterLine)
}

func (m model) severityTag(sev core.ScanSecuritySeverity) string {
	txt := strings.ToUpper(string(sev))
	switch sev {
	case core.ScanSecuritySeverityCritical:
		return m.applyColor(txt, "196", true)
	case core.ScanSecuritySeverityHigh:
		return m.applyColor(txt, "203", false)
	case core.ScanSecuritySeverityMedium:
		return m.applyColor(txt, "214", false)
	case core.ScanSecuritySeverityLow:
		return m.applyColor(txt, "81", false)
	default:
		return txt
	}
}

func (m model) applyColor(s, color string, bold bool) string {
	if m.noColor {
		return s
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	if bold {
		st = st.Bold(true)
	}
	return st.Render(s)
}

func searchBlob(f core.ScanSecurityFinding) string {
	parts := []string{
		f.CheckID,
		string(f.Severity),
		f.Title,
		f.Service,
		f.ControlRef,
		f.GuidanceURL,
		strings.Join(f.Regions, ","),
		strings.Join(f.Samples, ","),
	}
	return strings.Join(parts, " ")
}

func wrap(s string, width, maxLines int) string {
	if width <= 0 || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		if line == "" {
			out = append(out, "")
			if len(out) >= maxLines {
				return strings.Join(out[:maxLines], "\n")
			}
			continue
		}
		for len(line) > width {
			out = append(out, line[:width])
			line = line[width:]
			if len(out) >= maxLines {
				return strings.Join(out[:maxLines], "\n")
			}
		}
		out = append(out, line)
		if len(out) >= maxLines {
			return strings.Join(out[:maxLines], "\n")
		}
	}
	return strings.Join(out, "\n")
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

func severityRank(sev core.ScanSecuritySeverity) int {
	switch sev {
	case core.ScanSecuritySeverityCritical:
		return 4
	case core.ScanSecuritySeverityHigh:
		return 3
	case core.ScanSecuritySeverityMedium:
		return 2
	case core.ScanSecuritySeverityLow:
		return 1
	default:
		return 0
	}
}

func isPrintableKey(k string) bool {
	if len(k) != 1 {
		return false
	}
	ch := k[0]
	return ch >= 32 && ch <= 126
}

func emptyDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
