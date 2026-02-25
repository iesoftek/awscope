package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"awscope/internal/actions"
	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/aws"
	"awscope/internal/core"
	"awscope/internal/cost"
	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/components/navigator"
	"awscope/internal/tui/icons"
	"awscope/internal/tui/theme"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mistakenelf/teacup/statusbar"
	"github.com/muesli/reflow/wordwrap"

	"awscope/internal/tui/widgets/table"
)

type focus int

const (
	focusServices focus = iota
	focusResources
	focusDetails
	focusFilter
	focusRegions
	focusActions
	focusConfirm
	focusAudit
	focusAuditFilter
	focusAuditFacets
)

type detailsTab int

const (
	detailsSummary detailsTab = iota
	detailsRelationships
	detailsRaw
)

type auditMode int

const (
	auditModeList auditMode = iota
	auditModeDetail
)

type auditFilters struct {
	Text       string
	Actions    map[string]bool
	Services   map[string]bool
	EventNames map[string]bool
	Window     string // "24h"|"7d"|"30d"|"all"
	OnlyErrors bool
}

type navFrame struct {
	service     string
	typ         string
	regions     map[string]bool
	filter      string
	page        int
	selectedKey graph.ResourceKey
	tab         detailsTab

	graphMode    bool
	graphRootKey graph.ResourceKey

	expandedService string
	lensFocus       graphlens.Side
	lensInCursor    int
	lensOutCursor   int
	lensExpanded    []string
}

type model struct {
	ctx context.Context
	st  *store.Store

	dbPath  string
	offline bool
	width   int
	height  int
	build   string

	loader *aws.Loader

	paneLeftW  int
	paneMidW   int
	paneRightW int

	focus focus

	detailsTab detailsTab

	nav       navigator.Model
	resources table.Model
	lens      graphlens.Model
	filter    textinput.Model

	// Region picker
	regions           list.Model // legacy (kept for now; no longer rendered)
	regionTable       table.Model
	regionPickerOrder []string
	regionSvcCounts   map[string]int
	regionTypeCounts  map[string]int
	regionFilter      textinput.Model
	regionFilterOn    bool

	related list.Model
	raw     viewport.Model

	selectedService string
	selectedType    string
	selectedRegions map[string]bool
	knownRegions    []string

	resourceSummaries []store.ResourceSummary
	totalResources    int
	pager             paginator.Model

	auditOpen           bool
	auditMode           auditMode
	auditLoading        bool
	auditTable          table.Model
	auditFilter         textinput.Model
	auditPager          paginator.Model
	auditEvents         []store.CloudTrailEventSummary
	auditTotal          int
	auditPageNum        int
	auditPageSize       int
	auditHasNext        bool
	auditHasPrev        bool
	auditNextCursor     *store.CloudTrailCursor
	auditPrevCursor     *store.CloudTrailCursor
	auditDetail         *store.CloudTrailEventRow
	auditDetailViewport viewport.Model
	auditDetailPretty   string
	auditDetailError    error
	auditFilterSeq      int
	auditQuerySeq       int

	auditFilters auditFilters

	auditFacetOpen            bool
	auditFacetSection         int
	auditFacetActionsCursor   int
	auditFacetServicesCursor  int
	auditFacetEventSearchOn   bool
	auditFacetEventSearch     textinput.Model
	auditFacetEventNamePicker list.Model
	auditFacets               store.CloudTrailFacets
	auditFacetDraft           auditFilters

	auditPageCache      map[string]store.CloudTrailCursorPage
	auditPageCacheOrder []string

	graphMode    bool
	graphRootKey graph.ResourceKey

	loading bool
	err     error

	neighborsKey graph.ResourceKey
	neighbors    []store.Neighbor

	serviceCounts []store.ServiceCount
	typeCounts    []store.TypeCount

	profileName     string
	accountID       string
	partition       string
	identityErr     error
	identityLoading bool

	actions    list.Model
	confirm    textinput.Model
	pendingAct string
	statusLine string

	navStack         []navFrame
	pendingSelectKey graph.ResourceKey

	help     help.Model
	showHelp bool
	keys     keyMap

	filterSeq int

	theme  *theme.Manager
	styles theme.Styles

	statusbar statusbar.Model

	pricingMode bool

	iconMode string
	icons    icons.Set
}

type filterSnapshot struct {
	Resource   string
	Regions    string
	RegionFind string
}

func (m model) filters() filterSnapshot {
	return filterSnapshot{
		Resource:   strings.TrimSpace(m.filter.Value()),
		Regions:    m.regionsLabel(),
		RegionFind: strings.TrimSpace(m.regionFilter.Value()),
	}
}

type resourcesLoadedMsg struct {
	summaries []store.ResourceSummary
	total     int
	err       error
}

type auditEventsLoadedMsg struct {
	events []store.CloudTrailEventSummary
	total  int
	err    error
}

type auditDetailLoadedMsg struct {
	event store.CloudTrailEventRow
	found bool
	err   error
}

type auditCursorPageLoadedMsg struct {
	seq       int
	cacheKey  string
	page      store.CloudTrailCursorPage
	pageNum   int
	err       error
	fromCache bool
}

type auditCountLoadedMsg struct {
	seq   int
	total int
	err   error
}

type auditFacetsLoadedMsg struct {
	seq    int
	facets store.CloudTrailFacets
	err    error
}

type serviceCountsLoadedMsg struct {
	rows []store.ServiceCount
	err  error
}

type typeCountsLoadedMsg struct {
	service string
	rows    []store.TypeCount
	err     error
}

type serviceCostAggLoadedMsg struct {
	rows []store.CostAgg
	err  error
}

type typeCostAggLoadedMsg struct {
	service string
	rows    []store.CostAgg
	err     error
}

type neighborsLoadedMsg struct {
	key       graph.ResourceKey
	neighbors []store.Neighbor
	err       error
}

type regionsLoadedMsg struct {
	regions []string
	err     error
}

type regionCountsLoadedMsg struct {
	service string
	typ     string
	svcRows []store.RegionCount
	typRows []store.RegionCount
	err     error
}

type identityLoadedMsg struct {
	profileName string
	accountID   string
	partition   string
	arn         string
	err         error
}

type actionItem struct {
	id    string
	title string
	desc  string
	risk  actions.RiskLevel
}

func (i actionItem) Title() string       { return i.title }
func (i actionItem) Description() string { return i.desc }
func (i actionItem) FilterValue() string { return i.id + " " + i.title }

type actionDoneMsg struct {
	line string
	err  error
}

type filterDebouncedMsg struct {
	seq   int
	value string
}

type auditFilterDebouncedMsg struct {
	seq   int
	value string
}

type rawLoadedMsg struct {
	key     graph.ResourceKey
	content string
	err     error
}

type relItem struct {
	kind     string
	dir      string // "out" or "in"
	otherKey graph.ResourceKey
	service  string
	region   string
	typ      string
	title    string
}

func (i relItem) Title() string { return i.title }
func (i relItem) Description() string {
	arrow := "->"
	if i.dir == "in" {
		arrow = "<-"
	}
	parts := []string{arrow, i.kind}
	if i.typ != "" {
		parts = append(parts, i.typ)
	}
	if i.region != "" {
		parts = append(parts, i.region)
	}
	return strings.Join(parts, " | ")
}
func (i relItem) FilterValue() string { return i.title + " " + string(i.otherKey) + " " + i.kind }

type auditFacetEventItem struct {
	name     string
	count    int
	selected bool
}

func (i auditFacetEventItem) Title() string {
	prefix := "[ ]"
	if i.selected {
		prefix = "[x]"
	}
	return fmt.Sprintf("%s %s", prefix, i.name)
}
func (i auditFacetEventItem) Description() string { return fmt.Sprintf("%d", i.count) }
func (i auditFacetEventItem) FilterValue() string { return i.name }

func cloneBoolMap(src map[string]bool) map[string]bool {
	if len(src) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneAuditFilters(src auditFilters) auditFilters {
	return auditFilters{
		Text:       src.Text,
		Actions:    cloneBoolMap(src.Actions),
		Services:   cloneBoolMap(src.Services),
		EventNames: cloneBoolMap(src.EventNames),
		Window:     src.Window,
		OnlyErrors: src.OnlyErrors,
	}
}

type keyMap struct {
	Quit        key.Binding
	Focus       key.Binding
	Filter      key.Binding
	Regions     key.Binding
	Actions     key.Binding
	Refresh     key.Binding
	Theme       key.Binding
	Graph       key.Binding
	Pricing     key.Binding
	Audit       key.Binding
	AuditDetail key.Binding
	AuditJump   key.Binding
	AuditFacets key.Binding
	AuditWindow key.Binding
	AuditErrors key.Binding
	AuditPage   key.Binding
	PrevPage    key.Binding
	NextPage    key.Binding
	Back        key.Binding
	Help        key.Binding

	PaneNav       key.Binding
	PaneResources key.Binding
	PaneDetails   key.Binding

	Summary key.Binding
	Related key.Binding
	Raw     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Focus, k.Filter, k.Regions, k.Actions, k.Graph, k.Audit, k.Pricing, k.PrevPage, k.NextPage, k.Back, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Focus, k.Filter, k.Regions, k.Actions, k.Graph, k.Audit, k.Refresh},
		{k.PaneNav, k.PaneResources, k.PaneDetails, k.Summary, k.Related, k.Raw, k.Pricing, k.Theme},
		{k.AuditDetail, k.AuditJump, k.AuditFacets, k.AuditWindow, k.AuditErrors, k.AuditPage},
		{k.PrevPage, k.NextPage, k.Back},
		{k.Help, k.Quit},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.loadIdentityCmd(),
		m.loadRegionsCmd(),
		m.loadRegionCountsCmd(),
		m.loadServiceCountsCmd(),
		m.loadTypeCountsCmd(m.nav.ExpandedService()),
		m.loadResourcesCmd(),
		m.loadNeighborsCmd(),
	)
}

func statusbarAdaptiveColor(c lipgloss.Color) lipgloss.AdaptiveColor {
	s := string(c)
	if strings.TrimSpace(s) == "" {
		return lipgloss.AdaptiveColor{}
	}
	return lipgloss.AdaptiveColor{Light: s, Dark: s}
}

func statusbarColorConfigs(p theme.Palette) (statusbar.ColorConfig, statusbar.ColorConfig, statusbar.ColorConfig, statusbar.ColorConfig) {
	// Use conservative colors (no hard background) to remain readable across terminals and themes.
	// The primary value of this statusbar is layout/truncation + always-on context.
	fg := statusbarAdaptiveColor(p.Text)
	dim := statusbarAdaptiveColor(p.TextDim)
	accent := statusbarAdaptiveColor(p.Accent)
	return statusbar.ColorConfig{Foreground: fg}, statusbar.ColorConfig{Foreground: dim}, statusbar.ColorConfig{Foreground: dim}, statusbar.ColorConfig{Foreground: accent}
}

