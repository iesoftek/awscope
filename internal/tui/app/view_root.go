package app

import (
	"awscope/internal/cost"
	"awscope/internal/graph"
	"awscope/internal/store"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/icons"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.actionStreamOpen {
		return m.actionStreamFullScreenView()
	}
	if m.auditOpen {
		return m.auditFullScreenView()
	}

	// Pane headers should be visually distinct from content and reflect focus.
	headerStyle := m.styles.Title
	focusStyle := m.styles.TitleFocus

	resTitle := "Resources"
	if m.graphMode {
		resTitle = "Graph Lens"
	}
	resHeader := headerStyle.Render(resTitle)
	detHeader := headerStyle.Render("Details")

	navHeader := headerStyle.Render("Navigator")
	if m.focus == focusServices {
		navHeader = focusStyle.Render("Navigator")
	}
	if m.focus == focusResources {
		if m.graphMode {
			resHeader = focusStyle.Render(fmt.Sprintf("Graph Lens (%d)", len(m.neighbors)))
		} else if m.totalResources > 0 {
			resHeader = focusStyle.Render(fmt.Sprintf("Resources %s (%d)", m.pager.View(), m.totalResources))
		} else {
			resHeader = focusStyle.Render("Resources")
		}
	}
	if m.focus == focusFilter {
		resHeader = focusStyle.Render("Resources (filter)")
	}
	if m.focus == focusRegions {
		resHeader = focusStyle.Render("Regions")
	}
	if m.focus == focusActions || m.focus == focusConfirm {
		resHeader = focusStyle.Render("Actions")
	}
	if m.focus == focusAudit || m.focus == focusAuditFilter || m.focus == focusAuditFacets {
		if m.auditTotal > 0 {
			resHeader = focusStyle.Render(fmt.Sprintf("Audit Events %s (%d)", m.auditPager.View(), m.auditTotal))
		} else {
			resHeader = focusStyle.Render("Audit Events")
		}
	}
	if m.focus == focusDetails {
		tab := "Summary"
		if m.detailsTab == detailsRelationships {
			tab = "Related"
		} else if m.detailsTab == detailsRaw {
			tab = "Raw"
		}
		detHeader = focusStyle.Render("Details (" + tab + ")")
	}

	filterLine := fmt.Sprintf("filter: %s", m.filter.View())
	if m.auditOpen {
		filterLine = fmt.Sprintf("audit: %s", m.auditFilter.View())
	}

	leftBorder := m.styles.PaneBorder
	midBorder := m.styles.PaneBorder
	rightBorder := m.styles.PaneBorder
	if m.focus == focusServices {
		leftBorder = m.styles.PaneBorderFocus
	} else if m.focus == focusResources || m.focus == focusFilter || m.focus == focusRegions || m.focus == focusActions || m.focus == focusConfirm || m.focus == focusAudit || m.focus == focusAuditFilter || m.focus == focusAuditFacets {
		midBorder = m.styles.PaneBorderFocus
	} else {
		rightBorder = m.styles.PaneBorderFocus
	}

	leftPane := leftBorder.Render(navHeader + "\n" + m.nav.View())
	if m.paneLeftW > 0 {
		leftPane = leftBorder.Width(max(0, m.paneLeftW-2)).Render(navHeader + "\n" + m.nav.View())
	}

	// Center pane "overlay" pickers (regions/actions/confirm/help) should not add extra lines
	// above the layout, otherwise the modal can scroll off-screen on small terminals.
	midView := ""
	switch {
	case m.showHelp:
		resHeader = focusStyle.Render("Help")
		extra := strings.Join([]string{
			"Navigator:",
			"  up/down: move",
			"  enter/right: expand/collapse service; or focus resources on type",
			"  left: collapse service",
			"",
			"Regions (R):",
			"  space: toggle",
			"  a: all | i: invert | n: only",
			"  /: find | enter: apply | esc: cancel",
			"",
			"Pricing (p):",
			"  toggle pricing mode (adds navigator totals and table column)",
			"",
			"Audit (E):",
			"  full-screen create/delete CloudTrail events (indexed during scan)",
			"  / text | F facets | T window | e errors-only | +/- page size",
			"  [ ] cursor page | enter detail | J jump | esc back/close",
			"",
			"Icons:",
			"  set AWSCOPE_ICONS=ascii|nerd|none or use --icons (default: nerd)",
			"",
			"Graph Lens (g):",
			"  h/l or left/right: focus incoming/outgoing",
			"  space/enter: expand/collapse group",
			"  enter: traverse neighbor (when selected)",
			"  backspace: back",
			"",
		}, "\n")
		midView = extra + m.help.View(m.keys)
	case m.focus == focusRegions:
		selN, totalN := m.regionPickerStats()
		resHeader = focusStyle.Render(fmt.Sprintf("Regions (%d/%d)", selN, totalN))
		findLabel := "find"
		if m.regionFilterOn {
			findLabel = "find*"
		}
		findVal := strings.TrimSpace(m.regionFilter.Value())
		findView := ""
		if m.regionFilterOn {
			findView = m.regionFilter.View()
		} else if findVal == "" {
			findView = "(press /)"
		} else {
			findView = findVal
		}
		keysLine := "keys: space toggle | s solo+apply | n only | a/* all | i invert | / find | enter apply | esc cancel"
		midView = fmt.Sprintf("%s\n%s: %s\n%s", keysLine, findLabel, findView, m.regionTable.View())
	case m.focus == focusActions:
		resHeader = focusStyle.Render("Actions")
		midView = "Select action (enter), esc cancel\n" + m.actions.View()
	case m.focus == focusConfirm:
		resHeader = focusStyle.Render("Confirm")
		sel, ok := m.activeSelection()
		target := ""
		if ok {
			target = sel.PrimaryID
		}
		prompt := fmt.Sprintf("Confirm action %s on %s\nType %s and press enter", m.pendingAct, target, target)
		midView = prompt + "\n" + m.confirm.View()
	default:
		if m.graphMode {
			inBorder := m.styles.PaneBorder
			outBorder := m.styles.PaneBorder
			if m.lens.Focus() == graphlens.SideIn {
				inBorder = m.styles.PaneBorderFocus
			} else {
				outBorder = m.styles.PaneBorderFocus
			}
			card := m.styles.PaneBorder.Render(headerStyle.Render("Node") + "\n" + m.graphRootCardView())
			inCol := inBorder.Render(headerStyle.Render("Incoming") + "\n" + m.lens.IncomingView())
			outCol := outBorder.Render(headerStyle.Render("Outgoing") + "\n" + m.lens.OutgoingView())
			midView = lipgloss.JoinHorizontal(lipgloss.Top, inCol, card, outCol)
		} else {
			midView = m.resources.View()
		}
	}
	midPane := midBorder.Render(resHeader + "\n" + midView)
	rightPane := rightBorder.Render(detHeader + "\n" + m.detailsView())
	if m.paneMidW > 0 {
		midPane = midBorder.Width(max(0, m.paneMidW-2)).Render(resHeader + "\n" + midView)
	}
	if m.paneRightW > 0 {
		rightPane = rightBorder.Width(max(0, m.paneRightW-2)).Render(detHeader + "\n" + m.detailsView())
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, midPane, rightPane)
	metaW := lipgloss.Width(rightPane)
	if m.paneRightW > 0 {
		metaW = m.paneRightW
	}
	top := m.topBar(headerStyle, metaW)

	status := ""
	if m.loading {
		status = "loading..."
	} else if m.err != nil {
		status = "error: " + m.err.Error()
	} else if m.statusLine != "" {
		status = m.statusLine
	} else if m.auditOpen {
		status = "audit: create/delete events (last 7d) for selected regions"
	} else if m.focus == focusRegions {
		status = "regions: space toggle | a/* all | n only | s solo+apply | i invert | / find | enter apply | esc cancel"
	}

	// Statusbar (bottom): keep always-on context and key hints visible.
	fs := m.filters()
	sb1 := fmt.Sprintf("%s | %s", func() string {
		if m.graphMode {
			return "GRAPH"
		}
		return "LIST"
	}(), focusName(m.focus))
	sb2 := fmt.Sprintf("svc=%s type=%s regions=%s", m.selectedService, m.selectedType, fs.Regions)
	if m.auditOpen {
		sb2 = fmt.Sprintf("audit regions=%s", strings.Join(m.selectedAuditRegions(), ","))
		if strings.TrimSpace(m.auditFilter.Value()) != "" {
			sb2 += " filter=" + strings.TrimSpace(m.auditFilter.Value())
		}
	}
	if fs.Resource != "" {
		sb2 += " filter=" + fs.Resource
	}
	if m.focus == focusRegions && fs.RegionFind != "" {
		sb2 += " rfind=" + fs.RegionFind
	}
	sb3 := m.help.View(m.keys)
	sb4 := status
	m.statusbar.SetContent(sb1, sb2, sb3, sb4)

	lines := []string{top, filterLine}
	lines = append(lines, row)
	lines = append(lines, m.statusbar.View())
	return strings.Join(lines, "\n")
}

