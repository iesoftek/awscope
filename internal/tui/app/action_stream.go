package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"awscope/internal/core"
	"awscope/internal/graph"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/wordwrap"
)

const defaultActionStreamMaxBytes = 2 * 1024 * 1024

var actionStreamJSONKVPattern = regexp.MustCompile(`^(\s*)"([^"]+)"(\s*:\s*)(.*)$`)
var actionStreamJSONScalarPattern = regexp.MustCompile(`^(\s*)("([^"\\]|\\.)*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+\-]?\d+)?|true|false|null)(,?)$`)

type actionStreamMsgWriter struct {
	seq    int
	ch     chan<- tea.Msg
	stderr bool
}

func (w *actionStreamMsgWriter) Write(p []byte) (int, error) {
	if w == nil || len(p) == 0 {
		return len(p), nil
	}
	w.ch <- actionStreamChunkMsg{seq: w.seq, chunk: string(p), stderr: w.stderr}
	return len(p), nil
}

func waitActionStreamMsgCmd(seq int, ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return actionStreamClosedMsg{seq: seq}
		}
		return msg
	}
}

func (m *model) startActionStreamCmd(actionID string, key graph.ResourceKey, profile, target string) tea.Cmd {
	if m == nil {
		return nil
	}
	m.closeActionStreamProcess()

	if m.actionStreamMaxBytes <= 0 {
		m.actionStreamMaxBytes = defaultActionStreamMaxBytes
	}

	seq := m.actionStreamSeq + 1
	m.actionStreamSeq = seq
	runCtx, cancel := context.WithCancel(m.ctx)
	stdinR, stdinW := io.Pipe()
	ch := make(chan tea.Msg, 4096)

	m.actionStreamOpen = true
	m.actionStreamRunning = true
	m.actionStreamStopping = false
	m.actionStreamCloseOnStop = false
	m.actionStreamFollowTail = true
	m.actionStreamErr = nil
	m.actionStreamDoneLine = ""
	m.actionStreamActionID = actionID
	m.actionStreamTarget = target
	m.actionStreamLog = ""
	m.actionStreamWrappedLog = ""
	m.actionStreamRenderWidth = 0
	m.actionStreamCancel = cancel
	m.actionStreamInputWriter = stdinW
	m.actionStreamCh = ch
	m.actionStreamInput.SetValue("")
	m.actionStreamInput.Focus()
	m.focus = focusActionStreamInput
	m.loading = false
	m.resizeActionStreamWidgets()
	m.appendActionStreamOutput(fmt.Sprintf("starting action %s on %s\n", actionID, target), false)

	go func() {
		defer close(ch)
		defer stdinR.Close()

		stdout := &actionStreamMsgWriter{seq: seq, ch: ch, stderr: false}
		stderr := &actionStreamMsgWriter{seq: seq, ch: ch, stderr: true}
		res, err := core.RunAction(runCtx, m.st, actionID, key, profile, core.RunActionOptions{
			Stdin:                       stdinR,
			Stdout:                      stdout,
			Stderr:                      stderr,
			AutoApproveTeardownOnCancel: true,
		})

		line := ""
		if strings.TrimSpace(res.ActionID) != "" {
			line = fmt.Sprintf("action %s %s (%s)", res.ActionID, res.Status, res.ActionRunID)
		}
		ch <- actionStreamDoneMsg{seq: seq, line: line, err: err}
	}()

	return waitActionStreamMsgCmd(seq, ch)
}

func (m *model) closeActionStreamProcess() {
	if m == nil {
		return
	}
	if m.actionStreamInputWriter != nil {
		_ = m.actionStreamInputWriter.Close()
		m.actionStreamInputWriter = nil
	}
	if m.actionStreamCancel != nil {
		m.actionStreamCancel()
		m.actionStreamCancel = nil
	}
}

func (m *model) closeActionStreamView() {
	if m == nil {
		return
	}
	m.closeActionStreamProcess()
	m.actionStreamOpen = false
	m.actionStreamRunning = false
	m.actionStreamStopping = false
	m.actionStreamCloseOnStop = false
	m.actionStreamFollowTail = true
	m.actionStreamActionID = ""
	m.actionStreamTarget = ""
	m.actionStreamErr = nil
	m.actionStreamDoneLine = ""
	m.actionStreamLog = ""
	m.actionStreamWrappedLog = ""
	m.actionStreamRenderWidth = 0
	m.actionStreamCh = nil
	m.actionStreamInput.SetValue("")
	m.actionStreamInput.Blur()
	m.focus = focusResources
}

