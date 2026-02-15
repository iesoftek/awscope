package icons

import "testing"

func TestPad_Empty(t *testing.T) {
	if got := Pad("", 2); got != "  " {
		t.Fatalf("got %q", got)
	}
}

func TestPad_Truncate(t *testing.T) {
	if got := Pad("abc", 2); got != "ab" {
		t.Fatalf("got %q", got)
	}
}

func TestPad_PadRight(t *testing.T) {
	if got := Pad("a", 2); got != "a " {
		t.Fatalf("got %q", got)
	}
}

func TestParseMode_DefaultsToNerd(t *testing.T) {
	if got := ParseMode("wat"); got != ModeNerd {
		t.Fatalf("got %q", got)
	}
}