func (m model) auditFullScreenView() string {
	headerStyle := m.styles.Title
	metaW := m.paneRightW
	if metaW <= 0 {
		metaW = max(44, min(72, m.width/2))
	}
	top := m.topBar(headerStyle, metaW)

	filterVal := "(press /)"
	if m.focus == focusAuditFilter {
		filterVal = m.auditFilter.View()
	} else if v := strings.TrimSpace(m.auditFilter.Value()); v != "" {
		filterVal = v
	}
	filterLine := fmt.Sprintf("audit: %s", filterVal)

	body := ""
	if m.showHelp {
		body = m.helpModalView()
	} else if m.auditFacetOpen {
		body = m.auditFacetsModalView()
	} else if m.auditMode == auditModeDetail {
		body = m.auditDetailFullView()
	} else {
		body = m.auditListView()
	}

	status := ""
	if m.auditLoading {
		status = "loading audit events..."
	} else if m.auditDetailError != nil {
		status = "audit error: " + m.auditDetailError.Error()
	} else if m.statusLine != "" {
		status = m.statusLine
	} else if m.auditFacetOpen {
		status = "audit facets: tab section • space toggle • / search events • enter apply • c clear • esc close"
	} else if m.auditMode == auditModeDetail {
		status = "audit detail: J jump to resource • esc back • E close"
	} else {
		status = "audit list: enter detail • J jump • / text filter • F facets • T window • e errors • [ ] cursor page • +/- page size"
	}

	sb1 := "AUDIT | " + map[bool]string{true: "detail", false: "list"}[m.auditMode == auditModeDetail]
	sb2 := "regions=" + strings.Join(m.selectedAuditRegions(), ",")
	if strings.TrimSpace(m.auditFilter.Value()) != "" {
		sb2 += " filter=" + strings.TrimSpace(m.auditFilter.Value())
	}
	sb3 := "E close • enter detail • J jump • F facets • / filter • T window • e errors • +/- page size • [ prev ] next"
	sb4 := status
	m.statusbar.SetContent(sb1, sb2, sb3, sb4)

	return strings.Join([]string{top, filterLine, body, m.statusbar.View()}, "\n")
}

