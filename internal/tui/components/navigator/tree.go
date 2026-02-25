package navigator

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"awscope/internal/cost"
	"awscope/internal/store"
	"awscope/internal/tui/icons"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type RowKind int

const (
	RowService RowKind = iota
	RowType
)

type Row struct {
	Kind    RowKind
	Service string
	Type    string
	Count   int

	KnownUSD     float64
	UnknownCount int
	ShowCost     bool
}

type item struct{ row Row }

func (i item) Title() string       { return "" }
func (i item) Description() string { return "" }
func (i item) FilterValue() string { return i.row.Service + " " + i.row.Type }

type Model struct {
	list list.Model

	services []string

	serviceCounts       map[string]int
	typeCountsByService map[string]map[string]int // cached per service

	pricingMode       bool
	serviceCostByKey  map[string]store.CostAgg
	typeCostByService map[string]map[string]store.CostAgg // cached per service

	selectedService string
	selectedType    string
	expandedService string

	// Called to get fallback types when the DB has no types/counts yet.
	fallbackTypes func(service string) []string

	styles Styles

	icons icons.Set
}

type Styles struct {
	Arrow    lipgloss.Style
	Icon     lipgloss.Style
	IconDim  lipgloss.Style
	Service  lipgloss.Style
	Type     lipgloss.Style
	Count    lipgloss.Style
	Selected lipgloss.Style
}

func New(services []string, fallbackTypes func(service string) []string) Model {
	l := list.New(nil, treeDelegate{styles: Styles{}}, 20, 10)
	l.SetShowHelp(false)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	m := Model{
		list:                l,
		services:            append([]string(nil), services...),
		serviceCounts:       map[string]int{},
		typeCountsByService: map[string]map[string]int{},
		serviceCostByKey:    map[string]store.CostAgg{},
		typeCostByService:   map[string]map[string]store.CostAgg{},
		fallbackTypes:       fallbackTypes,
		styles:              Styles{},
		icons:               icons.New(icons.ModeNerd),
	}
	sort.Strings(m.services)
	m.rebuild()
	return m
}

func (m *Model) SetSize(width, height int) {
	m.list.SetSize(width, height)
}

func (m *Model) SetStyles(s Styles) {
	m.styles = s
	m.list.SetDelegate(treeDelegate{styles: s, icons: m.icons})
}

func (m *Model) SetIcons(set icons.Set) {
	if set == nil {
		return
	}
	m.icons = set
	m.list.SetDelegate(treeDelegate{styles: m.styles, icons: set})
}

func (m *Model) SetServiceCounts(rows []store.ServiceCount) {
	m.serviceCounts = map[string]int{}
	for _, r := range rows {
		if strings.TrimSpace(r.Service) == "" {
			continue
		}
		m.serviceCounts[r.Service] = r.Count
	}
	m.rebuild()
}

func (m *Model) SetTypeCounts(service string, rows []store.TypeCount) {
	if strings.TrimSpace(service) == "" {
		return
	}
	counts := map[string]int{}
	for _, r := range rows {
		if strings.TrimSpace(r.Type) == "" {
			continue
		}
		counts[r.Type] = r.Count
	}
	m.typeCountsByService[service] = counts
	m.rebuild()
}

func (m *Model) SetPricingMode(on bool) {
	m.pricingMode = on
	m.rebuild()
	m.selectCurrent()
}

func (m *Model) SetServiceCostAgg(rows []store.CostAgg) {
	m.serviceCostByKey = map[string]store.CostAgg{}
	for _, r := range rows {
		if strings.TrimSpace(r.Key) == "" {
			continue
		}
		m.serviceCostByKey[r.Key] = r
	}
	m.rebuild()
}

func (m *Model) SetTypeCostAgg(service string, rows []store.CostAgg) {
	service = strings.TrimSpace(service)
	if service == "" {
		return
	}
	if m.typeCostByService == nil {
		m.typeCostByService = map[string]map[string]store.CostAgg{}
	}
	mm := map[string]store.CostAgg{}
	for _, r := range rows {
		if strings.TrimSpace(r.Key) == "" {
			continue
		}
		mm[r.Key] = r
	}
	m.typeCostByService[service] = mm
	m.rebuild()
}

func (m *Model) SetSelection(service, typ string) {
	m.selectedService = strings.TrimSpace(service)
	m.selectedType = strings.TrimSpace(typ)
	m.rebuild()
	m.selectCurrent()
}

