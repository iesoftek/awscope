package app

import (
	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/widgets/table"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

func (m model) regionsModalView() string {
	return m.styles.PaneBorderFocus.Render("Select regions (space toggle, enter apply, esc cancel)\n" + m.regions.View())
}

func (m model) actionsModalView() string {
	return m.styles.PaneBorderFocus.Render("Select action (enter), esc cancel\n" + m.actions.View())
}

func (m model) helpModalView() string {
	extra := strings.Join([]string{
		"Navigator:",
		"  up/down: move",
		"  enter/right: expand/collapse service; or focus resources on type",
		"  left: collapse service",
		"",
		"Panes:",
		"  1: navigator",
		"  2: resources",
		"  3: details",
		"",
		"Details Tabs:",
		"  s: summary",
		"  r: related",
		"  x: raw",
		"",
		"Audit (E):",
		"  full-screen create/delete events (last 7d, indexed by scan)",
		"  /: text filter | F: facets | T: window | e: errors-only",
		"  [ ]: cursor page | +/-: page size | enter: detail | J: jump to resource",
		"  esc: detail->list, list->close",
		"",
		"Graph Lens (g):",
		"  h/l or left/right: focus incoming/outgoing",
		"  space/enter: expand/collapse group",
		"  enter: traverse neighbor (when selected)",
		"  backspace: back",
		"",
	}, "\n")
	return m.styles.PaneBorderFocus.Render("Help (esc to close)\n" + extra + m.help.View(m.keys))
}

func (m model) confirmModalView() string {
	sel, ok := m.activeSelection()
	target := ""
	if ok {
		target = sel.PrimaryID
	}
	prompt := fmt.Sprintf("Confirm action %s on %s\nType %s and press enter", m.pendingAct, target, target)
	return m.styles.PaneBorderFocus.Render(prompt + "\n" + m.confirm.View())
}

func (m *model) syncNavigatorSelection() {
	if m == nil || strings.TrimSpace(m.selectedService) == "" {
		return
	}
	// Ensure the selected type is visible in the tree.
	if m.selectedType != "" && m.nav.ExpandedService() != m.selectedService {
		m.nav.ToggleExpandedService(m.selectedService)
	}
	m.nav.SetSelection(m.selectedService, m.selectedType)
}

func (m *model) setContextFromKey(key graph.ResourceKey) {
	_, _, region, resourceType, _, err := graph.ParseResourceKey(key)
	if err != nil {
		return
	}
	if parts := strings.SplitN(resourceType, ":", 2); len(parts) > 0 && parts[0] != "" {
		m.selectedService = parts[0]
	}
	if resourceType != "" {
		m.selectedType = resourceType
	}
	m.applyECSSelection(m.selectedService, m.selectedType, false)
	if region != "" {
		m.selectedRegions = map[string]bool{region: true}
		m.applyServiceScope()

		// Keep region selector usable even if the DB was empty.
		found := false
		for _, r := range m.knownRegions {
			if r == region {
				found = true
				break
			}
		}
		if !found {
			m.knownRegions = append(m.knownRegions, region)
			sort.Strings(m.knownRegions)
		}
	}
	m.syncNavigatorSelection()
}

func (m *model) pushNav() {
	selectedKey := graph.ResourceKey("")
	if !m.graphMode {
		if s, ok := m.selectedSummary(); ok {
			selectedKey = s.Key
		}
	}
	inCur, outCur := m.lens.Cursors()
	m.navStack = append(m.navStack, navFrame{
		service:      m.selectedService,
		typ:          m.selectedType,
		regions:      copyBoolMap(m.selectedRegions),
		filter:       m.filters().Resource,
		page:         m.pager.Page,
		selectedKey:  selectedKey,
		tab:          m.detailsTab,
		graphMode:    m.graphMode,
		graphRootKey: m.graphRootKey,

		expandedService: m.nav.ExpandedService(),
		lensFocus:       m.lens.Focus(),
		lensInCursor:    inCur,
		lensOutCursor:   outCur,
		lensExpanded:    m.lens.ExpandedKeys(),
		ecsDrill:        m.ecsDrill,
	})
}

func (m *model) restoreNav() {
	if len(m.navStack) == 0 {
		return
	}
	last := m.navStack[len(m.navStack)-1]
	m.navStack = m.navStack[:len(m.navStack)-1]

	m.selectedService = last.service
	m.selectedType = last.typ
	m.selectedRegions = copyBoolMap(last.regions)
	m.applyServiceScope()
	m.syncNavigatorSelection()
	m.filter.SetValue(last.filter)
	m.pager.Page = last.page
	m.detailsTab = last.tab
	m.graphMode = last.graphMode
	m.graphRootKey = last.graphRootKey
	switch {
	case last.expandedService == "" && m.nav.ExpandedService() != "":
		// Collapse whatever is currently expanded.
		m.nav.ToggleExpandedService(m.nav.ExpandedService())
	case last.expandedService != "" && last.expandedService != m.nav.ExpandedService():
		m.nav.ToggleExpandedService(last.expandedService)
	}
	if last.lensFocus == graphlens.SideIn || last.lensFocus == graphlens.SideOut {
		m.lens.SetFocus(last.lensFocus)
	}
	m.lens.SetExpanded(last.lensExpanded)
	m.lens.SetCursors(last.lensInCursor, last.lensOutCursor)
	m.ecsDrill = last.ecsDrill
	m.focus = focusResources
	m.pendingSelectKey = last.selectedKey
}

func (m *model) jumpTo(service, region string, key graph.ResourceKey) {
	parsedType := ""
	if service == "" || region == "" || m.selectedType == "" {
		_, _, r, resourceType, _, err := graph.ParseResourceKey(key)
		if err == nil {
			if region == "" {
				region = r
			}
			parsedType = resourceType
			if service == "" {
				if parts := strings.SplitN(resourceType, ":", 2); len(parts) > 0 {
					service = parts[0]
				}
			}
		}
	}
	if service == "" {
		return
	}
	if region == "" {
		region = "global"
	}

	m.selectedService = service
	m.syncNavigatorSelection()
	if parsedType != "" {
		m.selectedType = parsedType
	} else if m.selectedType == "" {
		m.selectedType = defaultTypeForService(service)
	}
	m.applyECSSelection(m.selectedService, m.selectedType, false)
	m.selectedRegions = map[string]bool{region: true}
	m.applyServiceScope()

	// Reset view state when jumping.
	m.filter.SetValue("")
	m.pager.Page = 0
	m.detailsTab = detailsSummary
	m.focus = focusResources
	m.pendingSelectKey = key

	// Make sure we can render the region selector sensibly even if the DB was empty.
	found := false
	for _, r := range m.knownRegions {
		if r == region {
			found = true
			break
		}
	}
	if !found {
		m.knownRegions = append(m.knownRegions, region)
		sort.Strings(m.knownRegions)
	}
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

type regionItem struct{ name string }

func (i regionItem) Title() string       { return i.name }
func (i regionItem) Description() string { return "" }
func (i regionItem) FilterValue() string { return i.name }

func (m model) regionItems() []list.Item {
	items := make([]list.Item, 0, len(m.knownRegions))
	for _, r := range m.knownRegions {
		prefix := "[ ] "
		if m.selectedRegions != nil && m.selectedRegions[r] {
			prefix = "[x] "
		}
		items = append(items, regionItem{name: prefix + r})
	}
	return items
}

func (m model) regionPickerStats() (selected, total int) {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return 1, 1
	}
	selected = len(m.selectedRegionSlice())
	includeGlobal := ok && p.Scope() == providers.ScopeAccount &&
		(m.selectedRegions["global"] || m.regionSvcCounts["global"] > 0 || m.regionTypeCounts["global"] > 0)
	for _, r := range m.knownRegions {
		if r == "global" && !includeGlobal {
			continue
		}
		total++
	}
	if total == 0 {
		total = selected
	}
	return selected, total
}

func (m *model) currentRegionInPicker() (string, bool) {
	if m == nil || len(m.regionPickerOrder) == 0 {
		return "", false
	}
	i := m.regionTable.Cursor()
	if i < 0 || i >= len(m.regionPickerOrder) {
		return "", false
	}
	return m.regionPickerOrder[i], true
}

func (m *model) rebuildRegionTable(keepRegion string) {
	if m == nil {
		return
	}
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		// Global-scoped service: only allow global.
		m.regionPickerOrder = []string{"global"}
		m.regionTable.SetRows([]table.Row{{"x", "global", fmt.Sprintf("%d", m.regionSvcCounts["global"]), fmt.Sprintf("%d", m.regionTypeCounts["global"])}})
		m.regionTable.SetCursor(0)
		return
	}

	if keepRegion == "" {
		if cur, ok := m.currentRegionInPicker(); ok {
			keepRegion = cur
		}
	}

	if m.regionSvcCounts == nil {
		m.regionSvcCounts = map[string]int{}
	}
	if m.regionTypeCounts == nil {
		m.regionTypeCounts = map[string]int{}
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}

	f := strings.ToLower(strings.TrimSpace(m.regionFilter.Value()))
	var selected []string
	var rest []string
	for _, r := range m.knownRegions {
		if r == "global" {
			// For account-scoped providers, show "global" only if selected or there are resources
			// for the current service/type in global scope.
			if ok && p.Scope() == providers.ScopeAccount {
				if !m.selectedRegions["global"] && m.regionSvcCounts["global"] == 0 && m.regionTypeCounts["global"] == 0 {
					continue
				}
			} else {
				continue
			}
		}
		if f != "" && !strings.Contains(strings.ToLower(r), f) {
			continue
		}
		if m.selectedRegions[r] {
			selected = append(selected, r)
		} else {
			rest = append(rest, r)
		}
	}
	sort.Strings(selected)
	sort.Strings(rest)
	order := append(selected, rest...)
	m.regionPickerOrder = order

	rows := make([]table.Row, 0, len(order))
	for _, r := range order {
		sel := " "
		if m.selectedRegions[r] {
			sel = "x"
		}
		rows = append(rows, table.Row{
			sel,
			r,
			fmt.Sprintf("%d", m.regionSvcCounts[r]),
			fmt.Sprintf("%d", m.regionTypeCounts[r]),
		})
	}
	m.regionTable.SetRows(rows)

	// Keep cursor stable if possible.
	if keepRegion != "" {
		for i := range order {
			if order[i] == keepRegion {
				m.regionTable.SetCursor(i)
				return
			}
		}
	}
	if len(order) > 0 {
		m.regionTable.SetCursor(min(m.regionTable.Cursor(), len(order)-1))
	}
}

