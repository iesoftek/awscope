package app

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func newActionStreamTestModel() model {
	in := textinput.New()
	in.Focus()
	vp := viewport.New(24, 6)
	return model{
		actionStreamOpen:       true,
		actionStreamRunning:    true,
		actionStreamFollowTail: true,
		actionStreamWrap:       true,
		actionStreamColorize:   true,
		actionStreamSeq:        1,
		actionStreamViewport:   vp,
		actionStreamInput:      in,
		focus:                  focusActionStreamInput,
	}
}

func TestActionStreamEscStopsWithoutImmediateClose(t *testing.T) {
	m := newActionStreamTestModel()
	ctx, cancel := context.WithCancel(context.Background())
	m.actionStreamCancel = cancel
	_, w := io.Pipe()
	m.actionStreamInputWriter = w

	cmd, handled := m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatalf("expected esc to be handled")
	}
	if cmd != nil {
		t.Fatalf("expected no command for esc stop")
	}
	if !m.actionStreamOpen {
		t.Fatalf("stream view should remain open while stopping")
	}
	if !m.actionStreamStopping {
		t.Fatalf("expected stopping=true")
	}
	if !m.actionStreamCloseOnStop {
		t.Fatalf("expected closeOnStop=true")
	}
	if m.actionStreamInputWriter != nil {
		t.Fatalf("expected input writer closed")
	}
	if m.actionStreamCancel != nil {
		t.Fatalf("expected cancel func cleared")
	}
	if m.statusLine == "" || !strings.Contains(strings.ToLower(m.statusLine), "stopping") {
		t.Fatalf("expected stopping status line, got %q", m.statusLine)
	}

	// Ensure cancel actually fired.
	if ctx.Err() == nil {
		t.Fatalf("expected context canceled")
	}
}

func TestActionStreamDoneAfterEscAutoCloses(t *testing.T) {
	m := newActionStreamTestModel()
	m.actionStreamOpen = true
	m.actionStreamStopping = true
	m.actionStreamCloseOnStop = true
	m.actionStreamRunning = true
	m.actionStreamSeq = 42
	m.focus = focusActionStreamInput

	m.handleActionStreamMessage(actionStreamDoneMsg{seq: 42, err: context.Canceled, line: "action sns.stream FAILED (run-1)"})

	if m.actionStreamOpen {
		t.Fatalf("expected stream view to auto-close after stop completion")
	}
	if m.focus != focusResources {
		t.Fatalf("expected focus resources after close, got %v", m.focus)
	}
	if m.actionStreamRunning {
		t.Fatalf("expected running=false")
	}
}

func TestActionStreamWrapsLongLinesToViewportWidth(t *testing.T) {
	m := newActionStreamTestModel()
	m.actionStreamViewport.Width = 20
	m.actionStreamViewport.Height = 6

	m.appendActionStreamOutput(`{"very":"long json line with lots of data and nested values to wrap"}`+"\n", false)

	if strings.TrimSpace(m.actionStreamWrappedLog) == "" {
		t.Fatalf("expected wrapped content")
	}
	for _, ln := range strings.Split(m.actionStreamWrappedLog, "\n") {
		if len([]rune(ln)) > 20 {
			t.Fatalf("wrapped line exceeds width: %q", ln)
		}
	}
}

func TestActionStreamFollowTailToggleAndPreserveOffset(t *testing.T) {
	m := newActionStreamTestModel()
	m.actionStreamViewport.Width = 32
	m.actionStreamViewport.Height = 5

	for i := 0; i < 18; i++ {
		m.appendActionStreamOutput("line "+strings.Repeat("x", 20)+"\n", false)
	}
	if !m.actionStreamViewport.AtBottom() {
		t.Fatalf("expected initial follow tail at bottom")
	}

	cmd, handled := m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyUp})
	if !handled {
		t.Fatalf("expected up to be handled")
	}
	if cmd == nil {
		// viewport may not always return a cmd, that's fine
	}
	if m.actionStreamFollowTail {
		t.Fatalf("expected follow tail off after scrolling up")
	}
	off := m.actionStreamViewport.YOffset

	m.appendActionStreamOutput("new incoming line after scroll\n", false)
	if m.actionStreamViewport.YOffset != off {
		t.Fatalf("expected offset preserved when follow off; got=%d want=%d", m.actionStreamViewport.YOffset, off)
	}
	if m.actionStreamViewport.AtBottom() {
		t.Fatalf("expected not forced to bottom while follow off")
	}

	m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyEnd})
	if !m.actionStreamFollowTail {
		t.Fatalf("expected follow tail on after G")
	}
	if !m.actionStreamViewport.AtBottom() {
		t.Fatalf("expected bottom after G")
	}

	m.appendActionStreamOutput("another line with follow on\n", false)
	if !m.actionStreamViewport.AtBottom() {
		t.Fatalf("expected to remain at bottom when follow on")
	}
}