func (m *model) applyTheme() {
	// Theme is the single source of truth for UI styling. Any UI component with its own
	// internal styles should be updated here so theme switching is consistent.
	if m.theme != nil {
		m.styles = m.theme.Styles()
	} else {
		// Fallback: keep zero-value styles (no explicit colors).
		m.styles = theme.Styles{}
	}

	// Statusbar colors use the palette directly.
	if m.theme != nil {
		c1, c2, c3, c4 := statusbarColorConfigs(m.theme.Theme().Palette)
		m.statusbar.SetColors(c1, c2, c3, c4)
	}

	// Tables.
	tbl := table.Styles{
		Header:   m.styles.TableHeader,
		Cell:     m.styles.TableCell,
		Selected: m.styles.TableSelectedRow,
	}
	m.resources.SetStyles(tbl)
	m.regionTable.SetStyles(tbl)
	m.auditTable.SetStyles(tbl)
	// Re-render row content that embeds style (e.g. dim "-").
	includeStored := strings.EqualFold(m.selectedService, "logs") && strings.EqualFold(m.selectedType, "logs:log-group")
	includeIAMKeyFields := strings.EqualFold(m.selectedService, "iam") && strings.EqualFold(m.selectedType, "iam:access-key")
	w := 80
	if m.paneMidW > 0 {
		w = m.paneMidW - 4
	}
	m.resources.SetColumns(buildResourceColumns(w, m.pricingMode, includeStored, includeIAMKeyFields))
	m.resources.SetRows(makeResourceRows(m.resourceSummaries, m.pricingMode, m.styles, m.icons, includeStored, includeIAMKeyFields))
	auditW := 80
	if m.width > 0 {
		auditW = max(20, m.width-4)
	} else if m.paneMidW > 0 {
		auditW = m.paneMidW - 4
	}
	m.auditTable.SetColumns(buildAuditColumns(auditW))
	m.auditTable.SetRows(makeAuditRows(m.auditEvents, m.styles, m.icons))

	// Navigator.
	m.nav.SetStyles(navigator.Styles{
		Arrow:    m.styles.Dim,
		Icon:     m.styles.Icon,
		IconDim:  m.styles.IconDim,
		Service:  m.styles.Value,
		Type:     m.styles.Value,
		Count:    m.styles.Count,
		Selected: m.styles.Selected,
	})
	if m.icons != nil {
		m.nav.SetIcons(m.icons)
	}

	// Graph Lens.
	m.lens.SetStyles(graphlens.Styles{
		Group:    m.styles.Value,
		Neighbor: m.styles.Value,
		Meta:     m.styles.Dim,
		Arrow:    m.styles.Dim,
		Selected: m.styles.Selected,
		Icon:     m.styles.IconDim,
	})
	if m.icons != nil {
		m.lens.SetIcons(m.icons)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.showHelp {
			switch msg.String() {
			case "?", "esc":
				m.showHelp = false
				m.help.ShowAll = false
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.auditOpen && m.focus != focusRegions {
			if cmd, handled := m.handleAuditKey(msg); handled {
				return m, cmd
			}
			// While audit viewer is open, ignore non-audit keys.
			switch msg.String() {
			case "q", "ctrl+c", "?":
			default:
				return m, nil
			}
		}

		k := msg.String()
		switch k {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "?":
			// Avoid hijacking common input while typing.
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.showHelp = true
				m.help.ShowAll = true
			}

		case "T":
			// Avoid hijacking common input while typing.
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) && m.theme != nil {
				m.theme.CycleNext()
				m.applyTheme()
			}

		case "p":
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				newMode := !m.pricingMode
				m.pricingMode = newMode
				m.nav.SetPricingMode(newMode)

				// Rebuild the resource table columns/rows immediately.
				w := 80
				if m.paneMidW > 0 {
					w = m.paneMidW - 4
				}
				includeStored := strings.EqualFold(m.selectedService, "logs") && strings.EqualFold(m.selectedType, "logs:log-group")
				includeIAMKeyFields := strings.EqualFold(m.selectedService, "iam") && strings.EqualFold(m.selectedType, "iam:access-key")
				// Avoid transient row/column mismatches that can panic in table rendering.
				if newMode {
					// Adding a column: columns first, then rows.
					m.resources.SetColumns(buildResourceColumns(w, true, includeStored, includeIAMKeyFields))
					m.resources.SetRows(makeResourceRows(m.resourceSummaries, true, m.styles, m.icons, includeStored, includeIAMKeyFields))
				} else {
					// Removing a column: rows first, then columns.
					m.resources.SetRows(makeResourceRows(m.resourceSummaries, false, m.styles, m.icons, includeStored, includeIAMKeyFields))
					m.resources.SetColumns(buildResourceColumns(w, false, includeStored, includeIAMKeyFields))
				}

				// Load cost aggregates when enabled.
				if newMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}
			}

		case "E":
			if m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) {
				break
			}
			if m.auditOpen {
				m.auditOpen = false
				m.auditMode = auditModeList
				m.auditFacetOpen = false
				m.auditFilter.Blur()
				if m.focus == focusAudit || m.focus == focusAuditFilter {
					m.focus = focusResources
				}
				break
			}
			m.auditOpen = true
			m.auditMode = auditModeList
			m.focus = focusAudit
			m.auditFacetOpen = false
			m.auditLoading = true
			m.auditDetail = nil
			m.auditDetailPretty = ""
			m.auditDetailViewport.SetContent("")
			m.auditDetailViewport.GotoTop()
			m.auditDetailError = nil
			m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
			if m.auditFilters.Window == "" {
				m.auditFilters.Window = "7d"
			}
			if m.auditPageSize <= 0 {
				m.auditPageSize = 100
			}
			m.resetAuditPaging()
			m.clearAuditPageCache()
			m.resizeAuditWidgets()
			seq := m.nextAuditSeq()
			cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))

		case "g":
			if m.auditOpen || m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) {
				break
			}
			if m.graphMode {
				m.graphMode = false
				m.graphRootKey = ""
				m.loading = true
				m.err = nil
				m.pager.Page = 0
				cmds = append(cmds, m.loadResourcesCmd())
				break
			}
			s, ok := m.selectedSummary()
			if !ok {
				m.statusLine = "no selection"
				break
			}
			m.graphMode = true
			m.graphRootKey = s.Key
			m.lens.SetFocus(graphlens.SideOut)
			m.lens.SetCursors(0, 0)
			cmds = append(cmds, m.loadNeighborsCmd())

		case "1":
			if !m.auditOpen && m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.focus = focusServices
			}
		case "2":
			if !m.auditOpen && m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.focus = focusResources
			}
		case "3":
			if !m.auditOpen && m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.focus = focusDetails
			}

		case "r":
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.detailsTab = detailsRelationships
			}
		case "x":
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) {
				m.detailsTab = detailsRaw
				cmds = append(cmds, m.loadRawCmd())
			}

		case "tab":
			switch m.focus {
			case focusServices:
				m.focus = focusResources
			case focusResources:
				m.focus = focusDetails
			case focusDetails:
				m.focus = focusServices
			case focusFilter:
				m.focus = focusResources
				m.filter.Blur()
			case focusRegions:
				if m.auditOpen {
					m.auditMode = auditModeList
					m.focus = focusAudit
				} else {
					m.focus = focusResources
				}
				m.regionFilterOn = false
				m.regionFilter.Blur()
				m.loading = true
				m.err = nil
				m.pager.Page = 0
				cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd())
				if m.pricingMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}
			case focusActions:
				m.focus = focusResources
			case focusConfirm:
				m.focus = focusActions
				m.confirm.Blur()
			case focusAudit:
				// Keep focus within audit tool while open.
				m.focus = focusAudit
			case focusAuditFilter:
				m.focus = focusAudit
				m.auditFilter.Blur()
			}

		case "/":
			if m.auditOpen && m.focus == focusAudit {
				m.focus = focusAuditFilter
				m.auditFilter.Focus()
				break
			}
			if m.focus == focusRegions {
				m.regionFilterOn = true
				m.regionFilter.Focus()
				break
			}
			if m.focus != focusConfirm {
				m.focus = focusFilter
				m.filter.Focus()
			}

		case "R":
			if m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) {
				break
			}
			m.focus = focusRegions
			m.filter.Blur()
			m.regionFilterOn = false
			m.regionFilter.Blur()
			m.regionFilter.SetValue("")
			m.rebuildRegionTable("")
			cmds = append(cmds, m.loadRegionCountsCmd())

		case "A":
			if m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) {
				break
			}
			if m.offline {
				m.statusLine = "actions disabled in offline mode"
				break
			}
			m.focus = focusActions
			m.actions.SetItems(m.actionItemsForSelection())

		case "a":
			if m.focus == focusRegions && !m.regionFilterOn {
				m.selectAllRegions()
				m.rebuildRegionTable("")
			}
		case "*":
			if m.focus == focusRegions && !m.regionFilterOn {
				m.selectAllRegions()
				m.rebuildRegionTable("")
			}
		case "i":
			if m.focus == focusRegions && !m.regionFilterOn {
				m.invertRegions()
				m.rebuildRegionTable("")
			}
		case "n":
			if m.focus == focusRegions && !m.regionFilterOn {
				m.selectOnlyCurrentRegion()
				m.rebuildRegionTable("")
			}
		case "s":
			// Solo the current region and apply immediately (one keystroke).
			if m.focus == focusRegions && !m.regionFilterOn {
				m.selectOnlyCurrentRegion()
				m.rebuildRegionTable("")
				if m.auditOpen {
					m.auditMode = auditModeList
					m.focus = focusAudit
				} else {
					m.focus = focusResources
				}
				m.regionFilterOn = false
				m.regionFilter.Blur()
				m.loading = true
				m.err = nil
				m.pager.Page = 0
				cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd())
				if m.pricingMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}
				if m.auditOpen {
					m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
					m.auditLoading = true
					m.resetAuditPaging()
					m.clearAuditPageCache()
					seq := m.nextAuditSeq()
					cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
				}
				break
			}
			// Details tab shortcut: Summary
			if m.focus != focusFilter && m.focus != focusConfirm && !(m.focus == focusRegions && m.regionFilterOn) && m.focus != focusRegions {
				m.detailsTab = detailsSummary
			}

		case "backspace":
			if m.auditOpen || m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) || m.focus == focusAuditFilter {
				break
			}
			if len(m.navStack) > 0 {
				m.restoreNav()
				m.err = nil
				if m.graphMode {
					cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadNeighborsCmd())
				} else {
					m.loading = true
					cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd())
				}
				if m.pricingMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}
			}

		case "esc":
			switch m.focus {
			case focusFilter:
				m.focus = focusResources
				m.filter.Blur()
			case focusRegions:
				if m.regionFilterOn {
					m.regionFilterOn = false
					m.regionFilter.Blur()
					m.rebuildRegionTable("")
				} else {
					if m.auditOpen {
						m.auditMode = auditModeList
						m.focus = focusAudit
					} else {
						m.focus = focusResources
					}
				}
			case focusActions:
				m.focus = focusResources
			case focusConfirm:
				m.focus = focusActions
				m.confirm.SetValue("")
				m.confirm.Blur()
			case focusAuditFilter:
				m.focus = focusAudit
				m.auditFilter.Blur()
			case focusAudit:
				m.auditOpen = false
				m.focus = focusResources
			}

		case "enter":
			switch {
			case m.focus == focusAuditFilter:
				m.focus = focusAudit
				m.auditFilter.Blur()
				m.auditLoading = true
				m.auditPager.Page = 0
				cmds = append(cmds, m.loadAuditEventsCmd())

			case m.focus == focusAudit:
				ev, ok := m.selectedAuditEvent()
				if !ok {
					m.statusLine = "no audit event selected"
					break
				}
				if strings.TrimSpace(string(ev.ResourceKey)) == "" {
					m.statusLine = "resource not in inventory for this event"
					break
				}
				_, _, region, typ, _, err := graph.ParseResourceKey(ev.ResourceKey)
				if err != nil {
					m.statusLine = "cannot resolve target resource key"
					break
				}
				service := ""
				if parts := strings.SplitN(typ, ":", 2); len(parts) > 0 {
					service = parts[0]
				}
				m.pushNav()
				m.auditOpen = false
				m.auditFilter.Blur()
				m.jumpTo(service, region, ev.ResourceKey)
				m.syncNavigatorSelection()
				m.loading = true
				m.err = nil
				cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd(), m.loadNeighborsCmd())
				if m.pricingMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}

			case m.focus == focusFilter:
				m.focus = focusResources
				m.filter.Blur()
				m.loading = true
				m.err = nil
				m.pager.Page = 0
				cmds = append(cmds, m.loadResourcesCmd())

			case m.focus == focusRegions:
				if m.regionFilterOn {
					m.regionFilterOn = false
					m.regionFilter.Blur()
					m.rebuildRegionTable("")
					break
				}
				if m.auditOpen {
					m.auditMode = auditModeList
					m.focus = focusAudit
				} else {
					m.focus = focusResources
				}
				m.regionFilterOn = false
				m.regionFilter.Blur()
				m.loading = true
				m.err = nil
				m.pager.Page = 0
				cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd())
				if m.pricingMode {
					cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
				}
				if m.auditOpen {
					m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
					m.auditLoading = true
					m.resetAuditPaging()
					m.clearAuditPageCache()
					seq := m.nextAuditSeq()
					cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
				}

			case m.focus == focusDetails && m.detailsTab == detailsRelationships:
				if it, ok := m.related.SelectedItem().(relItem); ok && it.otherKey != "" {
					m.pushNav()
					if m.graphMode {
						m.graphRootKey = it.otherKey
						m.setContextFromKey(it.otherKey)
						m.syncNavigatorSelection()
						m.lens.SetFocus(graphlens.SideOut)
						m.lens.SetCursors(0, 0)
						m.focus = focusResources
						m.err = nil
						cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadNeighborsCmd())
						if m.pricingMode {
							cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
						}
					} else {
						m.jumpTo(it.service, it.region, it.otherKey)
						m.syncNavigatorSelection()
						m.loading = true
						m.err = nil
						cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd())
						if m.pricingMode {
							cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
						}
					}
				}

			case m.focus == focusActions:
				if it, ok := m.actions.SelectedItem().(actionItem); ok {
					m.pendingAct = it.id
					m.focus = focusConfirm
					m.confirm.SetValue("")
					m.confirm.Focus()
				}

			case m.focus == focusConfirm:
				cmds = append(cmds, m.tryRunActionCmd())

			case m.focus == focusServices:
				row, ok := m.nav.SelectedRow()
				if ok && row.Kind == navigator.RowService {
					m.nav.ToggleExpandedService(row.Service)
					if m.nav.ExpandedService() == row.Service {
						cmds = append(cmds, m.loadTypeCountsCmd(row.Service))
						if m.pricingMode {
							cmds = append(cmds, m.loadTypeCostAggCmd(row.Service))
						}
					}
					break
				}
				m.focus = focusResources

			case m.focus == focusResources:
				if m.graphMode {
					sel := m.lens.Selected()
					switch sel.Kind {
					case graphlens.SelectionGroup:
						m.lens.ToggleGroup()
					case graphlens.SelectionNeighbor:
						if sel.Neighbor.OtherKey == "" {
							m.statusLine = "no neighbor selected"
							break
						}
						m.pushNav()
						m.graphRootKey = sel.Neighbor.OtherKey
						m.setContextFromKey(sel.Neighbor.OtherKey)
						m.syncNavigatorSelection()
						m.lens.SetFocus(graphlens.SideOut)
						m.lens.SetCursors(0, 0)
						m.err = nil
						cmds = append(cmds, m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadNeighborsCmd())
					default:
						m.statusLine = "no selection"
					}
				} else {
					cmds = append(cmds, m.loadNeighborsCmd())
				}
			}

		case "left", "h":
			if m.focus == focusResources && m.graphMode {
				m.lens.FocusLeft()
				break
			}
			if m.focus == focusServices {
				row, ok := m.nav.SelectedRow()
				if !ok {
					break
				}
				if row.Kind == navigator.RowType {
					m.nav.CollapseService(row.Service)
					break
				}
				if row.Kind == navigator.RowService && m.nav.ExpandedService() == row.Service {
					m.nav.ToggleExpandedService(row.Service)
					break
				}
			}

		case "right", "l":
			if m.focus == focusResources && m.graphMode {
				m.lens.FocusRight()
				break
			}
			if m.focus == focusServices {
				row, ok := m.nav.SelectedRow()
				if !ok {
					break
				}
				if row.Kind == navigator.RowService {
					m.nav.ToggleExpandedService(row.Service)
					if m.nav.ExpandedService() == row.Service {
						cmds = append(cmds, m.loadTypeCountsCmd(row.Service))
						if m.pricingMode {
							cmds = append(cmds, m.loadTypeCostAggCmd(row.Service))
						}
					}
					break
				}
				if row.Kind == navigator.RowType {
					m.focus = focusResources
					break
				}
			}

		case "[":
			if m.focus == focusResources && !m.graphMode && !m.pager.OnFirstPage() {
				m.pager.PrevPage()
				m.loading = true
				m.err = nil
				cmds = append(cmds, m.loadResourcesCmd())
			} else if m.focus == focusAudit && !m.auditPager.OnFirstPage() {
				m.auditPager.PrevPage()
				m.auditLoading = true
				cmds = append(cmds, m.loadAuditEventsCmd())
			}
		case "]":
			if m.focus == focusResources && !m.graphMode && !m.pager.OnLastPage() {
				m.pager.NextPage()
				m.loading = true
				m.err = nil
				cmds = append(cmds, m.loadResourcesCmd())
			} else if m.focus == focusAudit && !m.auditPager.OnLastPage() {
				m.auditPager.NextPage()
				m.auditLoading = true
				cmds = append(cmds, m.loadAuditEventsCmd())
			}

		case "ctrl+r":
			m.loading = true
			m.err = nil
			if !m.offline {
				m.identityLoading = true
				m.identityErr = nil
			}
			cmds = append(cmds, m.loadIdentityCmd(), m.loadRegionsCmd(), m.loadRegionCountsCmd(), m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()), m.loadResourcesCmd(), m.loadNeighborsCmd())
			if m.pricingMode {
				cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
			}
			if m.auditOpen {
				m.auditLoading = true
				m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
				m.resetAuditPaging()
				m.clearAuditPageCache()
				seq := m.nextAuditSeq()
				cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
			}

		case " ":
			if m.focus == focusResources && m.graphMode {
				m.lens.ToggleGroup()
			} else if m.focus == focusRegions {
				if m.regionFilterOn {
					break
				}
				m.toggleSelectedRegionFromPicker()
				m.rebuildRegionTable("")
			} else if m.focus == focusDetails && m.detailsTab == detailsRaw {
				// allow space while scrolling raw
			}
		}

	case regionsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			m.knownRegions = msg.regions
			if m.selectedRegions == nil {
				m.selectedRegions = map[string]bool{}
			}
			if len(m.selectedRegions) == 0 {
				for _, r := range m.defaultRegionsForService() {
					m.selectedRegions[r] = true
				}
			}
			m.rebuildRegionTable("")
			cmds = append(cmds, m.loadRegionCountsCmd(), m.loadServiceCountsCmd(), m.loadTypeCountsCmd(m.nav.ExpandedService()))
			if m.pricingMode {
				cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
			}
			if m.auditOpen {
				m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
				m.auditLoading = true
				m.resetAuditPaging()
				m.clearAuditPageCache()
				seq := m.nextAuditSeq()
				cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
			}
		}

	case regionCountsLoadedMsg:
		m.err = msg.err
		if msg.err == nil && msg.service == m.selectedService && msg.typ == m.selectedType {
			m.regionSvcCounts = map[string]int{}
			for _, r := range msg.svcRows {
				m.regionSvcCounts[r.Region] = r.Count
			}
			m.regionTypeCounts = map[string]int{}
			for _, r := range msg.typRows {
				m.regionTypeCounts[r.Region] = r.Count
			}
			m.rebuildRegionTable("")
		}

	case serviceCountsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			m.serviceCounts = msg.rows
			m.nav.SetServiceCounts(msg.rows)
		}

	case typeCountsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			m.typeCounts = msg.rows
			m.nav.SetTypeCounts(msg.service, msg.rows)
		}

	case serviceCostAggLoadedMsg:
		m.err = msg.err
		if msg.err == nil && m.pricingMode {
			m.nav.SetServiceCostAgg(msg.rows)
		}

	case typeCostAggLoadedMsg:
		m.err = msg.err
		if msg.err == nil && m.pricingMode {
			m.nav.SetTypeCostAgg(msg.service, msg.rows)
		}

	case identityLoadedMsg:
		// Identity is "best effort": we keep the UI usable even if it fails.
		prevAccount := m.accountID
		m.identityLoading = false
		if msg.err != nil {
			m.identityErr = msg.err
			m.statusLine = "identity unavailable: " + msg.err.Error()
			break
		}
		m.identityErr = nil
		if msg.profileName != "" {
			m.profileName = msg.profileName
		}
		if msg.accountID != "" {
			m.accountID = msg.accountID
		}
		if msg.partition != "" {
			m.partition = msg.partition
		}
		// If we just learned the account context, refresh all scoped queries.
		if strings.TrimSpace(m.accountID) != "" && m.accountID != prevAccount {
			cmds = append(cmds,
				m.loadRegionsCmd(),
				m.loadRegionCountsCmd(),
				m.loadServiceCountsCmd(),
				m.loadTypeCountsCmd(m.nav.ExpandedService()),
				m.loadResourcesCmd(),
				m.loadNeighborsCmd(),
			)
			if m.auditOpen {
				m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
				m.auditLoading = true
				m.resetAuditPaging()
				m.clearAuditPageCache()
				seq := m.nextAuditSeq()
				cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
			}
		}

	case resourcesLoadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.totalResources = msg.total
			// Ensure total pages is always at least 1.
			if m.pager.PerPage <= 0 {
				m.pager.PerPage = 50
			}
			if msg.total <= 0 {
				m.pager.TotalPages = 1
				m.pager.Page = 0
			} else {
				m.pager.SetTotalPages(msg.total)
				if m.pager.Page > m.pager.TotalPages-1 {
					m.pager.Page = max(0, m.pager.TotalPages-1)
					m.loading = true
					cmds = append(cmds, m.loadResourcesCmd())
					break
				}
			}
			m.resourceSummaries = msg.summaries
			includeStored := strings.EqualFold(m.selectedService, "logs") && strings.EqualFold(m.selectedType, "logs:log-group")
			includeIAMKeyFields := strings.EqualFold(m.selectedService, "iam") && strings.EqualFold(m.selectedType, "iam:access-key")
			w := 80
			if m.paneMidW > 0 {
				w = m.paneMidW - 4
			}
			m.resources.SetColumns(buildResourceColumns(w, m.pricingMode, includeStored, includeIAMKeyFields))
			m.resources.SetRows(makeResourceRows(msg.summaries, m.pricingMode, m.styles, m.icons, includeStored, includeIAMKeyFields))
			if m.pendingSelectKey != "" {
				for idx := range msg.summaries {
					if msg.summaries[idx].Key == m.pendingSelectKey {
						m.resources.SetCursor(idx)
						break
					}
				}
				m.pendingSelectKey = ""
			} else {
				m.resources.SetCursor(0)
			}
			if len(msg.summaries) == 0 {
				m.neighborsKey = ""
				m.neighbors = nil
				m.related.SetItems(nil)
			} else {
				cmds = append(cmds, m.loadNeighborsCmd())
			}
		}

	case auditEventsLoadedMsg:
		m.auditLoading = false
		if msg.err != nil {
			m.auditDetailError = msg.err
			break
		}
		m.auditDetailError = nil
		m.auditEvents = msg.events
		m.auditTotal = msg.total
		if m.auditPager.PerPage <= 0 {
			m.auditPager.PerPage = 25
		}
		if msg.total <= 0 {
			m.auditPager.TotalPages = 1
			m.auditPager.Page = 0
		} else {
			m.auditPager.SetTotalPages(msg.total)
			if m.auditPager.Page > m.auditPager.TotalPages-1 {
				m.auditPager.Page = max(0, m.auditPager.TotalPages-1)
			}
		}
		m.resizeAuditWidgets()
		m.auditTable.SetRows(makeAuditRows(msg.events, m.styles, m.icons))
		m.auditTable.SetCursor(0)
		if m.auditMode == auditModeDetail {
			if ev, ok := m.selectedAuditEvent(); ok {
				cmds = append(cmds, m.loadAuditDetailCmd(ev.EventID))
			} else {
				m.auditDetail = nil
				m.auditDetailPretty = ""
				m.auditDetailViewport.SetContent("")
			}
		} else {
			m.auditDetail = nil
			m.auditDetailPretty = ""
		}

	case auditCursorPageLoadedMsg:
		if msg.seq != m.auditQuerySeq {
			break
		}
		m.auditLoading = false
		if msg.err != nil {
			m.auditDetailError = msg.err
			break
		}
		m.auditDetailError = nil
		m.putAuditPageCache(msg.cacheKey, msg.page)
		m.auditEvents = msg.page.Events
		m.auditHasNext = msg.page.HasNext
		m.auditHasPrev = msg.page.HasPrev
		m.auditNextCursor = msg.page.NextCursor
		m.auditPrevCursor = msg.page.PrevCursor
		m.auditPageNum = msg.pageNum
		if m.auditPageNum < 1 {
			m.auditPageNum = 1
		}
		m.auditPager.Page = max(0, m.auditPageNum-1)
		m.resizeAuditWidgets()
		m.auditTable.SetRows(makeAuditRows(msg.page.Events, m.styles, m.icons))
		m.auditTable.SetCursor(0)
		if m.auditMode == auditModeDetail {
			if ev, ok := m.selectedAuditEvent(); ok {
				cmds = append(cmds, m.loadAuditDetailCmd(ev.EventID))
			} else {
				m.auditMode = auditModeList
				m.auditDetail = nil
				m.auditDetailPretty = ""
				m.auditDetailViewport.SetContent("")
			}
		} else {
			m.auditDetail = nil
			m.auditDetailPretty = ""
		}

	case auditCountLoadedMsg:
		if msg.seq != m.auditQuerySeq {
			break
		}
		if msg.err != nil {
			m.auditDetailError = msg.err
			break
		}
		m.auditTotal = msg.total
		per := max(1, m.auditPageSize)
		pages := (msg.total + per - 1) / per
		m.auditPager.SetTotalPages(max(1, pages))

	case auditFacetsLoadedMsg:
		if msg.seq != m.auditQuerySeq {
			break
		}
		if msg.err != nil {
			m.auditDetailError = msg.err
			break
		}
		m.auditFacets = msg.facets
		m.applyAuditFacetsToPicker()

	case auditDetailLoadedMsg:
		if msg.err != nil {
			m.auditDetailError = msg.err
			break
		}
		if !msg.found {
			m.auditDetail = nil
			m.auditDetailError = nil
			m.auditDetailPretty = ""
			m.auditDetailViewport.SetContent("")
			break
		}
		d := msg.event
		m.auditDetail = &d
		m.auditDetailError = nil
		m.auditDetailPretty = formatAuditJSON(d.EventJSON)
		m.resizeAuditWidgets()
		m.auditDetailViewport.SetContent(wrapAuditPayload(m.auditDetailPretty, m.auditDetailViewport.Width))
		m.auditDetailViewport.GotoTop()

	case neighborsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			keyChanged := msg.key != m.neighborsKey
			m.neighborsKey = msg.key
			m.neighbors = msg.neighbors
			m.related.SetItems(neighborsToRelItems(msg.neighbors))
			m.lens.SetNeighbors(msg.neighbors, reverseKind)
			if keyChanged {
				m.lens.SetCursors(0, 0)
			}
		}

	case actionDoneMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.statusLine = msg.line
		}
		m.pendingAct = ""
		m.confirm.SetValue("")
		m.confirm.Blur()
		m.focus = focusResources

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width
		h := msg.Height
		left, mid, right := computePaneWidths(w)
		m.paneLeftW, m.paneMidW, m.paneRightW = left, mid, right

		// Keep one line for the statusbar at the bottom.
		usableH := max(10, h-8)
		m.nav.SetSize(max(10, left-4), max(4, usableH-2))

		m.resources.SetWidth(mid - 4)
		m.resources.SetHeight(h - 8)
		m.regions.SetSize(mid, min(12, h-8))
		m.actions.SetSize(mid, min(10, h-8))
		m.related.SetSize(max(10, right-4), h-8)
		m.raw.Width = max(10, right-4)
		m.raw.Height = h - 8
		m.help.Width = w
		m.statusbar, _ = m.statusbar.Update(msg)

		includeStored := strings.EqualFold(m.selectedService, "logs") && strings.EqualFold(m.selectedType, "logs:log-group")
		includeIAMKeyFields := strings.EqualFold(m.selectedService, "iam") && strings.EqualFold(m.selectedType, "iam:access-key")
		m.resources.SetColumns(buildResourceColumns(mid-4, m.pricingMode, includeStored, includeIAMKeyFields))
		m.regionTable.SetWidth(mid - 4)
		m.regionTable.SetHeight(max(6, h-12))
		m.regionTable.SetColumns(buildRegionColumns(mid - 4))
		m.regionFilter.Width = min(30, max(10, mid-16))
		m.resizeAuditWidgets()
		// Graph Lens side lists are sized dynamically in View(), but we still update height here.
		lensH := max(4, h-11)
		sideW := max(12, (mid-10)/3)
		m.lens.SetSize(sideW, sideW, lensH)

		// Match page size to viewport height as a reasonable default for paging.
		perPage := max(10, h-10)
		if perPage != m.pager.PerPage {
			m.pager.PerPage = perPage
			m.pager.Page = 0
			m.loading = true
			m.err = nil
			cmds = append(cmds, m.loadResourcesCmd())
		}
	}

	// Bubble components with internal focus state must be toggled explicitly.
	if m.focus == focusResources && !m.graphMode {
		m.resources.Focus()
	} else {
		m.resources.Blur()
	}
	if m.focus == focusRegions && !m.regionFilterOn {
		m.regionTable.Focus()
	} else {
		m.regionTable.Blur()
	}
	if m.focus == focusAudit && m.auditMode == auditModeList {
		m.auditTable.Focus()
	} else {
		m.auditTable.Blur()
	}

	// Delegate updates based on focus.
	switch m.focus {
	case focusServices:
		var cmd tea.Cmd
		before, _ := m.nav.SelectedRow()
		m.nav, cmd = m.nav.Update(msg)
		cmds = append(cmds, cmd)
		after, ok := m.nav.SelectedRow()
		if ok && (before.Kind != after.Kind || before.Service != after.Service || before.Type != after.Type) {
			switch after.Kind {
			case navigator.RowService:
				// Accordion behavior: when a service header is selected, expand it so the types
				// are visible immediately while browsing with up/down.
				if after.Service != "" && m.nav.ExpandedService() != after.Service {
					m.nav.ToggleExpandedService(after.Service)
				}
				if after.Service != "" && after.Service != m.selectedService {
					m.selectedService = after.Service
					// When browsing with arrows, select the first visible type for the service.
					// This keeps navigator movement deterministic and avoids jumping to a
					// non-first child from hard-coded defaults.
					m.selectedType = m.nav.FirstTypeForService(after.Service)
					if m.selectedType == "" {
						m.selectedType = defaultTypeForService(after.Service)
					}
					m.nav.SetSelection(m.selectedService, m.selectedType)
					m.applyServiceScope()
					m.rebuildRegionTable("")
					m.loading = true
					m.err = nil
					m.pager.Page = 0
					if m.graphMode {
						m.graphMode = false
						m.graphRootKey = ""
					}
					cmds = append(cmds, m.loadRegionCountsCmd(), m.loadServiceCountsCmd(), m.loadTypeCountsCmd(after.Service))
					if m.pricingMode {
						cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(after.Service))
					}
					cmds = append(cmds, m.loadResourcesCmd())
				} else if after.Service != "" {
					// Prefetch type counts so expansion is instant.
					cmds = append(cmds, m.loadTypeCountsCmd(after.Service))
					if m.pricingMode {
						cmds = append(cmds, m.loadTypeCostAggCmd(after.Service))
					}
				}
			case navigator.RowType:
				if after.Service != "" && after.Type != "" && (after.Service != m.selectedService || after.Type != m.selectedType) {
					m.selectedService = after.Service
					m.selectedType = after.Type
					m.nav.SetSelection(m.selectedService, m.selectedType)
					m.applyServiceScope()
					m.rebuildRegionTable("")
					m.loading = true
					m.err = nil
					m.pager.Page = 0
					if m.graphMode {
						m.graphMode = false
						m.graphRootKey = ""
					}
					cmds = append(cmds, m.loadRegionCountsCmd(), m.loadServiceCountsCmd())
					if m.pricingMode {
						cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(after.Service))
					}
					cmds = append(cmds, m.loadResourcesCmd())
				}
			}
		}
	case focusResources:
		var cmd tea.Cmd
		if m.graphMode {
			m.lens, cmd = m.lens.Update(msg)
		} else {
			m.resources, cmd = m.resources.Update(msg)
		}
		cmds = append(cmds, cmd)
	case focusDetails:
		if m.detailsTab == detailsRelationships {
			var cmd tea.Cmd
			m.related, cmd = m.related.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.detailsTab == detailsRaw {
			var cmd tea.Cmd
			m.raw, cmd = m.raw.Update(msg)
			cmds = append(cmds, cmd)
		}
	case focusFilter:
		before := m.filter.Value()
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		cmds = append(cmds, cmd)
		if after := m.filter.Value(); after != before {
			m.filterSeq++
			seq := m.filterSeq
			val := after
			cmds = append(cmds, tea.Tick(200*time.Millisecond, func(_ time.Time) tea.Msg {
				return filterDebouncedMsg{seq: seq, value: val}
			}))
		}
	case focusRegions:
		if m.regionFilterOn {
			before := m.regionFilter.Value()
			var cmd tea.Cmd
			m.regionFilter, cmd = m.regionFilter.Update(msg)
			cmds = append(cmds, cmd)
			if after := m.regionFilter.Value(); after != before {
				m.rebuildRegionTable("")
			}
		} else {
			var cmd tea.Cmd
			m.regionTable, cmd = m.regionTable.Update(msg)
			cmds = append(cmds, cmd)
		}
	case focusActions:
		var cmd tea.Cmd
		m.actions, cmd = m.actions.Update(msg)
		cmds = append(cmds, cmd)
	case focusConfirm:
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(msg)
		cmds = append(cmds, cmd)
	case focusAudit:
		var cmd tea.Cmd
		if m.auditMode == auditModeDetail {
			m.auditDetailViewport, cmd = m.auditDetailViewport.Update(msg)
		} else {
			m.auditTable, cmd = m.auditTable.Update(msg)
		}
		cmds = append(cmds, cmd)
	case focusAuditFilter:
		before := m.auditFilter.Value()
		var cmd tea.Cmd
		m.auditFilter, cmd = m.auditFilter.Update(msg)
		cmds = append(cmds, cmd)
		if after := m.auditFilter.Value(); after != before {
			m.auditFilterSeq++
			seq := m.auditFilterSeq
			val := after
			cmds = append(cmds, tea.Tick(200*time.Millisecond, func(_ time.Time) tea.Msg {
				return auditFilterDebouncedMsg{seq: seq, value: val}
			}))
		}
	}

	// Refresh neighbors on selection movement in the resource table.
	if km, ok := msg.(tea.KeyMsg); ok && m.focus == focusResources && !m.graphMode {
		switch km.String() {
		case "j", "k", "down", "up", "pgdown", "pgup", "G", "home", "end":
			cmds = append(cmds, m.loadNeighborsCmd())
			if m.detailsTab == detailsRaw {
				cmds = append(cmds, m.loadRawCmd())
			}
		}
	}
	// In graph mode, update Raw on neighbor selection movement so the details pane tracks selection.
	if km, ok := msg.(tea.KeyMsg); ok && m.focus == focusResources && m.graphMode {
		switch km.String() {
		case "j", "k", "down", "up", "pgdown", "pgup", "G", "home", "end":
			if m.detailsTab == detailsRaw {
				cmds = append(cmds, m.loadRawCmd())
			}
		}
	}
	if dm, ok := msg.(filterDebouncedMsg); ok {
		if m.focus == focusFilter && dm.seq == m.filterSeq && dm.value == m.filter.Value() {
			m.loading = true
			m.err = nil
			m.pager.Page = 0
			cmds = append(cmds, m.loadResourcesCmd())
		}
	}
	if dm, ok := msg.(auditFilterDebouncedMsg); ok {
		if m.focus == focusAuditFilter && dm.seq == m.auditFilterSeq && dm.value == m.auditFilter.Value() {
			m.auditLoading = true
			m.auditFilters.Text = strings.TrimSpace(dm.value)
			m.resetAuditPaging()
			m.clearAuditPageCache()
			seq := m.nextAuditSeq()
			cmds = append(cmds, tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)))
		}
	}

	if rm, ok := msg.(rawLoadedMsg); ok {
		if rm.err != nil {
			m.statusLine = "raw unavailable: " + rm.err.Error()
		} else {
			m.raw.SetContent(rm.content)
			m.raw.GotoTop()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleAuditKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.auditFacetOpen {
		switch msg.String() {
		case "esc":
			if m.auditFacetEventSearchOn {
				m.auditFacetEventSearchOn = false
				m.auditFacetEventSearch.Blur()
				return nil, true
			}
			m.auditFacetOpen = false
			m.focus = focusAudit
			return nil, true
		case "enter":
			if m.auditFacetEventSearchOn {
				m.auditFacetEventSearchOn = false
				m.auditFacetEventSearch.Blur()
				return nil, true
			}
			m.auditFilters.Actions = cloneBoolMap(m.auditFacetDraft.Actions)
			m.auditFilters.Services = cloneBoolMap(m.auditFacetDraft.Services)
			m.auditFilters.EventNames = cloneBoolMap(m.auditFacetDraft.EventNames)
			m.auditFilters.OnlyErrors = m.auditFacetDraft.OnlyErrors
			m.auditFacetOpen = false
			m.focus = focusAudit
			m.auditLoading = true
			m.resetAuditPaging()
			m.clearAuditPageCache()
			seq := m.nextAuditSeq()
			return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
		case "c", "C":
			m.auditFacetDraft.Actions = map[string]bool{}
			m.auditFacetDraft.Services = map[string]bool{}
			m.auditFacetDraft.EventNames = map[string]bool{}
			m.applyAuditFacetsToPicker()
			return nil, true
		case "tab", "right", "l":
			m.auditFacetSection = (m.auditFacetSection + 1) % 3
			return nil, true
		case "left", "h":
			m.auditFacetSection = (m.auditFacetSection + 2) % 3
			return nil, true
		case "/":
			if m.auditFacetSection == 2 {
				m.auditFacetEventSearchOn = true
				m.auditFacetEventSearch.Focus()
			}
			return nil, true
		case "e":
			m.auditFacetDraft.OnlyErrors = !m.auditFacetDraft.OnlyErrors
			return nil, true
		}

		if m.auditFacetEventSearchOn {
			before := m.auditFacetEventSearch.Value()
			var cmd tea.Cmd
			m.auditFacetEventSearch, cmd = m.auditFacetEventSearch.Update(msg)
			if m.auditFacetEventSearch.Value() != before {
				m.applyAuditFacetsToPicker()
			}
			return cmd, true
		}
		switch m.auditFacetSection {
		case 0:
			switch msg.String() {
			case "up", "k":
				if m.auditFacetActionsCursor > 0 {
					m.auditFacetActionsCursor--
				}
			case "down", "j":
				if m.auditFacetActionsCursor < 1 {
					m.auditFacetActionsCursor++
				}
			case " ":
				action := "create"
				if m.auditFacetActionsCursor == 1 {
					action = "delete"
				}
				if m.auditFacetDraft.Actions[action] {
					delete(m.auditFacetDraft.Actions, action)
				} else {
					m.auditFacetDraft.Actions[action] = true
				}
			}
			return nil, true
		case 1:
			n := len(m.auditFacets.Services)
			if n == 0 {
				return nil, true
			}
			if m.auditFacetServicesCursor >= n {
				m.auditFacetServicesCursor = n - 1
			}
			switch msg.String() {
			case "up", "k":
				if m.auditFacetServicesCursor > 0 {
					m.auditFacetServicesCursor--
				}
			case "down", "j":
				if m.auditFacetServicesCursor < n-1 {
					m.auditFacetServicesCursor++
				}
			case " ":
				svc := strings.TrimSpace(m.auditFacets.Services[m.auditFacetServicesCursor].Value)
				if svc != "" {
					if m.auditFacetDraft.Services[svc] {
						delete(m.auditFacetDraft.Services, svc)
					} else {
						m.auditFacetDraft.Services[svc] = true
					}
				}
			}
			return nil, true
		default:
			if msg.String() == " " {
				m.toggleSelectedAuditFacetEvent()
				return nil, true
			}
			var cmd tea.Cmd
			m.auditFacetEventNamePicker, cmd = m.auditFacetEventNamePicker.Update(msg)
			return cmd, true
		}
	}

	// While typing in the audit text filter, don't trigger global audit hotkeys.
	// Exit input mode explicitly with Enter/Esc/Tab, then hotkeys become active again.
	if m.focus == focusAuditFilter {
		switch msg.String() {
		case "esc", "tab":
			m.focus = focusAudit
			m.auditFilter.Blur()
			return nil, true
		case "enter", "ctrl+r":
			m.focus = focusAudit
			m.auditFilter.Blur()
			m.auditMode = auditModeList
			m.auditLoading = true
			m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
			m.resetAuditPaging()
			m.clearAuditPageCache()
			seq := m.nextAuditSeq()
			return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
		default:
			before := m.auditFilter.Value()
			var cmd tea.Cmd
			m.auditFilter, cmd = m.auditFilter.Update(msg)
			if after := m.auditFilter.Value(); after != before {
				m.auditFilterSeq++
				seq := m.auditFilterSeq
				val := after
				return tea.Batch(cmd, tea.Tick(200*time.Millisecond, func(_ time.Time) tea.Msg {
					return auditFilterDebouncedMsg{seq: seq, value: val}
				})), true
			}
			return cmd, true
		}
	}

	switch msg.String() {
	case "q", "ctrl+c", "?":
		return nil, false
	case "E":
		m.auditOpen = false
		m.auditMode = auditModeList
		m.auditFacetOpen = false
		m.auditFilter.Blur()
		m.focus = focusResources
		return nil, true
	case "R":
		if m.focus == focusFilter || m.focus == focusConfirm || (m.focus == focusRegions && m.regionFilterOn) {
			return nil, true
		}
		m.auditMode = auditModeList
		m.focus = focusRegions
		m.auditFilter.Blur()
		m.filter.Blur()
		m.regionFilterOn = false
		m.regionFilter.Blur()
		m.regionFilter.SetValue("")
		m.rebuildRegionTable("")
		return m.loadRegionCountsCmd(), true
	case "/":
		if m.auditMode == auditModeList && m.focus == focusAudit {
			m.focus = focusAuditFilter
			m.auditFilter.Focus()
		}
		return nil, true
	case "tab":
		if m.focus == focusAuditFilter {
			m.focus = focusAudit
			m.auditFilter.Blur()
		}
		return nil, true
	case "F":
		m.openAuditFacetModal()
		return nil, true
	case "T":
		switch normalizeAuditWindow(m.auditFilters.Window) {
		case "24h":
			m.auditFilters.Window = "7d"
		case "7d":
			m.auditFilters.Window = "30d"
		case "30d":
			m.auditFilters.Window = "all"
		default:
			m.auditFilters.Window = "24h"
		}
		m.auditLoading = true
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
	case "e":
		m.auditFilters.OnlyErrors = !m.auditFilters.OnlyErrors
		m.auditLoading = true
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
	case "+":
		switch m.auditPageSize {
		case 50:
			m.auditPageSize = 100
		case 100:
			m.auditPageSize = 200
		default:
			m.auditPageSize = 50
		}
		m.auditLoading = true
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
	case "-":
		switch m.auditPageSize {
		case 200:
			m.auditPageSize = 100
		case 100:
			m.auditPageSize = 50
		default:
			m.auditPageSize = 200
		}
		m.auditLoading = true
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
	case "esc":
		switch {
		case m.focus == focusAuditFilter:
			m.focus = focusAudit
			m.auditFilter.Blur()
		case m.auditMode == auditModeDetail:
			m.auditMode = auditModeList
			m.focus = focusAudit
		default:
			m.auditOpen = false
			m.auditMode = auditModeList
			m.auditFacetOpen = false
			m.auditFilter.Blur()
			m.focus = focusResources
		}
		return nil, true
	case "enter":
		if m.focus == focusAuditFilter {
			m.focus = focusAudit
			m.auditFilter.Blur()
			m.auditMode = auditModeList
			m.auditLoading = true
			m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
			m.resetAuditPaging()
			m.clearAuditPageCache()
			seq := m.nextAuditSeq()
			return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
		}
		if m.focus == focusAudit && m.auditMode == auditModeList {
			ev, ok := m.selectedAuditEvent()
			if !ok {
				m.statusLine = "no audit event selected"
				return nil, true
			}
			m.auditMode = auditModeDetail
			m.auditDetail = nil
			m.auditDetailError = nil
			m.auditDetailPretty = ""
			m.resizeAuditWidgets()
			m.auditDetailViewport.SetContent("(loading event detail...)")
			m.auditDetailViewport.GotoTop()
			return m.loadAuditDetailCmd(ev.EventID), true
		}
		return nil, true
	case "J":
		return m.jumpFromSelectedAuditEvent(), true
	case "[":
		if m.auditMode == auditModeList && m.focus == focusAudit && m.auditHasPrev {
			m.auditLoading = true
			return m.loadAuditPrevPageCmd(m.auditQuerySeq), true
		}
		return nil, true
	case "]":
		if m.auditMode == auditModeList && m.focus == focusAudit && m.auditHasNext {
			m.auditLoading = true
			return m.loadAuditNextPageCmd(m.auditQuerySeq), true
		}
		return nil, true
	case "ctrl+r":
		if m.focus == focusAuditFilter {
			m.focus = focusAudit
			m.auditFilter.Blur()
		}
		m.auditMode = auditModeList
		m.auditLoading = true
		m.auditFilters.Text = strings.TrimSpace(m.auditFilter.Value())
		m.resetAuditPaging()
		m.clearAuditPageCache()
		seq := m.nextAuditSeq()
		return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq)), true
	}

	if m.focus != focusAudit {
		return nil, false
	}
	if m.auditMode == auditModeDetail {
		var cmd tea.Cmd
		m.auditDetailViewport, cmd = m.auditDetailViewport.Update(msg)
		return cmd, true
	}
	// Audit key handling returns early from Update(), so restore table focus here.
	// Without this, returning detail->list can leave the table blurred and inert.
	if !m.auditTable.Focused() {
		m.auditTable.Focus()
	}
	var cmd tea.Cmd
	m.auditTable, cmd = m.auditTable.Update(msg)
	return cmd, true
}