func (m model) auditListView() string {
	head := "Audit Events"
	pageLabel := fmt.Sprintf("page %d", max(1, m.auditPageNum))
	if m.auditTotal > 0 {
		pageLabel = fmt.Sprintf("page %d (~%d)", max(1, m.auditPageNum), m.auditTotal)
	}
	window := normalizeAuditWindow(m.auditFilters.Window)
	prevState := "off"
	if m.auditHasPrev {
		prevState = "on"
	}
	nextState := "off"
	if m.auditHasNext {
		nextState = "on"
	}
	head = fmt.Sprintf(
		"%s  %s  %s  %s  %s",
		head,
		m.styles.Dim.Render(pageLabel),
		m.styles.Dim.Render("window:"+window),
		m.styles.Dim.Render(fmt.Sprintf("size:%d", m.auditPageSize)),
		m.styles.Dim.Render(fmt.Sprintf("cursor:[%s ]%s", prevState, nextState)),
	)
	keysLine := "keys: j/k pgup/pgdown ctrl+u/d g/G | [ prev ] next | / text | F facets | T window | e errors | +/- size"

	body := m.auditTable.View()
	if m.auditLoading {
		body = m.styles.Dim.Render("loading audit events...") + "\n" + body
	} else if m.auditDetailError != nil {
		body = m.styles.Bad.Render("audit load error: "+m.auditDetailError.Error()) + "\n" + body
	}
	chips := strings.Join(m.auditActiveChips(), "  ")

	box := m.styles.PaneBorder
	if m.focus == focusAudit || m.focus == focusAuditFilter {
		box = m.styles.PaneBorderFocus
	}
	if m.width > 0 {
		box = box.Width(max(10, m.width-2))
	}
	lines := []string{
		m.styles.Title.Render(head),
		m.styles.Dim.Render(keysLine),
	}
	if strings.TrimSpace(chips) != "" {
		lines = append(lines, chips)
	}
	lines = append(lines, body)
	return box.Render(strings.Join(lines, "\n"))
}

func (m model) auditActiveChips() []string {
	chips := []string{}
	if acts := sortedTrueKeys(m.auditFilters.Actions); len(acts) > 0 {
		chips = append(chips, m.styles.Value.Render("action:"+strings.Join(acts, "+")))
	}
	if svcs := sortedTrueKeys(m.auditFilters.Services); len(svcs) > 0 {
		if len(svcs) > 3 {
			chips = append(chips, m.styles.Value.Render(fmt.Sprintf("svc:%s+%d", strings.Join(svcs[:2], "+"), len(svcs)-2)))
		} else {
			chips = append(chips, m.styles.Value.Render("svc:"+strings.Join(svcs, "+")))
		}
	}
	if evs := sortedTrueKeys(m.auditFilters.EventNames); len(evs) > 0 {
		if len(evs) > 2 {
			chips = append(chips, m.styles.Value.Render(fmt.Sprintf("event:%s+%d", evs[0], len(evs)-1)))
		} else {
			chips = append(chips, m.styles.Value.Render("event:"+strings.Join(evs, "+")))
		}
	}
	if m.auditFilters.OnlyErrors {
		chips = append(chips, m.styles.Bad.Render("errors:on"))
	}
	if txt := strings.TrimSpace(m.auditFilters.Text); txt != "" {
		chips = append(chips, m.styles.Value.Render("q:"+txt))
	}
	return chips
}