func wrapActionStreamPayload(s string, width int) string {
	if width <= 1 {
		return s
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln == "" {
			out = append(out, "")
			continue
		}
		// Keep wrapping width-safe even for long JSON tokens with no spaces.
		if strings.ContainsAny(ln, " \t") {
			soft := wordwrap.String(ln, width)
			out = append(out, strings.Split(soft, "\n")...)
			continue
		}
		out = append(out, wrapActionStreamLineHard(ln, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapActionStreamLineHard(line string, width int) []string {
	if width <= 1 || line == "" {
		return []string{line}
	}
	out := make([]string, 0, 1)
	rest := line
	for rest != "" {
		cut := 0
		w := 0
		for idx, r := range rest {
			rw := runewidth.RuneWidth(r)
			if rw <= 0 {
				rw = 1
			}
			if w+rw > width {
				break
			}
			cut = idx + utf8.RuneLen(r)
			w += rw
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(rest)
			if size <= 0 {
				break
			}
			cut = size
		}
		out = append(out, rest[:cut])
		rest = rest[cut:]
	}
	if len(out) == 0 {
		return []string{line}
	}
	return out
}

func (m model) colorizeActionStreamLine(line string) string {
	trim := strings.TrimSpace(line)
	if trim == "" {
		return line
	}
	if strings.HasPrefix(trim, "[stderr]") {
		return m.styles.Bad.Render(line)
	}
	if strings.HasPrefix(trim, "[") && strings.Contains(trim, "] id=") {
		return m.styles.IconDim.Render(line)
	}
	if trim == "msg:" {
		return m.styles.Label.Render(line)
	}
	if trim == "{" || trim == "}" || trim == "[" || trim == "]" || trim == "}," || trim == "]," {
		return m.styles.Dim.Render(line)
	}
	matches := actionStreamJSONKVPattern.FindStringSubmatch(line)
	if len(matches) != 5 {
		return line
	}
	indent := matches[1]
	key := matches[2]
	sep := matches[3]
	rawVal := matches[4]

	wsLen := len(rawVal) - len(strings.TrimLeft(rawVal, " \t"))
	valLead := ""
	if wsLen > 0 {
		valLead = rawVal[:wsLen]
	}
	val := strings.TrimSpace(rawVal)
	comma := ""
	if strings.HasSuffix(val, ",") {
		comma = ","
		val = strings.TrimSpace(strings.TrimSuffix(val, ","))
	}

	valStyled := m.styles.Value.Render(val)
	switch {
	case val == "true" || val == "false":
		valStyled = m.styles.Warn.Render(val)
	case val == "null":
		valStyled = m.styles.Dim.Render(val)
	case strings.HasPrefix(val, "{") || strings.HasPrefix(val, "["):
		valStyled = m.styles.Dim.Render(val)
	default:
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			valStyled = m.styles.Good.Render(val)
		}
	}
	if comma != "" {
		valStyled += m.styles.Dim.Render(comma)
	}

	return indent + m.styles.Label.Render("\""+key+"\"") + m.styles.Dim.Render(sep) + valLead + valStyled
}

func (m model) styleJSONScalarValue(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "true" || v == "false":
		return m.styles.Warn.Render(v)
	case v == "null":
		return m.styles.Dim.Render(v)
	case strings.HasPrefix(v, "{") || strings.HasPrefix(v, "["):
		return m.styles.Dim.Render(v)
	case strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\""):
		return m.styles.Value.Render(v)
	default:
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			return m.styles.Good.Render(v)
		}
		return m.styles.Value.Render(v)
	}
}

func (m model) colorizeJSONLine(line string) string {
	trim := strings.TrimSpace(line)
	if trim == "" {
		return line
	}
	if trim == "{" || trim == "}" || trim == "[" || trim == "]" || trim == "}," || trim == "]," {
		return m.styles.Dim.Render(line)
	}
	if m1 := actionStreamJSONKVPattern.FindStringSubmatch(line); len(m1) == 5 {
		indent := m1[1]
		key := m1[2]
		sep := m1[3]
		rawVal := m1[4]

		wsLen := len(rawVal) - len(strings.TrimLeft(rawVal, " \t"))
		valLead := ""
		if wsLen > 0 {
			valLead = rawVal[:wsLen]
		}
		val := strings.TrimSpace(rawVal)
		comma := ""
		if strings.HasSuffix(val, ",") {
			comma = ","
			val = strings.TrimSpace(strings.TrimSuffix(val, ","))
		}
		valStyled := m.styleJSONScalarValue(val)
		if comma != "" {
			valStyled += m.styles.Dim.Render(comma)
		}
		return indent + m.styles.Label.Render("\""+key+"\"") + m.styles.Dim.Render(sep) + valLead + valStyled
	}
	if m2 := actionStreamJSONScalarPattern.FindStringSubmatch(line); len(m2) == 4 {
		indent := m2[1]
		val := m2[2]
		comma := m2[3]
		styled := m.styleJSONScalarValue(val)
		if comma != "" {
			styled += m.styles.Dim.Render(comma)
		}
		return indent + styled
	}
	return line
}

