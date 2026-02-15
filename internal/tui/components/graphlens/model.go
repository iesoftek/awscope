package graphlens

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"awscope/internal/store"
	"awscope/internal/tui/icons"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Side int

const (
	SideIn Side = iota
	SideOut
)

type ItemKind int

const (
	ItemGroup ItemKind = iota
	ItemNeighbor
)

type GroupKey struct {
	Dir  string // "in" or "out"
	Kind string // display kind (incoming already reversed for display)
}

func (k GroupKey) String() string { return k.Dir + "|" + k.Kind }

type SelectionKind int

const (
	SelectionNone SelectionKind = iota
	SelectionGroup
	SelectionNeighbor
)

type Selection struct {
	Kind SelectionKind
	Side Side
	Dir  string

	Group GroupKey
	Count int
	Items []string
	More  int

	Neighbor store.Neighbor
}

type item struct {
	kind     ItemKind
	groupKey GroupKey
	count    int
	neighbor store.Neighbor
}

func (i item) Title() string       { return "" }
func (i item) Description() string { return "" }
func (i item) FilterValue() string {
	return i.groupKey.Kind + " " + i.neighbor.DisplayName + " " + string(i.neighbor.OtherKey)
}

type Model struct {
	in  list.Model
	out list.Model

	focus Side

	expanded map[string]bool

	// Cached group neighbor lists for summary and expansion.
	groups map[string][]store.Neighbor
	counts map[string]int

	styles Styles

	icons icons.Set
}

type Styles struct {
	Group    lipgloss.Style
	Neighbor lipgloss.Style
	Meta     lipgloss.Style
	Arrow    lipgloss.Style
	Selected lipgloss.Style
	Icon     lipgloss.Style
}

func New() Model {
	ld := lensDelegate{styles: Styles{}, icons: icons.New(icons.ModeNerd)}
	in := list.New(nil, ld, 20, 10)
	out := list.New(nil, ld, 20, 10)

	for _, l := range []*list.Model{&in, &out} {
		l.SetShowHelp(false)
		l.SetShowTitle(false)
		l.SetShowStatusBar(false)
		l.SetFilteringEnabled(false)
		l.DisableQuitKeybindings()
	}

	return Model{
		in:       in,
		out:      out,
		focus:    SideOut,
		expanded: map[string]bool{},
		groups:   map[string][]store.Neighbor{},
		counts:   map[string]int{},
		styles:   Styles{},
		icons:    icons.New(icons.ModeNerd),
	}
}

func (m *Model) SetSize(inW, outW, h int) {
	m.in.SetSize(inW, h)
	m.out.SetSize(outW, h)
}

func (m *Model) SetStyles(s Styles) {
	m.styles = s
	m.in.SetDelegate(lensDelegate{styles: s, icons: m.icons})
	m.out.SetDelegate(lensDelegate{styles: s, icons: m.icons})
}

func (m *Model) SetIcons(set icons.Set) {
	if set == nil {
		return
	}
	m.icons = set
	m.in.SetDelegate(lensDelegate{styles: m.styles, icons: set})
	m.out.SetDelegate(lensDelegate{styles: m.styles, icons: set})
}

func (m *Model) SetFocus(side Side) {
	m.focus = side
}

func (m Model) Focus() Side { return m.focus }

func (m *Model) FocusLeft()  { m.focus = SideIn }
func (m *Model) FocusRight() { m.focus = SideOut }

func (m *Model) ToggleGroup() {
	it, ok := m.selectedItem()
	if !ok || it.kind != ItemGroup {
		return
	}
	k := it.groupKey.String()
	m.expanded[k] = !m.expanded[k]
	m.rebuild()
}

func (m *Model) ExpandGroup(dir, kind string, on bool) {
	k := (GroupKey{Dir: dir, Kind: kind}).String()
	m.expanded[k] = on
	m.rebuild()
}