func (m model) auditFacetsModalView() string {
	width := max(60, m.width-6)
	if m.width <= 0 {
		width = 100
	}
	sel := func(on bool, text string) string {
		if on {
			return m.styles.Selected.Render(text)
		}
		return m.styles.Value.Render(text)
	}
	rowPrefix := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}
	actions := []string{
		fmt.Sprintf("%s create", rowPrefix(m.auditFacetDraft.Actions["create"])),
		fmt.Sprintf("%s delete", rowPrefix(m.auditFacetDraft.Actions["delete"])),
	}
	for i := range actions {
		actions[i] = sel(m.auditFacetSection == 0 && i == m.auditFacetActionsCursor, actions[i])
	}
	actionBox := m.styles.PaneBorder.Render(
		m.styles.Title.Render("Action") + "\n" + strings.Join(actions, "\n"),
	)

	services := make([]string, 0, len(m.auditFacets.Services))
	for i, fc := range m.auditFacets.Services {
		line := fmt.Sprintf("%s %-20s %5d", rowPrefix(m.auditFacetDraft.Services[fc.Value]), fc.Value, fc.Count)
		services = append(services, sel(m.auditFacetSection == 1 && i == m.auditFacetServicesCursor, line))
	}
	if len(services) == 0 {
		services = append(services, m.styles.Dim.Render("(none)"))
	}
	serviceBox := m.styles.PaneBorder.Render(
		m.styles.Title.Render("Service") + "\n" + strings.Join(services, "\n"),
	)

	eventHeader := m.styles.Title.Render("Event Type")
	if m.auditFacetEventSearchOn {
		eventHeader += " " + m.styles.Dim.Render("(search)")
	}
	searchLine := m.styles.Dim.Render("search: (press /)")
	if m.auditFacetEventSearchOn {
		searchLine = "search: " + m.auditFacetEventSearch.View()
	} else if v := strings.TrimSpace(m.auditFacetEventSearch.Value()); v != "" {
		searchLine = "search: " + v
	}
	eventBox := m.styles.PaneBorder.Render(
		eventHeader + "\n" + searchLine + "\n" + m.auditFacetEventNamePicker.View(),
	)

	topLine := m.styles.Title.Render("Audit Facets") + " " + m.styles.Dim.Render("tab section • space toggle • / search events • e errors-only • enter apply • c clear • esc close")
	onlyErr := m.styles.Dim.Render("errors-only: off")
	if m.auditFacetDraft.OnlyErrors {
		onlyErr = m.styles.Bad.Render("errors-only: on")
	}

	colW := max(18, (width-8)/3)
	actionBox = lipgloss.NewStyle().Width(colW).Render(actionBox)
	serviceBox = lipgloss.NewStyle().Width(colW).Render(serviceBox)
	eventBox = lipgloss.NewStyle().Width(colW + 10).Render(eventBox)
	body := lipgloss.JoinHorizontal(lipgloss.Top, actionBox, serviceBox, eventBox)
	container := m.styles.PaneBorderFocus
	if m.width > 0 {
		container = container.Width(max(20, m.width-2))
	}
	return container.Render(topLine + "\n" + onlyErr + "\n" + body)
}

func (m model) auditDetailFullView() string {
	outer := m.styles.PaneBorder
	if m.width > 0 {
		outer = outer.Width(max(10, m.width-2))
	}

	if m.auditDetailError != nil {
		return outer.Render(m.styles.Title.Render("Audit Detail") + "\n" + m.styles.Bad.Render(m.auditDetailError.Error()))
	}
	if m.auditDetail == nil {
		return outer.Render(m.styles.Title.Render("Audit Detail") + "\n" + m.styles.Dim.Render("(loading event detail...)"))
	}

	row := m.auditDetail
	lbl := func(k string) string { return m.styles.Label.Render(k) }
	val := func(v string) string { return m.styles.Value.Render(v) }
	kv := func(k, v string) string {
		if !strings.HasSuffix(k, ":") {
			k += ":"
		}
		return lbl(k) + " " + val(v)
	}
	actionStyle := m.styles.Dim
	switch strings.ToLower(strings.TrimSpace(row.Action)) {
	case "create":
		actionStyle = m.styles.Good
	case "delete":
		actionStyle = m.styles.Bad
	}

	summary := []string{
		kv("time", row.EventTime.UTC().Format("2006-01-02 15:04:05")),
		lbl("action:") + " " + actionStyle.Render(strings.ToUpper(row.Action)),
		kv("service", row.Service),
		kv("event", row.EventName),
		kv("region", row.Region),
	}
	resource := firstNonEmpty(strings.TrimSpace(row.ResourceName), strings.TrimSpace(row.ResourceArn), strings.TrimSpace(row.ResourceType), "-")
	summary = append(summary, kv("resource", resource))
	actor := firstNonEmpty(strings.TrimSpace(row.Username), strings.TrimSpace(row.PrincipalArn), "-")
	summary = append(summary, kv("actor", actor))
	if strings.TrimSpace(string(row.ResourceKey)) != "" {
		summary = append(summary, m.styles.Dim.Render("J: jump to resource"))
	} else {
		summary = append(summary, m.styles.Dim.Render("resource not in inventory"))
	}
	if row.ErrorCode != "" || row.ErrorMessage != "" {
		errLine := row.ErrorCode
		if errLine == "" {
			errLine = row.ErrorMessage
		} else if row.ErrorMessage != "" {
			errLine += " - " + row.ErrorMessage
		}
		summary = append(summary, m.styles.Bad.Render("error: "+errLine))
	}
	summaryBox := outer.Render(m.styles.Title.Render("Audit Event") + "\n" + strings.Join(summary, "\n"))

	payloadBox := outer.Render(
		m.styles.Title.Render("Event Payload") + " " + m.styles.Dim.Render("(j/k/pgup/pgdown/g/G scroll, esc back)") + "\n" + m.auditDetailViewport.View(),
	)
	if m.width >= 150 {
		leftW := max(36, m.width/3)
		rightW := max(60, m.width-leftW-6)
		left := outer.Width(max(10, leftW-2)).Render(m.styles.Title.Render("Audit Event") + "\n" + strings.Join(summary, "\n"))
		right := outer.Width(max(10, rightW-2)).Render(
			m.styles.Title.Render("Event Payload") + " " + m.styles.Dim.Render("(j/k/pgup/pgdown/g/G scroll, esc back)") + "\n" + m.auditDetailViewport.View(),
		)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, summaryBox, payloadBox)
}

func focusName(f focus) string {
	switch f {
	case focusServices:
		return "navigator"
	case focusResources:
		return "resources"
	case focusDetails:
		return "details"
	case focusFilter:
		return "filter"
	case focusRegions:
		return "regions"
	case focusActions:
		return "actions"
	case focusConfirm:
		return "confirm"
	case focusAudit:
		return "audit"
	case focusAuditFilter:
		return "audit-filter"
	case focusAuditFacets:
		return "audit-facets"
	case focusActionStream:
		return "action-stream"
	case focusActionStreamInput:
		return "action-stream-input"
	default:
		return "unknown"
	}
}

