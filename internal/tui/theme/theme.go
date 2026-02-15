package theme

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type ThemeID string

const (
	ThemeAuto         ThemeID = "auto"
	ThemeClassic      ThemeID = "classic"
	ThemeHighContrast ThemeID = "high-contrast"
)

type Palette struct {
	Text        lipgloss.Color
	TextDim     lipgloss.Color
	Accent      lipgloss.Color
	Border      lipgloss.Color
	BorderFocus lipgloss.Color
	Good        lipgloss.Color
	Warn        lipgloss.Color
	Bad         lipgloss.Color
}

type Theme struct {
	ID      ThemeID
	Name    string
	Palette Palette
}

type Styles struct {
	// Titles/headers.
	Header      lipgloss.Style
	HeaderFocus lipgloss.Style

	// Explicit title styles for pane section headers (kept separate for clarity/future changes).
	Title      lipgloss.Style
	TitleFocus lipgloss.Style

	PaneBorder      lipgloss.Style
	PaneBorderFocus lipgloss.Style

	MetaBox lipgloss.Style

	// Content readability.
	Label    lipgloss.Style
	Value    lipgloss.Style
	Selected lipgloss.Style
	Count    lipgloss.Style
	Divider  lipgloss.Style

	// Table styles. (We keep these as lipgloss styles and map them onto bubbles/table.Styles in the app.)
	TableHeader      lipgloss.Style
	TableCell        lipgloss.Style
	TableSelectedRow lipgloss.Style

	Dim  lipgloss.Style
	Good lipgloss.Style
	Warn lipgloss.Style
	Bad  lipgloss.Style

	Icon    lipgloss.Style
	IconDim lipgloss.Style
}

type Manager struct {
	noColor bool

	themes []Theme
	cur    int

	styles Styles
}

type Options struct {
	Theme   string
	NoColor bool
}