func (m *model) jumpFromSelectedAuditEvent() tea.Cmd {
	ev, ok := m.selectedAuditEvent()
	if !ok {
		m.statusLine = "no audit event selected"
		return nil
	}
	if strings.TrimSpace(string(ev.ResourceKey)) == "" {
		m.statusLine = "resource not in inventory for this event"
		return nil
	}
	_, _, region, typ, _, err := graph.ParseResourceKey(ev.ResourceKey)
	if err != nil {
		m.statusLine = "cannot resolve target resource key"
		return nil
	}
	service := ""
	if parts := strings.SplitN(typ, ":", 2); len(parts) > 0 {
		service = parts[0]
	}
	m.pushNav()
	m.auditOpen = false
	m.auditMode = auditModeList
	m.auditFacetOpen = false
	m.auditFilter.Blur()
	m.jumpTo(service, region, ev.ResourceKey)
	m.syncNavigatorSelection()
	m.loading = true
	m.err = nil
	cmds := []tea.Cmd{
		m.loadServiceCountsCmd(),
		m.loadTypeCountsCmd(m.nav.ExpandedService()),
		m.loadResourcesCmd(),
		m.loadNeighborsCmd(),
	}
	if m.pricingMode {
		cmds = append(cmds, m.loadServiceCostAggCmd(), m.loadTypeCostAggCmd(m.nav.ExpandedService()))
	}
	return tea.Batch(cmds...)
}