func (m model) topBar(headerStyle lipgloss.Style, metaOuterW int) string {
	leftLines := []string{
		headerStyle.Render("awscope"),
		fmt.Sprintf("version: %s", m.build),
		fmt.Sprintf("db: %s%s", m.dbPath, func() string {
			if m.offline {
				return " | offline"
			}
			return ""
		}()),
	}
	if m.theme != nil {
		leftLines = append(leftLines, fmt.Sprintf("theme: %s", m.theme.Theme().ID))
	}
	left := strings.Join(leftLines, "\n")

	profile := m.profileName
	if profile == "" {
		profile = "(unknown)"
	}
	account := m.accountID
	part := m.partition
	switch {
	case m.identityLoading:
		account = "loading..."
		part = "loading..."
	case m.identityErr != nil:
		account = "unavailable"
		part = "unavailable"
	default:
		if account == "" {
			account = "(unknown)"
		}
		if part == "" {
			part = "(unknown)"
		}
	}
	mode := "LIST"
	if m.actionStreamOpen {
		mode = "ACTION"
	} else if m.auditOpen {
		mode = "AUDIT"
	} else if m.graphMode {
		mode = "GRAPH"
	}
	fs := m.filters()
	filter := fs.Resource
	if filter == "" {
		filter = "-"
	}
	page := "-"
	if m.auditOpen {
		page = fmt.Sprintf("%d/%d", max(1, m.auditPageNum), max(1, m.auditPager.TotalPages))
	} else if !m.graphMode {
		page = fmt.Sprintf("%d/%d", m.pager.Page+1, max(1, m.pager.TotalPages))
	}
	selected := "-"
	if m.auditOpen {
		if ev, ok := m.selectedAuditEvent(); ok {
			res := firstNonEmpty(strings.TrimSpace(ev.ResourceName), strings.TrimSpace(ev.ResourceArn), strings.TrimSpace(ev.ResourceType), "-")
			selected = fmt.Sprintf("%s [%s %s]", ev.EventName, ev.Service, res)
		}
	} else if m.graphMode {
		if sel := m.lens.Selected(); sel.Kind == graphlens.SelectionNeighbor && sel.Neighbor.OtherKey != "" {
			name := strings.TrimSpace(sel.Neighbor.DisplayName)
			if name == "" {
				name = strings.TrimSpace(sel.Neighbor.PrimaryID)
			}
			selected = fmt.Sprintf("%s [%s %s]", name, sel.Neighbor.Type, sel.Neighbor.Region)
		} else if m.graphRootKey != "" {
			_, _, r, rt, pid, err := graph.ParseResourceKey(m.graphRootKey)
			if err == nil {
				selected = fmt.Sprintf("%s [%s %s]", pid, rt, r)
			} else {
				selected = string(m.graphRootKey)
			}
		}
	} else if s, ok := m.selectedSummary(); ok {
		name := strings.TrimSpace(s.DisplayName)
		if name == "" {
			name = strings.TrimSpace(s.PrimaryID)
		}
		selected = fmt.Sprintf("%s [%s %s]", name, s.Type, s.Region)
	}

	// Compact context panel: keep it dense and scannable.
	// Use label/value styles so it matches the theme.
	lbl := func(s string) string { return m.styles.Label.Render(s) }
	val := func(s string) string { return m.styles.Value.Render(s) }
	kvInline := func(k, v string) string {
		k = strings.TrimSpace(k)
		if k != "" && !strings.HasSuffix(k, "=") {
			k += "="
		}
		return lbl(k) + val(v)
	}

	line1 := strings.Join([]string{
		kvInline("profile", profile),
		kvInline("account", account),
		kvInline("partition", part),
	}, "  ")

	line2 := strings.Join([]string{
		kvInline("regions", fs.Regions),
		kvInline("svc", m.selectedService),
		kvInline("type", m.selectedType),
	}, "  ")
	if m.pricingMode {
		line2 += "  " + kvInline("pricing", "on")
	}

	line3Parts := []string{
		kvInline("mode", mode),
		kvInline("focus", focusName(m.focus)),
		kvInline("page", page),
		kvInline("filter", filter),
	}
	if m.focus == focusRegions {
		rfind := fs.RegionFind
		if rfind == "" {
			rfind = "-"
		}
		line3Parts = append(line3Parts, kvInline("rfind", rfind))
	}
	line3 := strings.Join(line3Parts, "  ")

	line4 := kvInline("sel", selected)

	lines := []string{line1, line2, line3, line4}

	// Truncate lines to the available inner width so the panel stays compact.
	innerW := 0
	if metaOuterW > 0 {
		innerW = max(0, metaOuterW-4) // border + padding
	}
	if innerW > 0 {
		for i := range lines {
			lines[i] = lipgloss.NewStyle().MaxWidth(innerW).Render(lines[i])
		}
	}
	metaText := strings.Join(lines, "\n")

	metaStyle := m.styles.MetaBox
	if metaOuterW > 0 {
		if m.width > 0 {
			metaOuterW = min(metaOuterW, m.width)
		}
		// lipgloss.Style.Width affects content width; border adds +2.
		metaStyle = metaStyle.Width(max(0, metaOuterW-2))
	}
	meta := metaStyle.Render(metaText)

	// Keep the meta panel aligned to the right; try to match the right pane width.
	if m.width <= 0 || metaOuterW <= 0 {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", meta)
	}
	leftW := m.width - metaOuterW
	if leftW < 0 {
		leftW = 0
	}
	leftBlock := lipgloss.NewStyle().Width(leftW).Render(left)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, meta)
}