func (m model) colorizeJSONBlock(block string) (string, bool) {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return "", false
	}
	var doc any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return "", false
	}
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(pretty), "\n")
	for i := range lines {
		lines[i] = m.colorizeJSONLine(lines[i])
	}
	return strings.Join(lines, "\n"), true
}

func (m model) colorizeActionStreamPayload(s string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if trim != "msg:" {
			out = append(out, m.colorizeActionStreamLine(line))
			continue
		}

		out = append(out, m.styles.Label.Render(line))
		start := i + 1
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		block := strings.Join(lines[start:end], "\n")
		if colored, ok := m.colorizeJSONBlock(block); ok {
			out = append(out, strings.Split(colored, "\n")...)
		} else {
			for j := start; j < end; j++ {
				out = append(out, m.colorizeActionStreamLine(lines[j]))
			}
		}
		if end < len(lines) {
			out = append(out, lines[end])
		}
		i = end
	}
	return strings.Join(out, "\n")
}

func (m *model) updateActionStreamViewportContent(followTail bool, preserveYOffset int) {
	if m == nil {
		return
	}
	w := m.actionStreamViewport.Width
	if w <= 1 {
		w = 1
	}
	rendered := m.actionStreamLog
	if m.actionStreamWrap {
		rendered = wrapActionStreamPayload(rendered, w)
	}
	if m.actionStreamColorize {
		rendered = m.colorizeActionStreamPayload(rendered)
	}
	m.actionStreamWrappedLog = rendered
	m.actionStreamRenderWidth = w
	m.actionStreamViewport.SetContent(rendered)
	if followTail {
		m.actionStreamViewport.GotoBottom()
		return
	}
	m.actionStreamViewport.SetYOffset(preserveYOffset)
}

func (m *model) appendActionStreamOutput(chunk string, stderr bool) {
	if m == nil || chunk == "" {
		return
	}
	if stderr {
		chunk = "[stderr] " + chunk
	}
	m.actionStreamLog += chunk
	if m.actionStreamMaxBytes <= 0 {
		m.actionStreamMaxBytes = defaultActionStreamMaxBytes
	}
	if len(m.actionStreamLog) > m.actionStreamMaxBytes {
		keep := m.actionStreamMaxBytes - 256
		if keep < 0 {
			keep = m.actionStreamMaxBytes
		}
		if keep < len(m.actionStreamLog) {
			m.actionStreamLog = "... output truncated ...\n" + m.actionStreamLog[len(m.actionStreamLog)-keep:]
		}
	}
	prev := m.actionStreamViewport.YOffset
	follow := m.actionStreamFollowTail || m.actionStreamViewport.AtBottom()
	m.updateActionStreamViewportContent(follow, prev)
}

func (m *model) resizeActionStreamWidgets() {
	if m == nil {
		return
	}
	w := m.width
	if w <= 0 {
		w = 100
	}
	h := m.height
	if h <= 0 {
		h = 32
	}

	vw := max(20, w-6)
	vh := max(8, h-13)
	oldYOffset := m.actionStreamViewport.YOffset
	m.actionStreamViewport.Width = vw
	m.actionStreamViewport.Height = vh
	if strings.TrimSpace(m.actionStreamLog) != "" {
		follow := m.actionStreamFollowTail
		m.updateActionStreamViewportContent(follow, oldYOffset)
	}
	m.actionStreamInput.Width = max(10, min(60, vw-10))
}

func (m *model) submitActionStreamInput() {
	line := m.actionStreamInput.Value()
	m.actionStreamInput.SetValue("")
	if m.actionStreamInputWriter == nil {
		m.statusLine = "action input unavailable"
		return
	}
	m.appendActionStreamOutput("> "+line+"\n", false)
	if _, err := io.WriteString(m.actionStreamInputWriter, line+"\n"); err != nil {
		m.appendActionStreamOutput("write input failed: "+err.Error()+"\n", true)
		m.statusLine = "failed to send input to action"
	}
}