func (m *model) resizeAuditWidgets() {
	w := m.width
	if w <= 0 {
		w = 100
	}
	h := m.height
	if h <= 0 {
		h = 32
	}

	tableW := max(20, w-4)
	m.auditTable.SetWidth(tableW)
	m.auditTable.SetHeight(max(8, h-11))
	m.auditTable.SetColumns(buildAuditColumns(tableW))
	m.auditFilter.Width = min(40, max(12, tableW-26))

	payloadW := max(20, w-8)
	if w >= 150 {
		leftW := max(36, w/3)
		payloadW = max(40, w-leftW-10)
	}
	payloadH := max(6, h-18)
	m.auditDetailViewport.Width = payloadW
	m.auditDetailViewport.Height = payloadH
	if strings.TrimSpace(m.auditDetailPretty) != "" {
		m.auditDetailViewport.SetContent(wrapAuditPayload(m.auditDetailPretty, payloadW))
	}
	eventW := max(20, (w-14)/3+6)
	eventH := max(8, h-18)
	m.auditFacetEventNamePicker.SetSize(eventW, eventH)
	m.auditFacetEventSearch.Width = max(16, eventW-10)
}

func formatAuditJSON(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "{}"
	}
	var doc any
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return string(trimmed)
	}
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return string(trimmed)
	}
	return string(pretty)
}