func (m model) regionsLabel() string {
	rs := m.selectedRegionSlice()
	if len(rs) == 0 {
		return "(none)"
	}
	if len(m.knownRegions) > 0 && len(rs) == len(m.defaultRegionsForService()) {
		return fmt.Sprintf("all (%d)", len(rs))
	}
	// Show explicit region names when filtered so it's obvious a region scope is applied.
	if len(rs) <= 4 {
		return strings.Join(rs, ",")
	}
	return fmt.Sprintf("%d selected (%s,+%d)", len(rs), strings.Join(rs[:2], ","), len(rs)-2)
}

func (m model) graphRootCardView() string {
	if m.graphRootKey == "" {
		return "(no root)"
	}
	rawKey := string(m.graphRootKey)
	showKey := rawKey
	const maxKey = 96
	if len(showKey) > maxKey {
		showKey = showKey[:maxKey-3] + "..."
	}

	_, _, r, rt, pid, err := graph.ParseResourceKey(m.graphRootKey)
	if err != nil {
		return strings.Join([]string{
			fmt.Sprintf("key: %s", showKey),
			"",
			"(unparsed key)",
		}, "\n")
	}

	svc := ""
	if parts := strings.SplitN(rt, ":", 2); len(parts) > 0 {
		svc = parts[0]
	}

	lines := []string{
		fmt.Sprintf("name: %s", pid),
		fmt.Sprintf("service: %s", svc),
		fmt.Sprintf("type: %s", rt),
		fmt.Sprintf("region: %s", r),
		fmt.Sprintf("id: %s", pid),
		"",
		fmt.Sprintf("key: %s", showKey),
	}
	return strings.Join(lines, "\n")
}

func (m model) selectedSummary() (store.ResourceSummary, bool) {
	i := m.resources.Cursor()
	if i < 0 || i >= len(m.resourceSummaries) {
		return store.ResourceSummary{}, false
	}
	return m.resourceSummaries[i], true
}

func (m model) selectedAuditEvent() (store.CloudTrailEventSummary, bool) {
	i := m.auditTable.Cursor()
	if i < 0 || i >= len(m.auditEvents) {
		return store.CloudTrailEventSummary{}, false
	}
	return m.auditEvents[i], true
}

type selection struct {
	Key         graph.ResourceKey
	DisplayName string
	Service     string
	Type        string
	Region      string
	PrimaryID   string
	Arn         string
}

func (m model) activeSelection() (selection, bool) {
	if m.graphMode {
		if sel := m.lens.Selected(); sel.Kind == graphlens.SelectionNeighbor && sel.Neighbor.OtherKey != "" {
			n := sel.Neighbor
			return selection{
				Key:         n.OtherKey,
				DisplayName: n.DisplayName,
				Service:     n.Service,
				Type:        n.Type,
				Region:      n.Region,
				PrimaryID:   n.PrimaryID,
				Arn:         n.Arn,
			}, true
		}
		if m.graphRootKey != "" {
			_, _, region, resourceType, primaryID, err := graph.ParseResourceKey(m.graphRootKey)
			svc := ""
			if err == nil {
				if parts := strings.SplitN(resourceType, ":", 2); len(parts) > 0 {
					svc = parts[0]
				}
				return selection{
					Key:         m.graphRootKey,
					DisplayName: primaryID,
					Service:     svc,
					Type:        resourceType,
					Region:      region,
					PrimaryID:   primaryID,
				}, true
			}
			return selection{
				Key:         m.graphRootKey,
				DisplayName: string(m.graphRootKey),
				PrimaryID:   string(m.graphRootKey),
			}, true
		}
		return selection{}, false
	}

	s, ok := m.selectedSummary()
	if !ok {
		return selection{}, false
	}
	return selection{
		Key:         s.Key,
		DisplayName: s.DisplayName,
		Service:     s.Service,
		Type:        s.Type,
		Region:      s.Region,
		PrimaryID:   s.PrimaryID,
		Arn:         s.Arn,
	}, true
}