func isActionStreamScrollKey(k string) bool {
	switch k {
	case "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end", "g", "G", "j", "k":
		return true
	default:
		return false
	}
}

func (m *model) stopActionStreamAndClose() {
	if m == nil {
		return
	}
	if !m.actionStreamRunning || m.actionStreamStopping {
		return
	}
	m.actionStreamStopping = true
	m.actionStreamCloseOnStop = true
	m.statusLine = "stopping action and cleaning up..."
	m.appendActionStreamOutput("\nstop requested; waiting for cleanup...\n", true)
	m.closeActionStreamProcess()
}

func (m *model) handleActionStreamKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m == nil || !m.actionStreamOpen {
		return nil, false
	}

	k := msg.String()
	switch k {
	case "ctrl+c", "esc", "q":
		if m.actionStreamRunning {
			m.stopActionStreamAndClose()
			return nil, true
		}
		m.closeActionStreamView()
		return nil, true
	case "tab":
		if m.focus == focusActionStreamInput {
			m.focus = focusActionStream
			m.actionStreamInput.Blur()
		} else {
			m.focus = focusActionStreamInput
			m.actionStreamInput.Focus()
		}
		return nil, true
	case "i":
		m.focus = focusActionStreamInput
		m.actionStreamInput.Focus()
		return nil, true
	case "enter":
		if m.focus == focusActionStreamInput {
			m.submitActionStreamInput()
			return nil, true
		}
	}

	if m.focus != focusActionStreamInput {
		switch k {
		case "w", "W":
			m.actionStreamWrap = !m.actionStreamWrap
			prev := m.actionStreamViewport.YOffset
			m.updateActionStreamViewportContent(m.actionStreamFollowTail, prev)
			if m.actionStreamWrap {
				m.statusLine = "action stream wrap: on"
			} else {
				m.statusLine = "action stream wrap: off"
			}
			return nil, true
		case "c", "C":
			m.actionStreamColorize = !m.actionStreamColorize
			prev := m.actionStreamViewport.YOffset
			m.updateActionStreamViewportContent(m.actionStreamFollowTail, prev)
			if m.actionStreamColorize {
				m.statusLine = "action stream color: on"
			} else {
				m.statusLine = "action stream color: off"
			}
			return nil, true
		}
	}

	if isActionStreamScrollKey(k) {
		switch k {
		case "G", "end":
			m.actionStreamFollowTail = true
			m.actionStreamViewport.GotoBottom()
			return nil, true
		case "g", "home":
			m.actionStreamFollowTail = false
			m.actionStreamViewport.GotoTop()
			return nil, true
		default:
			m.actionStreamFollowTail = false
		}
		var cmd tea.Cmd
		m.actionStreamViewport, cmd = m.actionStreamViewport.Update(msg)
		return cmd, true
	}

	if m.focus == focusActionStreamInput {
		var cmd tea.Cmd
		m.actionStreamInput, cmd = m.actionStreamInput.Update(msg)
		return cmd, true
	}

	var cmd tea.Cmd
	m.actionStreamViewport, cmd = m.actionStreamViewport.Update(msg)
	return cmd, true
}