func wrapAuditPayload(s string, width int) string {
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
		out = append(out, wordwrap.String(ln, width))
	}
	return strings.Join(out, "\n")
}

func (m *model) putAuditPageCache(key string, page store.CloudTrailCursorPage) {
	if m == nil || strings.TrimSpace(key) == "" {
		return
	}
	if m.auditPageCache == nil {
		m.auditPageCache = map[string]store.CloudTrailCursorPage{}
	}
	if _, exists := m.auditPageCache[key]; !exists {
		m.auditPageCacheOrder = append(m.auditPageCacheOrder, key)
	}
	m.auditPageCache[key] = page
	const capN = 8
	for len(m.auditPageCacheOrder) > capN {
		evict := m.auditPageCacheOrder[0]
		m.auditPageCacheOrder = m.auditPageCacheOrder[1:]
		delete(m.auditPageCache, evict)
	}
}

func (m *model) clearAuditPageCache() {
	if m == nil {
		return
	}
	m.auditPageCache = map[string]store.CloudTrailCursorPage{}
	m.auditPageCacheOrder = nil
}

func (m *model) nextAuditSeq() int {
	m.auditQuerySeq++
	return m.auditQuerySeq
}

func (m *model) resetAuditPaging() {
	m.auditPageNum = 1
	m.auditHasPrev = false
	m.auditHasNext = false
	m.auditPrevCursor = nil
	m.auditNextCursor = nil
}

func (m *model) applyAuditFacetsToPicker() {
	items := make([]list.Item, 0, len(m.auditFacets.EventNames))
	search := strings.ToLower(strings.TrimSpace(m.auditFacetEventSearch.Value()))
	for _, fc := range m.auditFacets.EventNames {
		name := strings.TrimSpace(fc.Value)
		if name == "" {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(name), search) {
			continue
		}
		items = append(items, auditFacetEventItem{
			name:     name,
			count:    fc.Count,
			selected: m.auditFacetDraft.EventNames[name],
		})
	}
	m.auditFacetEventNamePicker.SetItems(items)
}

func (m *model) selectedAuditFacetItem() (auditFacetEventItem, bool) {
	it := m.auditFacetEventNamePicker.SelectedItem()
	v, ok := it.(auditFacetEventItem)
	return v, ok
}

func (m *model) toggleSelectedAuditFacetEvent() {
	it, ok := m.selectedAuditFacetItem()
	if !ok {
		return
	}
	name := strings.TrimSpace(it.name)
	if name == "" {
		return
	}
	if m.auditFacetDraft.EventNames == nil {
		m.auditFacetDraft.EventNames = map[string]bool{}
	}
	if m.auditFacetDraft.EventNames[name] {
		delete(m.auditFacetDraft.EventNames, name)
	} else {
		m.auditFacetDraft.EventNames[name] = true
	}
	m.applyAuditFacetsToPicker()
}

func (m *model) openAuditFacetModal() {
	m.auditFacetOpen = true
	m.auditFacetSection = 0
	m.focus = focusAuditFacets
	m.auditFacetDraft = cloneAuditFilters(m.auditFilters)
	if m.auditFacetDraft.Actions == nil {
		m.auditFacetDraft.Actions = map[string]bool{}
	}
	if m.auditFacetDraft.Services == nil {
		m.auditFacetDraft.Services = map[string]bool{}
	}
	if m.auditFacetDraft.EventNames == nil {
		m.auditFacetDraft.EventNames = map[string]bool{}
	}
	m.auditFacetEventSearchOn = false
	m.auditFacetEventSearch.Blur()
	m.applyAuditFacetsToPicker()
}

func (m model) View() string {
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
	if m.auditOpen {
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

func (m model) loadRawCmd() tea.Cmd {
	return func() tea.Msg {
		if m.graphMode {
			if sel := m.lens.Selected(); sel.Kind == graphlens.SelectionGroup {
				return rawLoadedMsg{content: "(group selected; no raw document)"}
			}
		}
		sel, ok := m.activeSelection()
		if !ok {
			return rawLoadedMsg{content: "(no selection)"}
		}
		n, err := m.st.GetResource(m.ctx, sel.Key)
		if err != nil {
			return rawLoadedMsg{key: sel.Key, err: err}
		}

		var raw any
		if len(n.Raw) == 0 {
			raw = nil
		} else if json.Valid(n.Raw) {
			raw = json.RawMessage(n.Raw)
		} else {
			raw = string(n.Raw)
		}

		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"resource_key": n.Key,
			"display_name": n.DisplayName,
			"service":      n.Service,
			"type":         n.Type,
			"region":       sel.Region,
			"primary_id":   n.PrimaryID,
			"arn":          n.Arn,
			"tags":         n.Tags,
			"attributes":   n.Attributes,
			"collected_at": n.CollectedAt,
			"source":       n.Source,
			"raw":          raw,
		}); err != nil {
			return rawLoadedMsg{key: sel.Key, err: err}
		}
		return rawLoadedMsg{key: sel.Key, content: strings.TrimSpace(buf.String())}
	}
}