func (m *Model) SetExpanded(keys []string) {
	m.expanded = map[string]bool{}
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		m.expanded[k] = true
	}
	m.rebuild()
}

func (m Model) ExpandedKeys() []string {
	var out []string
	for k, v := range m.expanded {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (m *Model) SetCursors(inCursor, outCursor int) {
	if inCursor >= 0 {
		m.in.Select(inCursor)
	}
	if outCursor >= 0 {
		m.out.Select(outCursor)
	}
}

func (m Model) Cursors() (int, int) {
	return m.in.Index(), m.out.Index()
}

func (m *Model) SetNeighbors(neighbors []store.Neighbor, reverseKind func(kind string) string) {
	m.groups = map[string][]store.Neighbor{}
	m.counts = map[string]int{}

	for _, n := range neighbors {
		dir := n.Dir
		kind := n.Kind
		if dir == "in" && reverseKind != nil {
			kind = reverseKind(kind)
		}
		gk := GroupKey{Dir: dir, Kind: kind}
		ks := gk.String()
		m.groups[ks] = append(m.groups[ks], n)
	}
	for k, xs := range m.groups {
		m.counts[k] = len(xs)
		sort.Slice(xs, func(i, j int) bool {
			ai := xs[i].DisplayName
			aj := xs[j].DisplayName
			if ai != aj {
				return ai < aj
			}
			return xs[i].PrimaryID < xs[j].PrimaryID
		})
		m.groups[k] = xs
	}

	m.rebuild()
}

func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.focus == SideIn {
		m.in, cmd = m.in.Update(msg)
	} else {
		m.out, cmd = m.out.Update(msg)
	}
	return *m, cmd
}

func (m Model) View(inBorder, outBorder lipgloss.Style) string {
	inView := inBorder.Render("Incoming\n" + m.in.View())
	outView := outBorder.Render("Outgoing\n" + m.out.View())
	return lipgloss.JoinHorizontal(lipgloss.Top, inView, outView)
}

func (m Model) IncomingView() string { return m.in.View() }
func (m Model) OutgoingView() string { return m.out.View() }

func (m Model) Selected() Selection {
	it, ok := m.selectedItem()
	if !ok {
		return Selection{Kind: SelectionNone}
	}
	side := m.focus
	if it.kind == ItemNeighbor {
		return Selection{
			Kind:     SelectionNeighbor,
			Side:     side,
			Dir:      it.groupKey.Dir,
			Group:    it.groupKey,
			Neighbor: it.neighbor,
		}
	}

	ks := it.groupKey.String()
	ns := m.groups[ks]
	count := len(ns)
	var names []string
	for _, n := range ns {
		name := strings.TrimSpace(n.DisplayName)
		if name == "" {
			name = strings.TrimSpace(n.PrimaryID)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	const maxSample = 12
	more := 0
	if len(names) > maxSample {
		more = len(names) - maxSample
		names = names[:maxSample]
	}

	return Selection{
		Kind:  SelectionGroup,
		Side:  side,
		Dir:   it.groupKey.Dir,
		Group: it.groupKey,
		Count: count,
		Items: names,
		More:  more,
	}
}

func (m *Model) selectedItem() (item, bool) {
	var it list.Item
	if m.focus == SideIn {
		it = m.in.SelectedItem()
	} else {
		it = m.out.SelectedItem()
	}
	ii, ok := it.(item)
	return ii, ok
}

func (m *Model) rebuild() {
	type groupView struct {
		key   GroupKey
		count int
	}

	var groupsIn []groupView
	var groupsOut []groupView
	for k, n := range m.counts {
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			continue
		}
		gk := GroupKey{Dir: parts[0], Kind: parts[1]}
		gv := groupView{key: gk, count: n}
		if gk.Dir == "in" {
			groupsIn = append(groupsIn, gv)
		} else {
			groupsOut = append(groupsOut, gv)
		}
	}

	sort.Slice(groupsIn, func(i, j int) bool {
		if groupsIn[i].count != groupsIn[j].count {
			return groupsIn[i].count > groupsIn[j].count
		}
		return groupsIn[i].key.Kind < groupsIn[j].key.Kind
	})
	sort.Slice(groupsOut, func(i, j int) bool {
		if groupsOut[i].count != groupsOut[j].count {
			return groupsOut[i].count > groupsOut[j].count
		}
		return groupsOut[i].key.Kind < groupsOut[j].key.Kind
	})

	inItems := make([]list.Item, 0, len(groupsIn)*2)
	for _, g := range groupsIn {
		inItems = append(inItems, item{kind: ItemGroup, groupKey: g.key, count: g.count})
		if !m.expanded[g.key.String()] {
			continue
		}
		for _, n := range m.groups[g.key.String()] {
			inItems = append(inItems, item{kind: ItemNeighbor, groupKey: g.key, neighbor: n})
		}
	}

	outItems := make([]list.Item, 0, len(groupsOut)*2)
	for _, g := range groupsOut {
		outItems = append(outItems, item{kind: ItemGroup, groupKey: g.key, count: g.count})
		if !m.expanded[g.key.String()] {
			continue
		}
		for _, n := range m.groups[g.key.String()] {
			outItems = append(outItems, item{kind: ItemNeighbor, groupKey: g.key, neighbor: n})
		}
	}

	inIdx := m.in.Index()
	outIdx := m.out.Index()
	m.in.SetItems(inItems)
	m.out.SetItems(outItems)
	if inIdx >= 0 {
		m.in.Select(min(inIdx, max(0, len(inItems)-1)))
	}
	if outIdx >= 0 {
		m.out.Select(min(outIdx, max(0, len(outItems)-1)))
	}
}

type lensDelegate struct {
	styles Styles
	icons  icons.Set
}

func (d lensDelegate) Height() int  { return 1 }
func (d lensDelegate) Spacing() int { return 0 }
func (d lensDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {
	return nil
}

func (d lensDelegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	ii, ok := li.(item)
	if !ok {
		return
	}

	isSelected := index == m.Index()

	switch ii.kind {
	case ItemGroup:
		arrow := "▸"
		if index+1 < len(m.Items()) {
			if next, ok := m.Items()[index+1].(item); ok && next.kind == ItemNeighbor && next.groupKey == ii.groupKey {
				arrow = "▾"
			}
		}
		relIcon := ""
		if d.icons != nil {
			relIcon = icons.Pad(d.icons.Relationship(ii.groupKey.Kind, ii.groupKey.Dir), 2)
		} else {
			relIcon = icons.Pad("", 2)
		}
		line := fmt.Sprintf("%s %s%s (%d)", arrow, relIcon, ii.groupKey.Kind, ii.count)
		if isSelected {
			fmt.Fprint(w, d.styles.Selected.Render(line))
			return
		}
		fmt.Fprintf(w, "%s %s%s",
			d.styles.Arrow.Render(arrow),
			d.styles.Icon.Render(relIcon),
			d.styles.Group.Render(fmt.Sprintf("%s (%d)", ii.groupKey.Kind, ii.count)),
		)
	case ItemNeighbor:
		name := strings.TrimSpace(ii.neighbor.DisplayName)
		if name == "" {
			name = strings.TrimSpace(ii.neighbor.PrimaryID)
		}
		meta := strings.TrimSpace(fmt.Sprintf("%s %s", ii.neighbor.Type, ii.neighbor.Region))
		if isSelected {
			fmt.Fprint(w, d.styles.Selected.Render(fmt.Sprintf("  %s (%s)", name, meta)))
			return
		}
		fmt.Fprintf(w, "  %s %s",
			d.styles.Neighbor.Render(name),
			d.styles.Meta.Render(fmt.Sprintf("(%s)", meta)),
		)
	}
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