func (m *Model) ToggleExpandedService(service string) {
	service = strings.TrimSpace(service)
	if service == "" {
		return
	}
	if m.expandedService == service {
		m.expandedService = ""
	} else {
		m.expandedService = service
	}
	m.rebuild()
	m.selectCurrent()
}

func (m *Model) CollapseService(service string) {
	service = strings.TrimSpace(service)
	if service == "" {
		return
	}
	if m.expandedService == service {
		m.expandedService = ""
		m.rebuild()
	}
	// Select the service header if it exists.
	m.selectedService = service
	m.selectedType = ""
	m.selectCurrent()
}

func (m *Model) CollapseToService() {
	if m.selectedService == "" {
		return
	}
	m.selectedType = ""
	m.rebuild()
	m.selectCurrent()
}

func (m Model) SelectedRow() (Row, bool) {
	it := m.list.SelectedItem()
	ii, ok := it.(item)
	if !ok {
		return Row{}, false
	}
	return ii.row, true
}

func (m Model) ExpandedService() string { return m.expandedService }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	return m.list.View()
}

// FirstTypeForService returns the first type row that would be rendered for a
// service after merging DB types and fallback types, using the same ordering as
// the navigator list.
func (m Model) FirstTypeForService(service string) string {
	service = strings.TrimSpace(service)
	if service == "" {
		return ""
	}
	typeSet := map[string]bool{}
	if typeCounts := m.typeCountsByService[service]; typeCounts != nil {
		for typ := range typeCounts {
			if strings.TrimSpace(typ) != "" {
				typeSet[typ] = true
			}
		}
	}
	if m.fallbackTypes != nil {
		for _, typ := range m.fallbackTypes(service) {
			typ = strings.TrimSpace(typ)
			if typ != "" {
				typeSet[typ] = true
			}
		}
	}
	if len(typeSet) == 0 {
		return ""
	}
	types := make([]string, 0, len(typeSet))
	for typ := range typeSet {
		types = append(types, typ)
	}
	sort.Strings(types)
	return types[0]
}

func (m *Model) rebuild() {
	items := make([]list.Item, 0, len(m.services)*2)
	for _, svc := range m.services {
		count := m.serviceCounts[svc]
		sr := Row{Kind: RowService, Service: svc, Count: count, ShowCost: m.pricingMode}
		if m.pricingMode {
			if ca, ok := m.serviceCostByKey[svc]; ok {
				sr.KnownUSD = ca.KnownUSD
				sr.UnknownCount = ca.UnknownCount
			}
		}
		items = append(items, item{row: sr})
		if svc != m.expandedService {
			continue
		}

		typeCounts := m.typeCountsByService[svc]
		if typeCounts == nil {
			typeCounts = map[string]int{}
		}

		// Types: merge DB counts with fallback types for this service.
		typeSet := map[string]bool{}
		for typ := range typeCounts {
			typeSet[typ] = true
		}
		if m.fallbackTypes != nil {
			for _, typ := range m.fallbackTypes(svc) {
				typeSet[typ] = true
			}
		}

		var types []string
		for typ := range typeSet {
			if typ != "" {
				types = append(types, typ)
			}
		}
		sort.Strings(types)
		for _, typ := range types {
			tr := Row{Kind: RowType, Service: svc, Type: typ, Count: typeCounts[typ], ShowCost: m.pricingMode}
			if m.pricingMode {
				if mm, ok := m.typeCostByService[svc]; ok {
					if ca, ok := mm[typ]; ok {
						tr.KnownUSD = ca.KnownUSD
						tr.UnknownCount = ca.UnknownCount
					}
				}
			}
			items = append(items, item{row: tr})
		}
	}
	m.list.SetItems(items)
}

func (m *Model) selectCurrent() {
	if m == nil {
		return
	}
	items := m.list.Items()
	if len(items) == 0 {
		m.list.ResetSelected()
		return
	}
	wantSvc := m.selectedService
	wantType := m.selectedType
	if wantSvc == "" {
		return
	}

	best := -1
	for i, it := range items {
		ii, ok := it.(item)
		if !ok {
			continue
		}
		if ii.row.Kind == RowType && wantType != "" && ii.row.Service == wantSvc && ii.row.Type == wantType {
			best = i
			break
		}
		if ii.row.Kind == RowService && ii.row.Service == wantSvc {
			best = i
		}
	}
	if best >= 0 {
		m.list.Select(best)
	}
}