func (m model) loadServiceCountsCmd() tea.Cmd {
	regions := m.selectedRegionSlice()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return serviceCountsLoadedMsg{rows: nil, err: nil}
		}
		rows, err := m.st.ListServiceCountsByRegions(m.ctx, m.accountID, regions)
		if err != nil {
			return serviceCountsLoadedMsg{err: err}
		}
		return serviceCountsLoadedMsg{rows: rows}
	}
}

func (m model) loadServiceCostAggCmd() tea.Cmd {
	regions := m.selectedRegionSlice()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return serviceCostAggLoadedMsg{rows: nil, err: nil}
		}
		rows, err := m.st.ListServiceCostAggByRegions(m.ctx, m.accountID, regions)
		if err != nil {
			return serviceCostAggLoadedMsg{err: err}
		}
		return serviceCostAggLoadedMsg{rows: rows}
	}
}

func (m model) loadRegionCountsCmd() tea.Cmd {
	service := strings.TrimSpace(m.selectedService)
	typ := strings.TrimSpace(m.selectedType)
	if service == "" {
		return nil
	}
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return regionCountsLoadedMsg{service: service, typ: typ, svcRows: nil, typRows: nil, err: nil}
		}
		svcRows, err := m.st.ListRegionCountsByService(m.ctx, m.accountID, service)
		if err != nil {
			return regionCountsLoadedMsg{service: service, typ: typ, err: err}
		}
		typRows, err := m.st.ListRegionCountsByServiceType(m.ctx, m.accountID, service, typ)
		if err != nil {
			return regionCountsLoadedMsg{service: service, typ: typ, err: err}
		}
		return regionCountsLoadedMsg{service: service, typ: typ, svcRows: svcRows, typRows: typRows}
	}
}

func (m model) loadTypeCountsCmd(service string) tea.Cmd {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil
	}
	regions := m.selectedRegionSlice()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return typeCountsLoadedMsg{service: service, rows: nil, err: nil}
		}
		rows, err := m.st.ListTypeCountsByServiceAndRegions(m.ctx, m.accountID, service, regions)
		if err != nil {
			return typeCountsLoadedMsg{service: service, err: err}
		}
		return typeCountsLoadedMsg{service: service, rows: rows}
	}
}

func (m model) loadTypeCostAggCmd(service string) tea.Cmd {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil
	}
	regions := m.selectedRegionSlice()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return typeCostAggLoadedMsg{service: service, rows: nil, err: nil}
		}
		rows, err := m.st.ListTypeCostAggByServiceAndRegions(m.ctx, m.accountID, service, regions)
		if err != nil {
			return typeCostAggLoadedMsg{service: service, err: err}
		}
		return typeCostAggLoadedMsg{service: service, rows: rows}
	}
}

func (m model) loadResourcesCmd() tea.Cmd {
	service := m.selectedService
	typ := m.selectedType
	filter := m.filters().Resource
	regions := m.selectedRegionSlice()
	page := m.pager.Page
	perPage := m.pager.PerPage
	return func() tea.Msg {
		if service == "" || typ == "" {
			return resourcesLoadedMsg{summaries: nil, total: 0, err: nil}
		}
		if strings.TrimSpace(m.accountID) == "" {
			return resourcesLoadedMsg{summaries: nil, total: 0, err: nil}
		}
		total, err := m.st.CountResourceSummariesByServiceTypeAndRegions(m.ctx, m.accountID, service, typ, regions, filter)
		if err != nil {
			return resourcesLoadedMsg{err: err}
		}
		offset := page * perPage
		summaries, err := m.st.ListResourceSummariesByServiceTypeAndRegionsPaged(m.ctx, m.accountID, service, typ, regions, filter, perPage, offset)
		if err != nil {
			return resourcesLoadedMsg{err: err}
		}
		return resourcesLoadedMsg{summaries: summaries, total: total}
	}
}

func (m model) loadNeighborsCmd() tea.Cmd {
	var key graph.ResourceKey
	if m.graphMode {
		if m.graphRootKey == "" {
			return func() tea.Msg { return neighborsLoadedMsg{key: "", neighbors: nil, err: nil} }
		}
		key = m.graphRootKey
	} else {
		s, ok := m.selectedSummary()
		if !ok {
			return func() tea.Msg { return neighborsLoadedMsg{key: "", neighbors: nil, err: nil} }
		}
		key = s.Key
	}
	return func() tea.Msg {
		neighbors, err := m.st.ListNeighbors(m.ctx, key)
		if err != nil {
			return neighborsLoadedMsg{key: key, err: err}
		}
		return neighborsLoadedMsg{key: key, neighbors: neighbors}
	}
}

func normalizeAuditWindow(window string) string {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "24h":
		return "24h"
	case "7d":
		return "7d"
	case "30d":
		return "30d"
	case "all":
		return "all"
	default:
		return "7d"
	}
}

func (m model) auditWindowBounds() (*time.Time, *time.Time) {
	now := time.Now().UTC()
	until := now
	switch normalizeAuditWindow(m.auditFilters.Window) {
	case "24h":
		s := now.Add(-24 * time.Hour)
		return &s, &until
	case "7d":
		s := now.Add(-7 * 24 * time.Hour)
		return &s, &until
	case "30d":
		s := now.Add(-30 * 24 * time.Hour)
		return &s, &until
	default:
		return nil, nil
	}
}

func sortedTrueKeys(mv map[string]bool) []string {
	if len(mv) == 0 {
		return nil
	}
	out := make([]string, 0, len(mv))
	for k, on := range mv {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (m model) auditQuery() store.CloudTrailEventQuery {
	since, until := m.auditWindowBounds()
	limit := m.auditPageSize
	if limit <= 0 {
		limit = 100
	}
	return store.CloudTrailEventQuery{
		Regions:    m.selectedAuditRegions(),
		Text:       strings.TrimSpace(m.auditFilters.Text),
		Actions:    sortedTrueKeys(m.auditFilters.Actions),
		Services:   sortedTrueKeys(m.auditFilters.Services),
		EventNames: sortedTrueKeys(m.auditFilters.EventNames),
		Since:      since,
		Until:      until,
		OnlyErrors: m.auditFilters.OnlyErrors,
		Limit:      limit,
	}
}

func auditCursorToken(c *store.CloudTrailCursor) string {
	if c == nil {
		return "-"
	}
	return strings.TrimSpace(c.EventTime) + "|" + strings.TrimSpace(c.EventID)
}

func (m model) auditSignature(q store.CloudTrailEventQuery) string {
	parts := []string{
		"r=" + strings.Join(q.Regions, ","),
		"t=" + q.Text,
		"a=" + strings.Join(q.Actions, ","),
		"s=" + strings.Join(q.Services, ","),
		"e=" + strings.Join(q.EventNames, ","),
		fmt.Sprintf("err=%t", q.OnlyErrors),
		fmt.Sprintf("l=%d", q.Limit),
	}
	if q.Since != nil {
		parts = append(parts, "since="+q.Since.UTC().Format(time.RFC3339Nano))
	}
	if q.Until != nil {
		parts = append(parts, "until="+q.Until.UTC().Format(time.RFC3339Nano))
	}
	return strings.Join(parts, ";")
}

func (m model) auditPageCacheKey(q store.CloudTrailEventQuery, pageNum int, after, before *store.CloudTrailCursor) string {
	return m.auditSignature(q) + fmt.Sprintf("|p=%d|after=%s|before=%s", pageNum, auditCursorToken(after), auditCursorToken(before))
}

func (m model) loadAuditPageCmd(seq int, pageNum int, after, before *store.CloudTrailCursor) tea.Cmd {
	q := m.auditQuery()
	cacheKey := m.auditPageCacheKey(q, pageNum, after, before)
	if page, ok := m.auditPageCache[cacheKey]; ok {
		return func() tea.Msg {
			return auditCursorPageLoadedMsg{
				seq:       seq,
				cacheKey:  cacheKey,
				page:      page,
				pageNum:   pageNum,
				fromCache: true,
			}
		}
	}
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return auditCursorPageLoadedMsg{
				seq:      seq,
				cacheKey: cacheKey,
				pageNum:  pageNum,
				page:     store.CloudTrailCursorPage{},
			}
		}
		page, err := m.st.ListCloudTrailEventsByCursor(m.ctx, m.accountID, q, after, before)
		if err != nil {
			return auditCursorPageLoadedMsg{seq: seq, cacheKey: cacheKey, pageNum: pageNum, err: err}
		}
		return auditCursorPageLoadedMsg{seq: seq, cacheKey: cacheKey, pageNum: pageNum, page: page}
	}
}

func (m model) loadAuditFirstPageCmd(seq int) tea.Cmd {
	return m.loadAuditPageCmd(seq, 1, nil, nil)
}

func (m model) loadAuditNextPageCmd(seq int) tea.Cmd {
	if m.auditNextCursor == nil {
		return nil
	}
	next := m.auditPageNum + 1
	if next <= 0 {
		next = 2
	}
	return m.loadAuditPageCmd(seq, next, m.auditNextCursor, nil)
}

func (m model) loadAuditPrevPageCmd(seq int) tea.Cmd {
	if m.auditPrevCursor == nil {
		return nil
	}
	prev := m.auditPageNum - 1
	if prev < 1 {
		prev = 1
	}
	return m.loadAuditPageCmd(seq, prev, nil, m.auditPrevCursor)
}

func (m model) loadAuditCountCmd(seq int) tea.Cmd {
	q := m.auditQuery()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return auditCountLoadedMsg{seq: seq, total: 0}
		}
		total, err := m.st.CountCloudTrailEventsByQuery(m.ctx, m.accountID, q)
		return auditCountLoadedMsg{seq: seq, total: total, err: err}
	}
}

func (m model) loadAuditFacetsCmd(seq int) tea.Cmd {
	q := m.auditQuery()
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return auditFacetsLoadedMsg{seq: seq, facets: store.CloudTrailFacets{}}
		}
		f, err := m.st.ListCloudTrailEventFacets(m.ctx, m.accountID, q, 200)
		return auditFacetsLoadedMsg{seq: seq, facets: f, err: err}
	}
}

// Legacy compatibility shim for older call-sites while audit commands migrate.
func (m model) loadAuditEventsCmd() tea.Cmd {
	seq := m.auditQuerySeq
	if seq <= 0 {
		seq = 1
	}
	return tea.Batch(m.loadAuditFirstPageCmd(seq), m.loadAuditCountCmd(seq), m.loadAuditFacetsCmd(seq))
}

func (m model) loadAuditDetailCmd(eventID string) tea.Cmd {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil
	}
	return func() tea.Msg {
		if strings.TrimSpace(m.accountID) == "" {
			return auditDetailLoadedMsg{found: false}
		}
		row, ok, err := m.st.GetCloudTrailEventByID(m.ctx, m.accountID, eventID)
		if err != nil {
			return auditDetailLoadedMsg{err: err}
		}
		return auditDetailLoadedMsg{event: row, found: ok}
	}
}

func neighborsToRelItems(neighbors []store.Neighbor) []list.Item {
	items := make([]list.Item, 0, len(neighbors))
	for _, n := range neighbors {
		kind := n.Kind
		if n.Dir == "in" {
			kind = reverseKind(kind)
		}
		title := n.DisplayName
		if title == "" {
			title = n.PrimaryID
		}
		items = append(items, relItem{
			kind:     kind,
			dir:      n.Dir,
			otherKey: n.OtherKey,
			service:  n.Service,
			region:   n.Region,
			typ:      n.Type,
			title:    title,
		})
	}
	return items
}

func reverseKind(kind string) string {
	switch kind {
	case "attached-to":
		return "attached-by"
	case "member-of":
		return "has-member"
	case "contains":
		return "contained-by"
	case "uses":
		return "used-by"
	case "targets":
		return "targeted-by"
	case "forwards-to":
		return "forwarded-from"
	case "belongs-to":
		return "has-child"
	default:
		return kind
	}
}

func (m model) actionItemsForSelection() []list.Item {
	sel, ok := m.activeSelection()
	if !ok {
		return []list.Item{}
	}
	node, err := m.st.GetResource(m.ctx, sel.Key)
	if err != nil {
		return []list.Item{}
	}

	var items []list.Item
	for _, id := range actionsRegistry.ListIDs() {
		a, _ := actionsRegistry.Get(id)
		if a == nil {
			continue
		}
		if a.Applicable(node) {
			items = append(items, actionItem{id: id, title: a.Title(), desc: a.Description(), risk: a.Risk()})
		}
	}
	return items
}