func (m model) detailsView() string {
	lbl := func(k string) string { return m.styles.Label.Render(k) }
	val := func(v string) string { return m.styles.Value.Render(v) }
	sec := func(s string) string { return m.styles.Title.Render(s) }
	kv := func(k, v string) string {
		k = strings.TrimSpace(k)
		if !strings.HasSuffix(k, ":") {
			k += ":"
		}
		return lbl(k) + " " + val(v)
	}
	statusVal := func(s string) string {
		s2 := strings.ToLower(strings.TrimSpace(s))
		icon := ""
		if m.icons != nil {
			icon = icons.Pad(m.icons.Status(s), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		switch s2 {
		case "running", "available", "active", "ok", "enabled", "in-service", "inservice":
			return m.styles.Good.Render(icon + s)
		case "pending", "creating", "modifying", "updating", "provisioning":
			return m.styles.Warn.Render(icon + s)
		case "failed", "error", "deleting", "terminated":
			return m.styles.Bad.Render(icon + s)
		case "stopped", "inactive", "disabled":
			return m.styles.Dim.Render(icon + s)
		default:
			return val(icon + s)
		}
	}

	sel, ok := m.activeSelection()
	if !ok {
		return strings.Join([]string{
			sec("no selection"),
			"",
			sec("context:"),
			kv("profile", m.profileName),
			kv("account", m.accountID),
			kv("regions", m.regionsLabel()),
			kv("service/type", fmt.Sprintf("%s / %s", m.selectedService, m.selectedType)),
			"",
			sec("tabs:"),
			val("s: summary"),
			val("r: related"),
			val("x: raw"),
			"",
			sec("panes:"),
			val("1: navigator"),
			val("2: resources"),
			val("3: details"),
			"",
			sec("keys:"),
			val("tab: switch focus"),
			val("/: filter"),
			val("?: help"),
			val("R: regions"),
			val("A: actions"),
			val("E: audit events"),
			val("[ ]: prev/next page"),
			val("backspace: back"),
			val("ctrl+r: refresh"),
			val("p: pricing mode"),
			val("T: cycle theme"),
			val("q: quit"),
		}, "\n")
	}

	if m.detailsTab == detailsRelationships {
		header := sec("related") + " " + m.styles.Dim.Render("(enter jump, backspace back)")
		if len(m.related.Items()) == 0 {
			return header + "\n" + m.styles.Dim.Render("(none)")
		}
		return header + "\n" + m.related.View()
	}
	if m.detailsTab == detailsRaw {
		return sec("raw") + " " + m.styles.Dim.Render("(j/k/pgup/pgdown scroll)") + "\n" + m.raw.View()
	}

	if m.graphMode {
		lensSel := m.lens.Selected()
		if lensSel.Kind == graphlens.SelectionGroup {
			dir := "outgoing"
			if lensSel.Dir == "in" {
				dir = "incoming"
			}
			lines := []string{sec("group:")}
			lines = append(lines, kv("dir", dir))
			lines = append(lines, kv("kind", lensSel.Group.Kind))
			lines = append(lines, kv("count", fmt.Sprintf("%d", lensSel.Count)))
			if len(lensSel.Items) > 0 {
				lines = append(lines, "")
				lines = append(lines, sec("sample:"))
				lines = append(lines, val(strings.Join(lensSel.Items, ", ")))
				if lensSel.More > 0 {
					lines = append(lines, m.styles.Dim.Render(fmt.Sprintf("(+%d more)", lensSel.More)))
				}
			}
			lines = append(lines, "")
			lines = append(lines, sec("keys:"))
			lines = append(lines, val("h/l or left/right: switch incoming/outgoing"))
			lines = append(lines, val("space/enter: expand/collapse group"))
			lines = append(lines, val("enter: traverse neighbor (when selected)"))
			lines = append(lines, val("backspace: back"))
			if len(m.navStack) > 0 {
				lines = append(lines, "")
				lines = append(lines, kv("nav depth", fmt.Sprintf("%d (backspace to go back)", len(m.navStack))))
			}
			return strings.Join(lines, "\n")
		}

		root := string(m.graphRootKey)
		if m.graphRootKey != "" {
			_, _, r, rt, pid, err := graph.ParseResourceKey(m.graphRootKey)
			if err == nil {
				root = fmt.Sprintf("%s [%s, %s]", pid, rt, r)
			}
		}
		lines := []string{sec("graph:")}
		lines = append(lines, kv("root", root))
		lines = append(lines, kv("selected", sel.DisplayName))
		lines = append(lines, kv("type", sel.Type))
		lines = append(lines, kv("service", sel.Service))
		lines = append(lines, kv("region", sel.Region))
		lines = append(lines, kv("id", sel.PrimaryID))
		if sel.Arn != "" {
			lines = append(lines, kv("arn", sel.Arn))
		}

		lines = append(lines, "")
		lines = append(lines, sec("neighbors:"))
		lines = append(lines, val(fmt.Sprintf("%d (press 2 to browse)", len(m.related.Items()))))

		if !m.offline {
			if items := m.actionItemsForSelection(); len(items) > 0 {
				lines = append(lines, "")
				lines = append(lines, sec("actions:"))
				for _, it := range items {
					ai, _ := it.(actionItem)
					if ai.id == "" {
						continue
					}
					lines = append(lines, val(fmt.Sprintf("- %s (%s)", ai.title, ai.id)))
				}
			}
		}
		if len(m.navStack) > 0 {
			lines = append(lines, "")
			lines = append(lines, kv("nav depth", fmt.Sprintf("%d (backspace to go back)", len(m.navStack))))
		}
		return strings.Join(lines, "\n")
	}

	s, ok := m.selectedSummary()
	if !ok {
		return "no selection"
	}

	status := statusFromAttrs(s.Attributes)
	created := ""
	if v, ok := s.Attributes["created_at"].(string); ok {
		created = v
	}

	lines := []string{sec("summary:")}
	lines = append(lines, kv("name", s.DisplayName))
	lines = append(lines, kv("type", s.Type))
	lines = append(lines, kv("service", s.Service))
	lines = append(lines, kv("region", s.Region))
	if status != "" {
		lines = append(lines, lbl("status:")+" "+statusVal(status))
	}
	if created != "" {
		lines = append(lines, kv("created", created))
	}
	// Estimated monthly cost (best-effort).
	if s.EstMonthlyUSD != nil {
		lines = append(lines, lbl("est/mo:")+" "+val(cost.FormatUSDPerMonthFull(*s.EstMonthlyUSD)))
	} else {
		lines = append(lines, lbl("est/mo:")+" "+m.styles.Dim.Render("-"))
	}
	if !s.CollectedAt.IsZero() {
		lines = append(lines, kv("collected", s.CollectedAt.Format("2006-01-02 15:04")))
	}
	if !s.UpdatedAt.IsZero() {
		lines = append(lines, kv("updated", s.UpdatedAt.Format("2006-01-02 15:04")))
	}

	lines = append(lines, "")
	lines = append(lines, sec("identifiers:"))
	lines = append(lines, kv("id", s.PrimaryID))
	if s.Arn != "" {
		lines = append(lines, kv("arn", s.Arn))
	}

	if len(s.Attributes) > 0 {
		lines = append(lines, "")
		lines = append(lines, sec("attributes:"))
		ks := make([]string, 0, len(s.Attributes))
		for k := range s.Attributes {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			lines = append(lines, lbl(k+":")+" "+val(fmt.Sprintf("%v", s.Attributes[k])))
		}
	}

	lines = append(lines, "")
	lines = append(lines, sec("related:"))
	if len(m.related.Items()) == 0 {
		lines = append(lines, m.styles.Dim.Render("(none)"))
	} else {
		lines = append(lines, val(fmt.Sprintf("%d (press 2 to browse)", len(m.related.Items()))))
	}

	if !m.offline {
		if items := m.actionItemsForSelection(); len(items) > 0 {
			lines = append(lines, "")
			lines = append(lines, sec("actions:"))
			for _, it := range items {
				ai, _ := it.(actionItem)
				if ai.id == "" {
					continue
				}
				lines = append(lines, val(fmt.Sprintf("- %s (%s)", ai.title, ai.id)))
			}
		}
	}
	if len(m.navStack) > 0 {
		lines = append(lines, "")
		lines = append(lines, kv("nav depth", fmt.Sprintf("%d (backspace to go back)", len(m.navStack))))
	}
	return strings.Join(lines, "\n")
}

func (m model) auditDetailsView() string {
	lbl := func(k string) string { return m.styles.Label.Render(k) }
	val := func(v string) string { return m.styles.Value.Render(v) }
	sec := func(s string) string { return m.styles.Title.Render(s) }
	kv := func(k, v string) string {
		if !strings.HasSuffix(k, ":") {
			k += ":"
		}
		return lbl(k) + " " + val(v)
	}
	if m.auditDetailError != nil {
		return sec("audit error") + "\n" + m.styles.Bad.Render(m.auditDetailError.Error())
	}
	if m.auditDetail == nil {
		return sec("audit event") + "\n" + m.styles.Dim.Render("(select an event)")
	}

	row := m.auditDetail
	actionStyle := m.styles.Dim
	switch strings.ToLower(strings.TrimSpace(row.Action)) {
	case "create":
		actionStyle = m.styles.Good
	case "delete":
		actionStyle = m.styles.Bad
	}

	lines := []string{
		sec("summary:"),
		kv("time", row.EventTime.UTC().Format("2006-01-02 15:04:05")),
		lbl("action:") + " " + actionStyle.Render(strings.ToUpper(row.Action)),
		kv("service", row.Service),
		kv("event", row.EventName),
		kv("region", row.Region),
	}
	if row.ResourceType != "" || row.ResourceName != "" || row.ResourceArn != "" {
		lines = append(lines, "")
		lines = append(lines, sec("resource:"))
		if row.ResourceType != "" {
			lines = append(lines, kv("type", row.ResourceType))
		}
		if row.ResourceName != "" {
			lines = append(lines, kv("name", row.ResourceName))
		}
		if row.ResourceArn != "" {
			lines = append(lines, kv("arn", row.ResourceArn))
		}
		if strings.TrimSpace(string(row.ResourceKey)) != "" {
			lines = append(lines, kv("resource_key", string(row.ResourceKey)))
			lines = append(lines, m.styles.Dim.Render("J: jump to resource"))
		} else {
			lines = append(lines, m.styles.Dim.Render("resource not in inventory"))
		}
	}

	lines = append(lines, "")
	lines = append(lines, sec("actor:"))
	if row.Username != "" {
		lines = append(lines, kv("username", row.Username))
	}
	if row.PrincipalArn != "" {
		lines = append(lines, kv("principal", row.PrincipalArn))
	}
	if row.SourceIP != "" {
		lines = append(lines, kv("source_ip", row.SourceIP))
	}
	if row.UserAgent != "" {
		lines = append(lines, kv("user_agent", row.UserAgent))
	}
	if row.ReadOnly != "" {
		lines = append(lines, kv("read_only", row.ReadOnly))
	}
	if row.ErrorCode != "" || row.ErrorMessage != "" {
		lines = append(lines, "")
		lines = append(lines, sec("error:"))
		if row.ErrorCode != "" {
			lines = append(lines, kv("code", row.ErrorCode))
		}
		if row.ErrorMessage != "" {
			lines = append(lines, kv("message", row.ErrorMessage))
		}
	}
	lines = append(lines, "")
	lines = append(lines, sec("event json:"))
	lines = append(lines, m.styles.Dim.Render(strings.TrimSpace(string(row.EventJSON))))
	return strings.Join(lines, "\n")
}
