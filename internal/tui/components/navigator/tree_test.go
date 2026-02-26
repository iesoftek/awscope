package navigator

import (
	"strings"
	"testing"

	"awscope/internal/tui/icons"
)

func TestSetBusyServicesUsesSpinnerGlyphForServiceRow(t *testing.T) {
	m := New([]string{"ec2"}, func(string) []string { return nil })
	m.SetIcons(icons.New(icons.ModeASCII))
	m.SetSize(48, 8)

	plain := m.View()
	if strings.Contains(plain, "⟳") {
		t.Fatalf("unexpected spinner glyph in non-busy view: %q", plain)
	}

	m.SetBusyServices(map[string]bool{"ec2": true}, "⟳")
	busy := m.View()
	if !strings.Contains(busy, "⟳") {
		t.Fatalf("expected busy view to contain spinner glyph, got: %q", busy)
	}

	m.SetBusyServices(nil, "")
	cleared := m.View()
	if strings.Contains(cleared, "⟳") {
		t.Fatalf("spinner glyph still present after clearing busy state: %q", cleared)
	}
}