func (m model) tryRunActionCmd() tea.Cmd {
	sel, ok := m.activeSelection()
	if !ok {
		m.statusLine = "no selection"
		m.focus = focusResources
		return nil
	}
	if m.pendingAct == "" {
		m.statusLine = "no action selected"
		m.focus = focusResources
		return nil
	}
	if m.confirm.Value() != sel.PrimaryID {
		m.statusLine = "confirmation mismatch"
		return nil
	}

	profile := m.profileName
	if profile == "" {
		profile = "default"
	}
	actionID := m.pendingAct
	key := sel.Key

	m.loading = true
	m.err = nil
	m.statusLine = ""

	return func() tea.Msg {
		res, err := core.RunAction(m.ctx, m.st, actionID, key, profile)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{line: fmt.Sprintf("action %s %s (%s)", res.ActionID, res.Status, res.ActionRunID)}
	}
}

func (m model) loadRegionsCmd() tea.Cmd {
	return func() tea.Msg {
		regions := []string{}
		// If we don't know the account yet, don't leak regions from other accounts' cached data.
		// Prefer AWS discovery (online) or return an empty list (offline).
		if strings.TrimSpace(m.accountID) != "" {
			rs, err := m.st.ListDistinctRegions(m.ctx, m.accountID)
			if err != nil {
				return regionsLoadedMsg{err: err}
			}
			regions = rs
		}

		if len(regions) == 0 && !m.offline && m.loader != nil {
			// Fresh DB or unknown account: fall back to AWS region discovery so region selection works before scanning.
			cfg, _, loadErr := m.loader.Load(m.ctx, m.profileName, "us-east-1")
			if loadErr == nil {
				discovered, listErr := m.loader.ListEnabledRegions(m.ctx, cfg)
				if listErr == nil {
					regions = discovered
				} else {
					return regionsLoadedMsg{err: listErr}
				}
			} else {
				return regionsLoadedMsg{err: loadErr}
			}
		}
		// Always include "global" so global-scoped services can select it explicitly.
		hasGlobal := false
		for _, r := range regions {
			if r == "global" {
				hasGlobal = true
				break
			}
		}
		if !hasGlobal {
			regions = append(regions, "global")
		}
		return regionsLoadedMsg{regions: regions}
	}
}

func buildRegionColumns(width int) []table.Column {
	// width is content width (border already accounted for elsewhere).
	if width <= 0 {
		width = 60
	}
	selW := 3
	regionW := 14
	svcW := 7
	typeW := 7
	sep := 4
	if regionW+svcW+typeW+selW+sep > width {
		regionW = max(8, width-(svcW+typeW+selW+sep))
	}
	return []table.Column{
		{Title: "Sel", Width: selW},
		{Title: "Region", Width: regionW},
		{Title: "Svc", Width: svcW},
		{Title: "Type", Width: typeW},
	}
}

func (m model) loadIdentityCmd() tea.Cmd {
	if m.offline || m.loader == nil {
		return nil
	}
	profile := effectiveProfile(m.profileName)
	region := "us-east-1"
	// If a regional selection exists, prefer that (helps Gov/China partitions).
	if rs := m.selectedRegionSlice(); len(rs) > 0 && rs[0] != "global" {
		region = rs[0]
	}
	return func() tea.Msg {
		_, id, err := m.loader.Load(m.ctx, profile, region)
		if err != nil {
			return identityLoadedMsg{err: err}
		}
		return identityLoadedMsg{
			profileName: profile,
			accountID:   id.AccountID,
			partition:   id.Partition,
			arn:         id.Arn,
		}
	}
}

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

func buildResourceColumns(width int, includeCost bool, includeStored bool, includeIAMKeyFields bool) []table.Column {
	// Reserve widths for non-name fields; name gets the remainder.
	if width <= 0 {
		width = 80
	}
	regionW := 10
	typeW := 18
	statusW := 12 // includes optional status icon prefix
	storedW := 10
	ageW := 6
	lastUsedW := 16
	idW := 22
	createdW := 19
	costW := 10
	sep := 6 // padding-ish
	fixed := regionW + typeW + statusW + idW + createdW + sep
	if includeStored {
		fixed += storedW
	}
	if includeIAMKeyFields {
		fixed += ageW + lastUsedW
	}
	if includeCost {
		fixed += costW
	}
	nameW := width - fixed
	if nameW < 12 {
		nameW = 12
	}
	cols := []table.Column{
		{Title: "Name", Width: nameW},
		{Title: "Type", Width: typeW},
		{Title: "Region", Width: regionW},
		{Title: "Status", Width: statusW},
	}
	if includeIAMKeyFields {
		cols = append(cols,
			table.Column{Title: "Age", Width: ageW},
			table.Column{Title: "Last Used", Width: lastUsedW},
		)
	}
	if includeStored {
		cols = append(cols, table.Column{Title: "Stored", Width: storedW})
	}
	if includeCost {
		cols = append(cols, table.Column{Title: "Est/mo", Width: costW})
	}
	cols = append(cols,
		table.Column{Title: "Created", Width: createdW},
		table.Column{Title: "ID", Width: idW},
	)
	return cols
}

func buildAuditColumns(width int) []table.Column {
	if width <= 0 {
		width = 100
	}
	timeW := 19
	ageW := 7
	actionW := 7
	serviceW := 12
	eventW := 20
	regionW := 11
	actorW := 14
	sep := 8
	fixed := timeW + ageW + actionW + serviceW + eventW + regionW + actorW + sep
	resourceW := width - fixed
	if resourceW < 16 {
		resourceW = 16
	}
	return []table.Column{
		{Title: "Time", Width: timeW},
		{Title: "Age", Width: ageW},
		{Title: "Action", Width: actionW},
		{Title: "Service", Width: serviceW},
		{Title: "Event", Width: eventW},
		{Title: "Resource", Width: resourceW},
		{Title: "Region", Width: regionW},
		{Title: "Actor", Width: actorW},
	}
}

func ageLabel(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func makeAuditRows(events []store.CloudTrailEventSummary, styles theme.Styles, ic icons.Set) []table.Row {
	rows := make([]table.Row, 0, len(events))
	for _, ev := range events {
		when := ev.EventTime.UTC().Format("2006-01-02 15:04:05")
		action := strings.ToUpper(strings.TrimSpace(ev.Action))
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(ev.Action), 2)
		}
		switch strings.ToLower(strings.TrimSpace(ev.Action)) {
		case "create":
			action = styles.Good.Render(icon + action)
		case "delete":
			action = styles.Bad.Render(icon + action)
		default:
			action = styles.Dim.Render(icon + action)
		}

		resource := firstNonEmpty(strings.TrimSpace(ev.ResourceName), strings.TrimSpace(ev.ResourceArn), strings.TrimSpace(ev.ResourceType), "-")
		actor := firstNonEmpty(strings.TrimSpace(ev.Username), strings.TrimSpace(ev.PrincipalArn), "-")
		rows = append(rows, table.Row{
			styles.Dim.Render(when),
			styles.Dim.Render(ageLabel(ev.EventTime)),
			action,
			ev.Service,
			ev.EventName,
			resource,
			styles.Dim.Render(ev.Region),
			actor,
		})
	}
	return rows
}

func makeResourceRows(ss []store.ResourceSummary, includeCost bool, styles theme.Styles, ic icons.Set, includeStored bool, includeIAMKeyFields bool) []table.Row {
	rows := make([]table.Row, 0, len(ss))
	for _, s := range ss {
		name := strings.TrimSpace(s.DisplayName)
		if name == "" || name == s.PrimaryID {
			if tn := strings.TrimSpace(s.Tags["Name"]); tn != "" {
				name = tn
			}
		}
		if name == "" {
			name = s.PrimaryID
		}

		statusRaw := statusFromAttrs(s.Attributes)
		status := renderStatusForTable(statusRaw, styles, ic)
		age := ""
		lastUsed := ""
		if includeIAMKeyFields {
			age = renderAgeDaysForTable(s.Attributes, styles)
			lastUsed = renderLastUsedForTable(s.Attributes, styles)
		}
		stored := ""
		if includeStored {
			stored = renderStoredGiBForTable(s.Attributes, styles)
		}
		created := ""
		if v, ok := s.Attributes["created_at"].(string); ok {
			created = v
		} else {
			// Unknown or not available from the AWS API for this resource type.
			created = "-"
		}
		id := s.PrimaryID
		if len(id) > 22 {
			id = id[len(id)-22:]
		}
		costStr := ""
		if includeCost {
			if s.EstMonthlyUSD == nil {
				costStr = styles.Dim.Render("-")
			} else {
				costStr = cost.FormatUSDPerMonthTable(*s.EstMonthlyUSD)
			}
		}
		base := table.Row{
			name,
			styles.Dim.Render(s.Type),
			styles.Dim.Render(s.Region),
			status,
		}
		if includeIAMKeyFields {
			base = append(base, age, lastUsed)
		}
		if includeStored {
			base = append(base, stored)
		}
		if includeCost {
			base = append(base, costStr)
		}
		base = append(base,
			styles.Dim.Render(created),
			id,
		)
		rows = append(rows, base)
	}
	return rows
}

func renderAgeDaysForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	var n int64 = -1
	switch v := attrs["age_days"].(type) {
	case int:
		n = int64(v)
	case int64:
		n = v
	case float64:
		n = int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			n = i
		}
	case string:
		if i, err := json.Number(strings.TrimSpace(v)).Int64(); err == nil {
			n = i
		}
	}
	if n < 0 {
		return styles.Dim.Render("-")
	}
	return fmt.Sprintf("%dd", n)
}

func renderLastUsedForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	if v, ok := attrs["last_used_at"].(string); ok {
		v = strings.TrimSpace(v)
		if v == "" || v == "-" {
			return styles.Dim.Render("-")
		}
		return v
	}
	return styles.Dim.Render("-")
}

func renderStoredGiBForTable(attrs map[string]any, styles theme.Styles) string {
	if attrs == nil {
		return styles.Dim.Render("-")
	}
	if v, ok := attrs["storedGiB"].(float64); ok {
		if v <= 0 {
			return styles.Dim.Render("0GiB")
		}
		return fmt.Sprintf("%.2fGiB", v)
	}

	var b float64
	switch x := attrs["storedBytes"].(type) {
	case int64:
		b = float64(x)
	case int:
		b = float64(x)
	case float64:
		b = x
	case float32:
		b = float64(x)
	case json.Number:
		if f, err := x.Float64(); err == nil {
			b = f
		}
	case string:
		if n, err := json.Number(strings.TrimSpace(x)).Float64(); err == nil {
			b = n
		}
	}
	if b <= 0 {
		return styles.Dim.Render("-")
	}
	gib := b / float64(1024*1024*1024)
	if gib <= 0 {
		return styles.Dim.Render("0GiB")
	}
	return fmt.Sprintf("%.2fGiB", gib)
}

func statusFromAttrs(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	// Common keys across providers.
	for _, k := range []string{
		"state",      // ec2/elbv2/lambda
		"status",     // rds/ecs/dynamodb
		"keyState",   // kms
		"lastStatus", // ecs task
		"desiredStatus",
	} {
		if v, ok := attrs[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func renderStatusForTable(raw string, styles theme.Styles, ic icons.Set) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(""), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + "-")
	}
	s := strings.ToLower(raw)
	switch s {
	// Good-ish.
	case "running", "available", "active", "enabled", "attached", "inuse", "in-use", "ok", "healthy", "console":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Good.Render(icon + raw)
	// In progress / transitional.
	case "pending", "provisioning", "creating", "updating", "modifying", "starting", "stopping", "rebooting", "initializing", "backing-up":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Warn.Render(icon + raw)
	// Bad / failure.
	case "failed", "error", "deleted", "deleting", "terminated", "unhealthy":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Bad.Render(icon + raw)
	// Neutral/dim.
	case "stopped", "inactive", "disabled", "detached", "programmatic", "unknown":
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + raw)
	default:
		// Preserve original string; use dim for unknown statuses to reduce noise.
		icon := ""
		if ic != nil {
			icon = icons.Pad(ic.Status(raw), 2)
		} else {
			icon = icons.Pad("", 2)
		}
		return styles.Dim.Render(icon + raw)
	}
}

func defaultTypeForService(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "ec2":
		return "ec2:instance"
	case "ecs":
		return "ecs:service"
	case "elbv2":
		return "elbv2:load-balancer"
	case "iam":
		return "iam:role"
	case "s3":
		return "s3:bucket"
	case "rds":
		return "rds:db-instance"
	case "logs":
		return "logs:log-group"
	case "lambda":
		return "lambda:function"
	case "dynamodb":
		return "dynamodb:table"
	case "sqs":
		return "sqs:queue"
	case "sns":
		return "sns:topic"
	case "kms":
		return "kms:key"
	case "secretsmanager":
		return "secretsmanager:secret"
	case "autoscaling":
		return "autoscaling:group"
	case "sagemaker":
		return "sagemaker:endpoint"
	case "identitycenter":
		return "identitycenter:permission-set"
	case "cloudtrail":
		return "cloudtrail:trail"
	case "config":
		return "config:recorder"
	case "guardduty":
		return "guardduty:detector"
	case "securityhub":
		return "securityhub:hub"
	case "accessanalyzer":
		return "accessanalyzer:analyzer"
	case "wafv2":
		return "wafv2:web-acl"
	case "acm":
		return "acm:certificate"
	case "cloudfront":
		return "cloudfront:distribution"
	case "apigateway":
		return "apigateway:rest-api"
	case "ecr":
		return "ecr:repository"
	case "eks":
		return "eks:cluster"
	case "elasticache":
		return "elasticache:replication-group"
	case "opensearch":
		return "opensearch:domain"
	case "redshift":
		return "redshift:cluster"
	case "msk":
		return "msk:cluster"
	case "efs":
		return "efs:file-system"
	default:
		return ""
	}
}