func (m *model) toggleSelectedRegionFromPicker() {
	name, ok := m.currentRegionInPicker()
	if !ok {
		return
	}
	m.toggleSelectedRegionByName(name)
}

func (m *model) toggleSelectedRegionByName(name string) {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	// Only allow explicit "global" selection for account-scoped providers.
	if name == "global" && (!ok || p.Scope() != providers.ScopeAccount) {
		return
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}
	if m.selectedRegions[name] {
		delete(m.selectedRegions, name)
	} else {
		m.selectedRegions[name] = true
	}
	if len(m.selectedRegions) == 0 {
		m.selectedRegions[name] = true
	}
}

func (m *model) selectAllRegions() {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}
	for _, r := range m.knownRegions {
		if r == "global" {
			continue
		}
		m.selectedRegions[r] = true
	}
	if len(m.selectedRegions) == 0 {
		for _, r := range m.defaultRegionsForService() {
			m.selectedRegions[r] = true
		}
	}
}

func (m *model) invertRegions() {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}
	next := map[string]bool{}
	for _, r := range m.knownRegions {
		if r == "global" {
			continue
		}
		if !m.selectedRegions[r] {
			next[r] = true
		}
	}
	if len(next) == 0 {
		// Keep at least one region selected; fall back to current cursor region.
		if cur, ok := m.currentRegionInPicker(); ok {
			next[cur] = true
		}
	}
	m.selectedRegions = next
}