func NewManager(opts Options) (*Manager, error) {
	m := &Manager{
		noColor: opts.NoColor || envNoColor(),
		themes:  builtinThemes(),
		cur:     0,
	}

	id := ThemeID(strings.TrimSpace(opts.Theme))
	if id == "" {
		if env := strings.TrimSpace(os.Getenv("AWSCOPE_THEME")); env != "" {
			id = ThemeID(env)
		} else {
			id = ThemeAuto
		}
	}

	if err := m.SetTheme(id); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Theme() Theme {
	if m.cur < 0 || m.cur >= len(m.themes) {
		return Theme{ID: ThemeAuto, Name: "Auto"}
	}
	return m.themes[m.cur]
}

func (m *Manager) Styles() Styles {
	return m.styles
}

func (m *Manager) SetTheme(id ThemeID) error {
	for i, t := range m.themes {
		if t.ID == id {
			m.cur = i
			m.styles = buildStyles(t, m.noColor)
			return nil
		}
	}
	return fmt.Errorf("unknown theme %q", id)
}

func (m *Manager) CycleNext() {
	if len(m.themes) == 0 {
		return
	}
	m.cur = (m.cur + 1) % len(m.themes)
	m.styles = buildStyles(m.themes[m.cur], m.noColor)
}

func builtinThemes() []Theme {
	return []Theme{
		{
			ID:   ThemeAuto,
			Name: "Auto",
			Palette: Palette{
				Text:        lipgloss.Color("252"),
				TextDim:     lipgloss.Color("245"),
				Accent:      lipgloss.Color("205"),
				Border:      lipgloss.Color("238"),
				BorderFocus: lipgloss.Color("205"),
				Good:        lipgloss.Color("42"),
				Warn:        lipgloss.Color("214"),
				Bad:         lipgloss.Color("203"),
			},
		},
		{
			ID:   ThemeClassic,
			Name: "Classic",
			Palette: Palette{
				Text:        lipgloss.Color("252"),
				TextDim:     lipgloss.Color("245"),
				Accent:      lipgloss.Color("69"),
				Border:      lipgloss.Color("238"),
				BorderFocus: lipgloss.Color("69"),
				Good:        lipgloss.Color("42"),
				Warn:        lipgloss.Color("214"),
				Bad:         lipgloss.Color("203"),
			},
		},
		{
			ID:   ThemeHighContrast,
			Name: "High Contrast",
			Palette: Palette{
				Text:        lipgloss.Color("15"),
				TextDim:     lipgloss.Color("250"),
				Accent:      lipgloss.Color("12"),
				Border:      lipgloss.Color("15"),
				BorderFocus: lipgloss.Color("12"),
				Good:        lipgloss.Color("10"),
				Warn:        lipgloss.Color("11"),
				Bad:         lipgloss.Color("9"),
			},
		},
	}
}

func buildStyles(t Theme, noColor bool) Styles {
	p := t.Palette
	if noColor {
		p = Palette{} // no explicit colors
	}

	header := lipgloss.NewStyle().Bold(true)
	if p.Text != "" {
		header = header.Foreground(p.Text)
	}
	headerFocus := lipgloss.NewStyle().Bold(true)
	if p.Accent != "" {
		headerFocus = headerFocus.Foreground(p.Accent)
	}

	title := header
	titleFocus := headerFocus

	pane := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if p.Border != "" {
		pane = pane.BorderForeground(p.Border)
	}
	paneFocus := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if p.BorderFocus != "" {
		paneFocus = paneFocus.BorderForeground(p.BorderFocus)
	}

	meta := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if p.Border != "" {
		meta = meta.BorderForeground(p.Border)
	}

	value := lipgloss.NewStyle()
	if p.Text != "" {
		value = value.Foreground(p.Text)
	}

	dim := lipgloss.NewStyle()
	if p.TextDim != "" {
		dim = dim.Foreground(p.TextDim)
	}

	label := dim
	count := dim
	divider := dim

	selected := lipgloss.NewStyle().Bold(true)
	if p.Accent != "" {
		selected = selected.Foreground(p.Accent)
	}

	tableHeader := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	if p.Accent != "" {
		tableHeader = tableHeader.Foreground(p.Accent)
	} else if p.Text != "" {
		tableHeader = tableHeader.Foreground(p.Text)
	}
	tableCell := lipgloss.NewStyle().Padding(0, 1)
	if p.Text != "" {
		tableCell = tableCell.Foreground(p.Text)
	}
	// Selected table rows are rendered per-cell, so this style should be a cell style:
	// keep padding aligned with TableCell and add a background for clear selection.
	tableSelected := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	if p.Text != "" {
		tableSelected = tableSelected.Foreground(p.Text)
	} else if p.Accent != "" {
		tableSelected = tableSelected.Foreground(p.Accent)
	}
	// Theme-driven background: prefer border surface; in high contrast, use Accent to ensure visibility.
	if !noColor {
		bg := p.Border
		if t.ID == ThemeHighContrast && p.Accent != "" {
			bg = p.Accent
		}
		if bg != "" {
			tableSelected = tableSelected.Background(bg)
		}
	}

	good := lipgloss.NewStyle()
	if p.Good != "" {
		good = good.Foreground(p.Good)
	}
	warn := lipgloss.NewStyle()
	if p.Warn != "" {
		warn = warn.Foreground(p.Warn)
	}
	bad := lipgloss.NewStyle()
	if p.Bad != "" {
		bad = bad.Foreground(p.Bad)
	}

	icon := lipgloss.NewStyle()
	if p.Accent != "" {
		icon = icon.Foreground(p.Accent)
	} else if p.Text != "" {
		icon = icon.Foreground(p.Text)
	}
	iconDim := dim

	return Styles{
		Header:           header,
		HeaderFocus:      headerFocus,
		Title:            title,
		TitleFocus:       titleFocus,
		PaneBorder:       pane,
		PaneBorderFocus:  paneFocus,
		MetaBox:          meta,
		Label:            label,
		Value:            value,
		Selected:         selected,
		Count:            count,
		Divider:          divider,
		TableHeader:      tableHeader,
		TableCell:        tableCell,
		TableSelectedRow: tableSelected,
		Dim:              dim,
		Good:             good,
		Warn:             warn,
		Bad:              bad,
		Icon:             icon,
		IconDim:          iconDim,
	}
}

func envNoColor() bool {
	// https://no-color.org/ (common convention)
	_, ok := os.LookupEnv("NO_COLOR")
	return ok
}