func TestActionStreamScrollKeysWorkWhenInputFocused(t *testing.T) {
	m := newActionStreamTestModel()
	m.focus = focusActionStreamInput
	m.actionStreamFollowTail = false
	m.actionStreamViewport.Width = 24
	m.actionStreamViewport.Height = 4

	for i := 0; i < 20; i++ {
		m.appendActionStreamOutput("row\n", false)
	}
	m.actionStreamViewport.GotoTop()
	before := m.actionStreamViewport.YOffset

	_, handled := m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyPgDown})
	if !handled {
		t.Fatalf("expected pgdown handled while input focused")
	}
	after := m.actionStreamViewport.YOffset
	if after <= before {
		t.Fatalf("expected viewport to scroll while input focused (before=%d after=%d)", before, after)
	}
}

func TestActionStreamToggleKeysIgnoredWhileInputFocused(t *testing.T) {
	m := newActionStreamTestModel()
	m.focus = focusActionStreamInput
	m.actionStreamWrap = true
	m.actionStreamColorize = true

	_, handled := m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if !handled {
		t.Fatalf("expected key handled by input")
	}
	if !m.actionStreamWrap {
		t.Fatalf("expected wrap unchanged while input focused")
	}

	_, handled = m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !handled {
		t.Fatalf("expected key handled by input")
	}
	if !m.actionStreamColorize {
		t.Fatalf("expected colorize unchanged while input focused")
	}
}

func TestActionStreamToggleKeysWorkInOutputFocus(t *testing.T) {
	m := newActionStreamTestModel()
	m.focus = focusActionStream
	m.actionStreamViewport.Width = 24
	m.actionStreamViewport.Height = 4
	m.appendActionStreamOutput("{\"hello\":\"world\"}\n", false)

	_, handled := m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if !handled {
		t.Fatalf("expected wrap toggle handled")
	}
	if m.actionStreamWrap {
		t.Fatalf("expected wrap toggled off")
	}

	_, handled = m.handleActionStreamKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !handled {
		t.Fatalf("expected color toggle handled")
	}
	if m.actionStreamColorize {
		t.Fatalf("expected colorize toggled off")
	}
}

func TestActionStreamColorizeParsesJSONBlock(t *testing.T) {
	m := newActionStreamTestModel()
	in := strings.Join([]string{
		"[2026-02-25 13:31:00] id=x rc=1 topic=foo",
		"msg:",
		`{"a":1,"b":[1,true,null,"x"],"c":{"k":"v"}}`,
		"",
	}, "\n")

	got := m.colorizeActionStreamPayload(in)
	if !strings.Contains(got, "  \"a\": 1,") {
		t.Fatalf("expected pretty JSON object key formatting, got: %q", got)
	}
	if !strings.Contains(got, "    true,") || !strings.Contains(got, "    null,") {
		t.Fatalf("expected pretty JSON array scalar formatting, got: %q", got)
	}
	if !strings.Contains(got, "  \"c\": {") {
		t.Fatalf("expected nested object formatting, got: %q", got)
	}
}

func TestActionStreamColorizeLeavesNonJSONBlock(t *testing.T) {
	m := newActionStreamTestModel()
	in := strings.Join([]string{
		"[2026-02-25 13:31:00] id=x rc=1 topic=foo",
		"msg:",
		"plain text payload",
		"",
	}, "\n")

	got := m.colorizeActionStreamPayload(in)
	if !strings.Contains(got, "plain text payload") {
		t.Fatalf("expected non-JSON payload to remain visible, got: %q", got)
	}
}