func (m model) actionStreamFullScreenView() string {
	headerStyle := m.styles.Title
	metaW := m.paneRightW
	if metaW <= 0 {
		metaW = max(44, min(72, m.width/2))
	}
	top := m.topBar(headerStyle, metaW)

	state := "done"
	switch {
	case m.actionStreamRunning && m.actionStreamStopping:
		state = "stopping"
	case m.actionStreamRunning:
		state = "running"
	}
	filterLine := fmt.Sprintf("action: %s (%s)", m.actionStreamActionID, state)

	box := m.styles.PaneBorderFocus
	if m.width > 0 {
		box = box.Width(max(10, m.width-2))
	}

	follow := "off"
	if m.actionStreamFollowTail {
		follow = "on"
	}
	wrapMode := "off"
	if m.actionStreamWrap {
		wrapMode = "on"
	}
	colorMode := "off"
	if m.actionStreamColorize {
		colorMode = "on"
	}
	keys := "keys: tab switch input/output • i focus input • enter send • up/down/pgup/pgdown/ctrl+u/ctrl+d scroll • g/G top/bottom • w wrap • c color • esc stop+cleanup+close"
	if !m.actionStreamRunning {
		keys = "keys: up/down/pgup/pgdown/ctrl+u/ctrl+d scroll • g/G top/bottom • w wrap • c color • esc close"
	}

	body := m.actionStreamViewport.View()
	if strings.TrimSpace(body) == "" {
		body = m.styles.Dim.Render("(no output yet)")
	}

	inputLabel := "input"
	if m.focus == focusActionStreamInput {
		inputLabel = "input*"
	}
	inputLine := fmt.Sprintf("%s: %s", inputLabel, m.actionStreamInput.View())
	if !m.actionStreamRunning {
		inputLine = m.styles.Dim.Render(inputLine)
	}

	headLine := m.styles.Title.Render("Action Stream") + " " + m.styles.Dim.Render(m.actionStreamActionID)
	if strings.TrimSpace(m.actionStreamTarget) != "" {
		headLine += " " + m.styles.Dim.Render("target="+m.actionStreamTarget)
	}
	headLine += " " + m.styles.Dim.Render("follow:"+follow+" wrap:"+wrapMode+" color:"+colorMode)

	lines := []string{headLine, m.styles.Dim.Render(keys), body, inputLine}
	if m.actionStreamErr != nil {
		lines = append(lines, m.styles.Bad.Render("error: "+m.actionStreamErr.Error()))
	} else if strings.TrimSpace(m.actionStreamDoneLine) != "" {
		lines = append(lines, m.styles.Good.Render(m.actionStreamDoneLine))
	}

	status := "action completed"
	switch {
	case m.actionStreamRunning && m.actionStreamStopping:
		status = "stopping + cleanup in progress"
	case m.actionStreamRunning:
		status = "streaming action output"
	case m.actionStreamErr != nil:
		status = "action failed"
	}
	controls := "esc stop+cleanup+close • G follow • w wrap • c color"
	if !m.actionStreamRunning {
		controls = "esc close • G follow • w wrap • c color"
	}
	m.statusbar.SetContent("ACTION", "id="+m.actionStreamActionID, controls, status)

	return strings.Join([]string{top, filterLine, box.Render(strings.Join(lines, "\n")), m.statusbar.View()}, "\n")
}

func (m *model) handleActionStreamMessage(msg tea.Msg) tea.Cmd {
	if m == nil {
		return nil
	}
	switch sm := msg.(type) {
	case actionStreamChunkMsg:
		if sm.seq != m.actionStreamSeq {
			return nil
		}
		m.appendActionStreamOutput(sm.chunk, sm.stderr)
		return waitActionStreamMsgCmd(sm.seq, m.actionStreamCh)
	case actionStreamDoneMsg:
		if sm.seq != m.actionStreamSeq {
			return nil
		}
		m.loading = false
		m.actionStreamRunning = false
		m.actionStreamStopping = false
		m.actionStreamDoneLine = strings.TrimSpace(sm.line)
		m.actionStreamErr = sm.err
		if m.actionStreamDoneLine != "" {
			m.appendActionStreamOutput(m.actionStreamDoneLine+"\n", false)
		}

		if sm.err != nil {
			if m.actionStreamCloseOnStop && errors.Is(sm.err, context.Canceled) {
				m.statusLine = "action stopped"
				m.appendActionStreamOutput("action stopped\n", false)
				m.actionStreamErr = nil
			} else {
				m.appendActionStreamOutput("action error: "+sm.err.Error()+"\n", true)
				m.statusLine = "action failed: " + sm.err.Error()
			}
		} else if m.actionStreamDoneLine != "" {
			m.statusLine = m.actionStreamDoneLine
		}
		m.pendingAct = ""
		m.confirm.SetValue("")
		m.confirm.Blur()
		m.focus = focusActionStream
		m.actionStreamInput.Blur()
		m.closeActionStreamProcess()

		if m.actionStreamCloseOnStop {
			m.closeActionStreamView()
		}
		return nil
	case actionStreamClosedMsg:
		if sm.seq != m.actionStreamSeq {
			return nil
		}
		if m.actionStreamRunning {
			m.loading = false
			m.actionStreamRunning = false
			m.actionStreamStopping = false
			m.actionStreamErr = fmt.Errorf("action stream closed unexpectedly")
			m.statusLine = "action stream closed unexpectedly"
			m.closeActionStreamProcess()
			if m.actionStreamCloseOnStop {
				m.closeActionStreamView()
			}
		}
		return nil
	default:
		return nil
	}
}