func (m *model) selectOnlyCurrentRegion() {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return
	}
	cur, ok := m.currentRegionInPicker()
	if !ok {
		return
	}
	if cur == "" || cur == "global" {
		return
	}
	m.selectedRegions = map[string]bool{cur: true}
}

func (m *model) toggleSelectedRegion() {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return
	}
	it := m.regions.SelectedItem()
	ri, ok := it.(regionItem)
	if !ok {
		return
	}
	name := ri.name
	if strings.HasPrefix(name, "[x] ") {
		name = strings.TrimPrefix(name, "[x] ")
	} else if strings.HasPrefix(name, "[ ] ") {
		name = strings.TrimPrefix(name, "[ ] ")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}
	if m.selectedRegions[name] {
		delete(m.selectedRegions, name)
	} else {
		m.selectedRegions[name] = true
	}
	if len(m.selectedRegions) == 0 {
		m.selectedRegions[name] = true
	}
}

func (m *model) applyServiceScope() {
	p, ok := registry.Get(m.selectedService)
	if !ok {
		return
	}
	if m.selectedRegions == nil {
		m.selectedRegions = map[string]bool{}
	}
	switch p.Scope() {
	case providers.ScopeGlobal:
		m.selectedRegions = map[string]bool{"global": true}
	default:
		if len(m.selectedRegions) == 1 && m.selectedRegions["global"] {
			m.selectedRegions = map[string]bool{}
			for _, r := range m.defaultRegionsForService() {
				m.selectedRegions[r] = true
			}
		}
	}
}

func (m model) defaultRegionsForService() []string {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return []string{"global"}
	}
	var out []string
	for _, r := range m.knownRegions {
		if r == "global" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (m model) selectedRegionSlice() []string {
	p, ok := registry.Get(m.selectedService)
	if ok && p.Scope() == providers.ScopeGlobal {
		return []string{"global"}
	}
	var out []string
	for r, on := range m.selectedRegions {
		if on {
			// Regional providers should never include "global" in the applied scope.
			if r == "global" && (!ok || p.Scope() != providers.ScopeAccount) {
				continue
			}
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = m.defaultRegionsForService()
	}
	sort.Strings(out)
	return out
}

func (m model) selectedAuditRegions() []string {
	out := make([]string, 0, len(m.selectedRegions))
	for r, on := range m.selectedRegions {
		if on && r != "" && r != "global" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		for _, r := range m.selectedRegionSlice() {
			if r != "global" {
				out = append(out, r)
			}
		}
	}
	if len(out) == 0 {
		for _, r := range m.knownRegions {
			if r != "global" {
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return dedupeStrings(out)
}