type treeDelegate struct {
	styles Styles
	icons  icons.Set
}

func (d treeDelegate) Height() int                             { return 1 }
func (d treeDelegate) Spacing() int                            { return 0 }
func (d treeDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d treeDelegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	ii, ok := li.(item)
	if !ok {
		return
	}

	isSelected := index == m.Index()
	maxW := m.Width()
	if maxW <= 0 {
		maxW = 80
	}

	row := ii.row
	padTrunc := func(s string, width int) string {
		s = ansi.Truncate(strings.TrimSpace(s), width, "…")
		return fmt.Sprintf("%-*s", width, s)
	}
	switch row.Kind {
	case RowService:
		arrow := "▸"
		// We can't access expanded state directly here; encode it via whether it's followed by types.
		// The Model rebuild ensures a service that is expanded is followed by type rows.
		// We infer expansion by looking ahead one item.
		if index+1 < len(m.Items()) {
			if next, ok := m.Items()[index+1].(item); ok && next.row.Kind == RowType && next.row.Service == row.Service {
				arrow = "▾"
			}
		}
		ico := ""
		if d.icons != nil {
			ico = icons.Pad(d.icons.Service(row.Service), 2)
		}
		plain := fmt.Sprintf("%s %s%s %d", arrow, ico, padTrunc(row.Service, 10), row.Count)
		if row.ShowCost {
			costStr := cost.FormatUSDPerMonthCompact(row.KnownUSD)
			if row.UnknownCount > 0 {
				costStr += fmt.Sprintf(" (+%d unk)", row.UnknownCount)
			}
			plain += "  " + costStr
		}

		if isSelected {
			fmt.Fprint(w, ansi.Truncate(d.styles.Selected.Render(plain), maxW, "…"))
			return
		}

		line := fmt.Sprintf("%s %s %s",
			d.styles.Arrow.Render(arrow),
			func() string {
				ic := ico
				if d.icons == nil {
					ic = icons.Pad("", 2)
				}
				st := d.styles.Icon
				if row.Count == 0 {
					st = d.styles.IconDim
				}
				return st.Render(ic) + d.styles.Service.Render(padTrunc(row.Service, 10))
			}(),
			d.styles.Count.Render(fmt.Sprintf("%d", row.Count)),
		)
		if row.ShowCost {
			costStr := cost.FormatUSDPerMonthCompact(row.KnownUSD)
			if row.UnknownCount > 0 {
				costStr += fmt.Sprintf(" (+%d unk)", row.UnknownCount)
			}
			line += "  " + d.styles.Count.Render(costStr)
		}
		fmt.Fprint(w, ansi.Truncate(line, maxW, "…"))
	case RowType:
		typ := row.Type
		if len(typ) > 0 {
			ico := ""
			if d.icons != nil {
				ico = icons.Pad(d.icons.Type(typ), 2)
			}
			plain := fmt.Sprintf("  %s%s %d", ico, padTrunc(typ, 18), row.Count)
			if row.ShowCost {
				costStr := cost.FormatUSDPerMonthCompact(row.KnownUSD)
				if row.UnknownCount > 0 {
					costStr += fmt.Sprintf(" (+%d unk)", row.UnknownCount)
				}
				plain += "  " + costStr
			}
			if isSelected {
				fmt.Fprint(w, ansi.Truncate(d.styles.Selected.Render(plain), maxW, "…"))
				return
			}

			line := fmt.Sprintf("  %s%s %s",
				func() string {
					ic := ico
					if d.icons == nil {
						ic = icons.Pad("", 2)
					}
					st := d.styles.Icon
					if row.Count == 0 {
						st = d.styles.IconDim
					}
					return st.Render(ic)
				}(),
				d.styles.Type.Render(padTrunc(typ, 18)),
				d.styles.Count.Render(fmt.Sprintf("%d", row.Count)),
			)
			if row.ShowCost {
				costStr := cost.FormatUSDPerMonthCompact(row.KnownUSD)
				if row.UnknownCount > 0 {
					costStr += fmt.Sprintf(" (+%d unk)", row.UnknownCount)
				}
				line += "  " + d.styles.Count.Render(costStr)
			}
			fmt.Fprint(w, ansi.Truncate(line, maxW, "…"))
		}
	}
}
