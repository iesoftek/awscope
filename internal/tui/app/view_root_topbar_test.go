package app

import (
	"strings"
	"testing"

	"awscope/internal/store"
	"awscope/internal/tui/widgets/table"

	"github.com/charmbracelet/lipgloss"
)

func TestTopBarKeepsFixedHeightWithLongSelectedText(t *testing.T) {
	mk := func(name string) model {
		return model{
			build:           "test",
			dbPath:          "/tmp/awscope.sqlite",
			width:           140,
			profileName:     "default",
			accountID:       "123456789012",
			partition:       "aws",
			selectedService: "ec2",
			selectedType:    "ec2:instance",
			selectedRegions: map[string]bool{"us-west-2": true},
			knownRegions:    []string{"us-west-2"},
			resources:       table.New(table.WithColumns(nil), table.WithRows(nil)),
			resourceSummaries: []store.ResourceSummary{
				{
					DisplayName: name,
					Service:     "ec2",
					Type:        "ec2:instance",
					Region:      "us-west-2",
					PrimaryID:   "i-123",
				},
			},
		}
	}

	shortTop := mk("simple-instance").topBar(lipgloss.NewStyle(), 64)
	longName := strings.Repeat("very-long-resource-name-", 24) + "\nwith-newline"
	longTop := mk(longName).topBar(lipgloss.NewStyle(), 64)

	shortLines := len(strings.Split(shortTop, "\n"))
	longLines := len(strings.Split(longTop, "\n"))

	if shortLines != longLines {
		t.Fatalf("top bar line count changed: short=%d long=%d", shortLines, longLines)
	}
	if longLines != 4 {
		t.Fatalf("unexpected top bar height: got %d lines, want 4", longLines)
	}
}

func TestConstrainDetailsBodyLimitsLineCount(t *testing.T) {
	m := model{
		height:     24,
		paneRightW: 60,
	}

	var in []string
	for i := 0; i < 40; i++ {
		in = append(in, "line-"+strings.Repeat("x", 120))
	}
	out := m.constrainDetailsBody(strings.Join(in, "\n"))

	gotLines := strings.Split(out, "\n")
	wantMax := max(4, m.height-10)
	if len(gotLines) != wantMax {
		t.Fatalf("constrainDetailsBody line count = %d, want %d", len(gotLines), wantMax)
	}
	if !strings.Contains(gotLines[len(gotLines)-1], "(+") {
		t.Fatalf("expected overflow marker on last line, got %q", gotLines[len(gotLines)-1])
	}
}

func TestComposeFrameKeepsExactHeightAndSingleFilterLine(t *testing.T) {
	m := model{width: 80, height: 20}

	top := strings.Join([]string{"t1", "t2", "t3", "t4"}, "\n")
	filter := "filter line\nshould not show"
	body := strings.Join([]string{"b1", "b2", "b3", "b4", "b5", "b6", "b7", "b8", "b9", "b10", "b11", "b12", "b13", "b14", "b15", "b16", "b17", "b18", "b19", "b20"}, "\n")
	status := strings.Join([]string{"s1", "s2"}, "\n")

	out := m.composeFrame(top, filter, body, status)
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Fatalf("composeFrame line count = %d, want 20", len(lines))
	}
	if lines[4] != "filter line" {
		t.Fatalf("filter line mismatch: got %q", lines[4])
	}
	if lines[len(lines)-2] != "s1" || lines[len(lines)-1] != "s2" {
		t.Fatalf("status lines not anchored at bottom: tail=%q", lines[len(lines)-2:])
	}
}