func fallbackTypesForService(service string) []string {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "ec2":
		return []string{"ec2:instance", "ec2:volume", "ec2:snapshot", "ec2:ami", "ec2:nat-gateway", "ec2:eip", "ec2:network-interface", "ec2:route-table", "ec2:nacl", "ec2:internet-gateway", "ec2:launch-template", "ec2:key-pair", "ec2:placement-group", "ec2:security-group", "ec2:subnet", "ec2:vpc"}
	case "ecs":
		return []string{"ecs:service", "ecs:cluster", "ecs:task", "ecs:task-definition"}
	case "elbv2":
		return []string{"elbv2:load-balancer", "elbv2:target-group", "elbv2:listener", "elbv2:rule"}
	case "iam":
		return []string{"iam:user", "iam:group", "iam:access-key", "iam:role", "iam:policy"}
	case "s3":
		return []string{"s3:bucket"}
	case "rds":
		return []string{"rds:db-instance", "rds:db-cluster", "rds:db-subnet-group"}
	case "lambda":
		return []string{"lambda:function"}
	case "dynamodb":
		return []string{"dynamodb:table"}
	case "sqs":
		return []string{"sqs:queue"}
	case "sns":
		return []string{"sns:topic", "sns:subscription"}
	case "kms":
		return []string{"kms:key", "kms:alias"}
	case "secretsmanager":
		return []string{"secretsmanager:secret"}
	case "logs":
		return []string{"logs:log-group"}
	case "autoscaling":
		return []string{"autoscaling:group", "autoscaling:launch-configuration", "autoscaling:instance"}
	case "sagemaker":
		return []string{"sagemaker:endpoint", "sagemaker:endpoint-config", "sagemaker:model", "sagemaker:notebook-instance", "sagemaker:training-job", "sagemaker:processing-job", "sagemaker:transform-job", "sagemaker:domain", "sagemaker:user-profile"}
	case "identitycenter":
		return []string{"identitycenter:permission-set", "identitycenter:assignment", "identitycenter:user", "identitycenter:group", "identitycenter:instance"}
	case "cloudtrail":
		return []string{"cloudtrail:trail"}
	case "config":
		return []string{"config:recorder", "config:delivery-channel"}
	case "guardduty":
		return []string{"guardduty:detector"}
	case "securityhub":
		return []string{"securityhub:hub", "securityhub:standard-subscription"}
	case "accessanalyzer":
		return []string{"accessanalyzer:analyzer"}
	case "wafv2":
		return []string{"wafv2:web-acl"}
	case "acm":
		return []string{"acm:certificate"}
	case "cloudfront":
		return []string{"cloudfront:distribution"}
	case "apigateway":
		return []string{"apigateway:rest-api", "apigateway:domain-name"}
	case "ecr":
		return []string{"ecr:repository"}
	case "eks":
		return []string{"eks:cluster", "eks:nodegroup"}
	case "elasticache":
		return []string{"elasticache:replication-group", "elasticache:cache-cluster", "elasticache:subnet-group"}
	case "opensearch":
		return []string{"opensearch:domain"}
	case "redshift":
		return []string{"redshift:cluster", "redshift:subnet-group"}
	case "msk":
		return []string{"msk:cluster"}
	case "efs":
		return []string{"efs:file-system", "efs:mount-target", "efs:access-point"}
	default:
		if t := defaultTypeForService(service); t != "" {
			return []string{t}
		}
		return nil
	}
}

type Options struct {
	Profile string
	Icons   string
}

func Run(ctx context.Context, st *store.Store, opts Options) error {
	serviceIDs := registry.ListIDs()
	sort.Strings(serviceIDs)
	selected := ""
	for _, id := range serviceIDs {
		if id == "ec2" {
			selected = "ec2"
			break
		}
	}
	if selected == "" && len(serviceIDs) > 0 {
		selected = serviceIDs[0]
	}
	selectedType := defaultTypeForService(selected)

	nav := navigator.New(serviceIDs, fallbackTypesForService)
	nav.SetSelection(selected, selectedType)
	if selected != "" {
		nav.ToggleExpandedService(selected)
	}

	resTable := table.New(table.WithColumns(buildResourceColumns(80, false, false, false)), table.WithRows(nil))
	resTable.SetStyles(table.DefaultStyles())

	lens := graphlens.New()

	filter := textinput.New()
	filter.Placeholder = "substring"
	filter.CharLimit = 100
	filter.Width = 30

	regionsList := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	regionsList.SetShowHelp(false)
	regionsList.Title = ""

	regionTable := table.New(table.WithColumns(buildRegionColumns(60)), table.WithRows(nil))
	regionTable.SetStyles(table.DefaultStyles())

	regionFilter := textinput.New()
	regionFilter.Placeholder = "type to filter (press /)"
	regionFilter.CharLimit = 80
	regionFilter.Width = 24

	auditTable := table.New(table.WithColumns(buildAuditColumns(100)), table.WithRows(nil))
	auditTable.SetStyles(table.DefaultStyles())

	auditFilter := textinput.New()
	auditFilter.Placeholder = "event/resource/actor"
	auditFilter.CharLimit = 120
	auditFilter.Width = 24

	auditFacetSearch := textinput.New()
	auditFacetSearch.Placeholder = "search event type"
	auditFacetSearch.CharLimit = 120
	auditFacetSearch.Width = 24

	auditFacetList := list.New([]list.Item{}, list.NewDefaultDelegate(), 32, 12)
	auditFacetList.SetShowHelp(false)
	auditFacetList.SetShowStatusBar(false)
	auditFacetList.SetFilteringEnabled(false)
	auditFacetList.Title = ""

	actionsList := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	actionsList.SetShowHelp(false)
	actionsList.Title = ""

	relatedList := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	relatedList.SetShowHelp(false)
	relatedList.Title = ""

	confirm := textinput.New()
	confirm.Placeholder = "type id to confirm"
	confirm.CharLimit = 200
	confirm.Width = 40

	m := model{
		ctx:              ctx,
		st:               st,
		dbPath:           st.DBPath(),
		offline:          st.Offline(),
		build:            buildLabel(),
		loader:           aws.NewLoader(),
		focus:            focusResources,
		detailsTab:       detailsSummary,
		nav:              nav,
		resources:        resTable,
		lens:             lens,
		filter:           filter,
		regions:          regionsList,
		regionTable:      regionTable,
		regionSvcCounts:  map[string]int{},
		regionTypeCounts: map[string]int{},
		regionFilter:     regionFilter,
		auditTable:       auditTable,
		auditMode:        auditModeList,
		auditFilter:      auditFilter,
		auditPager:       paginator.New(paginator.WithPerPage(25)),
		auditPageNum:     1,
		auditPageSize:    100,
		auditFilters: auditFilters{
			Actions:    map[string]bool{},
			Services:   map[string]bool{},
			EventNames: map[string]bool{},
			Window:     "7d",
		},
		auditFacetEventSearch:     auditFacetSearch,
		auditFacetEventNamePicker: auditFacetList,
		auditPageCache:            map[string]store.CloudTrailCursorPage{},
		auditDetailViewport:       viewport.New(30, 10),
		related:                   relatedList,
		raw:                       viewport.New(30, 10),
		actions:                   actionsList,
		confirm:                   confirm,
		selectedService:           selected,
		selectedType:              selectedType,
		loading:                   true,
		identityLoading:           !st.Offline(),
		selectedRegions:           map[string]bool{},
		pager:                     paginator.New(paginator.WithPerPage(50)),
		help:                      help.New(),
		keys: keyMap{
			Quit:          key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
			Focus:         key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus next")),
			Filter:        key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
			Regions:       key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "regions")),
			Actions:       key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "actions")),
			Refresh:       key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh")),
			Theme:         key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "theme")),
			Graph:         key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "graph")),
			Audit:         key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "audit")),
			AuditDetail:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "audit detail")),
			AuditJump:     key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "audit jump")),
			AuditFacets:   key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "audit facets")),
			AuditWindow:   key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "audit window")),
			AuditErrors:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "audit errors")),
			AuditPage:     key.NewBinding(key.WithKeys("+", "-"), key.WithHelp("+/-", "audit page size")),
			Pricing:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pricing")),
			PrevPage:      key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev page")),
			NextPage:      key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next page")),
			Back:          key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "back")),
			Help:          key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
			PaneNav:       key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "navigator")),
			PaneResources: key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "resources")),
			PaneDetails:   key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "details")),
			Summary:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "summary")),
			Related:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "related")),
			Raw:           key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "raw")),
		},
	}
	m.iconMode = strings.TrimSpace(opts.Icons)
	if m.offline {
		m.loader = nil
		m.identityLoading = false
	}

	tm, _ := theme.NewManager(theme.Options{})
	m.theme = tm
	if tm != nil {
		c1, c2, c3, c4 := statusbarColorConfigs(tm.Theme().Palette)
		m.statusbar = statusbar.New(c1, c2, c3, c4)
	} else {
		m.statusbar = statusbar.New(statusbar.ColorConfig{}, statusbar.ColorConfig{}, statusbar.ColorConfig{}, statusbar.ColorConfig{})
	}
	m.applyTheme()

	// Icons (default ASCII; overridable by env or CLI flag).
	modeStr := strings.TrimSpace(m.iconMode)
	if modeStr == "" {
		modeStr = strings.TrimSpace(os.Getenv("AWSCOPE_ICONS"))
	}
	set := icons.New(icons.ParseMode(modeStr))
	m.icons = set
	m.nav.SetIcons(set)
	m.lens.SetIcons(set)

	// Prefer CLI/env profile as the "active" profile; fall back to DB metadata.
	profileExplicit := strings.TrimSpace(opts.Profile) != "" || strings.TrimSpace(os.Getenv("AWS_PROFILE")) != ""
	m.profileName = effectiveProfile(opts.Profile)

	// If we have cached profile->account metadata, use it to scope the UI immediately (especially in --offline mode).
	if meta, ok, err := st.LookupProfile(ctx, m.profileName); err == nil && ok {
		if m.accountID == "" {
			m.accountID = meta.AccountID
		}
		if m.partition == "" {
			m.partition = meta.Partition
		}
	} else if err != nil {
		m.statusLine = "profile lookup failed: " + err.Error()
	}

	// If the profile wasn't explicitly chosen and we don't have cached metadata, fall back to the last used profile.
	if !profileExplicit && strings.TrimSpace(m.accountID) == "" {
		if meta, err := st.GetLastUsedProfile(ctx); err == nil {
			if meta.ProfileName != "" {
				m.profileName = meta.ProfileName
			}
			if m.accountID == "" {
				m.accountID = meta.AccountID
			}
			if m.partition == "" {
				m.partition = meta.Partition
			}
		}
	}

	// In offline mode, we can't resolve STS identity. If we don't have cached metadata, keep the UI scoped to "no account".
	if m.offline && strings.TrimSpace(m.accountID) == "" {
		m.statusLine = fmt.Sprintf("no cached inventory for profile %q (run `awscope scan --profile %s ...`)", m.profileName, m.profileName)
	}

	m.applyServiceScope()

	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func effectiveProfile(in string) string {
	if strings.TrimSpace(in) != "" {
		return strings.TrimSpace(in)
	}
	if env := strings.TrimSpace(os.Getenv("AWS_PROFILE")); env != "" {
		return env
	}
	return "default"
}

func buildLabel() string {
	info, ok := debug.ReadBuildInfo()
	if ok && info != nil {
		var rev string
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				rev = s.Value
				break
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return rev
		}
		if v := strings.TrimSpace(info.Main.Version); v != "" {
			return v
		}
	}
	return "dev"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func computePaneWidths(w int) (left, mid, right int) {
	// These are "outer" widths, i.e. including border.
	const (
		minLeft  = 18
		minMid   = 30
		minRight = 30
	)
	if w <= 0 {
		return minLeft, minMid, minRight
	}

	// Navigator should be compact; Details/Resources are where the user spends time.
	left = max(minLeft, w/6)
	// Cap Navigator width so it doesn't dominate on wide terminals.
	left = min(left, 32)

	mid = max(minMid, (w*50)/100)
	right = w - left - mid

	if right < minRight {
		need := minRight - right

		// Shrink mid first.
		shrinkMid := min(need, max(0, mid-minMid))
		mid -= shrinkMid
		need -= shrinkMid

		// Then shrink left if needed.
		if need > 0 {
			shrinkLeft := min(need, max(0, left-minLeft))
			left -= shrinkLeft
			need -= shrinkLeft
		}

		right = w - left - mid
	}

	// If terminal is extremely narrow, accept that right can go below minRight.
	if right < 0 {
		right = 0
	}
	return left, mid, right
}
