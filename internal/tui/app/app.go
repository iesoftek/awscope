package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"awscope/internal/aws"
	"awscope/internal/catalog"
	"awscope/internal/core"
	"awscope/internal/graph"
	"awscope/internal/store"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/components/navigator"
	"awscope/internal/tui/icons"
	"awscope/internal/tui/theme"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
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
	focusActionStream
	focusActionStreamInput
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

type ecsDrillLevel int

const (
	ecsDrillNone ecsDrillLevel = iota
	ecsDrillServices
	ecsDrillTasks
)

type ecsDrillState struct {
	Level       ecsDrillLevel
	ClusterKey  graph.ResourceKey
	ClusterArn  string
	ClusterName string
	ServiceKey  graph.ResourceKey
	ServiceArn  string
	ServiceName string
	Region      string
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
	ecsDrill        ecsDrillState
}

type model struct {
	ctx     context.Context
	st      *store.Store
	scanner scanRunner

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
	ecsDrill        ecsDrillState

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

	actionStreamOpen        bool
	actionStreamActionID    string
	actionStreamTarget      string
	actionStreamRunning     bool
	actionStreamStopping    bool
	actionStreamCloseOnStop bool
	actionStreamFollowTail  bool
	actionStreamWrap        bool
	actionStreamColorize    bool
	actionStreamErr         error
	actionStreamDoneLine    string
	actionStreamViewport    viewport.Model
	actionStreamInput       textinput.Model
	actionStreamSeq         int
	actionStreamCh          <-chan tea.Msg
	actionStreamCancel      context.CancelFunc
	actionStreamInputWriter io.WriteCloser
	actionStreamLog         string
	actionStreamWrappedLog  string
	actionStreamRenderWidth int
	actionStreamMaxBytes    int

	refreshActive            bool
	refreshSeq               int
	refreshScope             refreshScope
	refreshStartedAt         time.Time
	refreshCancel            context.CancelFunc
	refreshCh                <-chan tea.Msg
	refreshCurrent           core.ScanProgressEvent
	refreshTotal             int
	refreshCompleted         int
	refreshResSoFar          int
	refreshEdgesSoFar        int
	refreshStepFailures      int
	refreshActiveStepKeys    map[string]string
	refreshFailureStepKeys   map[string]bool
	refreshBusyServiceCounts map[string]int
	refreshBusyServices      map[string]bool
	refreshSpinner           spinner.Model
	refreshProgress          progress.Model

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

const (
	frameTopBarLines        = 4
	frameStatusReserved     = 2
	frameBodyStatusGapLines = 1
)

func (m model) frameBodyHeightBudget() int {
	if m.height <= 0 {
		return 0
	}
	reserved := frameTopBarLines + frameStatusReserved + frameBodyStatusGapLines
	return max(6, m.height-reserved)
}

func (m model) paneInnerHeightBudget() int {
	return max(4, m.frameBodyHeightBudget()-3) // pane header + top/bottom border
}

func (m model) filters() filterSnapshot {
	return filterSnapshot{
		Resource:   strings.TrimSpace(m.filter.Value()),
		Regions:    m.regionsLabel(),
		RegionFind: strings.TrimSpace(m.regionFilter.Value()),
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
	w := 80
	if m.paneMidW > 0 {
		w = m.paneMidW - 4
	}
	m.applyResourceTableLayout(w, m.resourceSummaries)
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

func (m *model) applyResourceTableLayout(width int, summaries []store.ResourceSummary) {
	if width <= 0 {
		width = 80
	}
	preset := catalog.ResourceTablePreset(m.selectedService, m.selectedType)
	m.resources.SetColumns(buildResourceColumns(width, preset, m.pricingMode))
	m.resources.SetRows(makeResourceRows(summaries, preset, m.pricingMode, m.styles, m.icons))
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
				m.stopRefreshScan()
				return m, tea.Quit
			}
			return m, nil
		}
		if m.actionStreamOpen {
			if cmd, handled := m.handleActionStreamKey(msg); handled {
				return m, cmd
			}
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
			m.stopRefreshScan()
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
				m.applyResourceTableLayout(w, m.resourceSummaries)

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
				m.clearECSDrillIfRegionOutOfScope()
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
				m.clearECSDrillIfRegionOutOfScope()
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
				m.clearECSDrillIfRegionOutOfScope()
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
			if m.focus == focusResources && !m.graphMode && m.selectedService == "ecs" {
				if cmd := m.drillECSUpCmd(); cmd != nil {
					cmds = append(cmds, cmd)
					break
				}
			}
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
			if m.focus == focusResources && !m.graphMode && m.selectedService == "ecs" {
				if cmd := m.drillECSDownCmd(); cmd != nil {
					cmds = append(cmds, cmd)
					break
				}
			}
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
			if m.offline {
				m.loading = true
				m.err = nil
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
				break
			}
			if m.refreshActive {
				m.statusLine = "refresh already running"
				break
			}
			if cmd := m.startRefreshForCurrentScopeCmd(); cmd != nil {
				cmds = append(cmds, cmd)
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
			w := 80
			if m.paneMidW > 0 {
				w = m.paneMidW - 4
			}
			m.applyResourceTableLayout(w, msg.summaries)
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

	case actionStreamChunkMsg, actionStreamDoneMsg, actionStreamClosedMsg:
		if cmd := m.handleActionStreamMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case refreshProgressMsg, refreshDoneMsg, refreshClosedMsg:
		if cmd := m.handleRefreshMessage(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width
		left, mid, right := computePaneWidths(w)
		m.paneLeftW, m.paneMidW, m.paneRightW = left, mid, right

		bodyH := m.frameBodyHeightBudget()
		paneInnerH := m.paneInnerHeightBudget()
		m.nav.SetSize(max(10, left-4), paneInnerH)

		m.resources.SetWidth(mid - 4)
		m.regions.SetSize(mid, min(12, max(6, bodyH-2)))
		m.actions.SetSize(mid, min(10, max(6, bodyH-2)))
		m.related.SetSize(max(10, right-4), paneInnerH)
		m.raw.Width = max(10, right-4)
		m.raw.Height = paneInnerH
		m.help.Width = w
		m.statusbar, _ = m.statusbar.Update(msg)

		m.applyResourceTableLayout(mid-4, m.resourceSummaries)
		m.regionTable.SetWidth(mid - 4)
		m.regionTable.SetHeight(max(6, bodyH-4))
		m.regionTable.SetColumns(buildRegionColumns(mid - 4))
		m.regionFilter.Width = min(30, max(10, mid-16))
		m.resizeAuditWidgets()
		m.resizeActionStreamWidgets()
		// Graph Lens side lists are sized dynamically in View(), but we still update height here.
		lensH := max(4, paneInnerH-1)
		sideW := max(12, (mid-10)/3)
		m.lens.SetSize(sideW, sideW, lensH)

		// Match page size to viewport height as a reasonable default for paging.
		perPage := max(10, paneInnerH-2)
		if perPage != m.pager.PerPage {
			m.pager.PerPage = perPage
			m.pager.Page = 0
			m.loading = true
			m.err = nil
			cmds = append(cmds, m.loadResourcesCmd())
		}

	case spinner.TickMsg:
		if m.refreshActive {
			var cmd tea.Cmd
			m.refreshSpinner, cmd = m.refreshSpinner.Update(msg)
			if glyph := strings.TrimSpace(m.refreshSpinner.View()); glyph != "" {
				m.nav.SetBusyServices(m.refreshBusyServices, glyph)
			}
			cmds = append(cmds, cmd)
		}
	}

	m.syncResourceHeight()

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
	if m.focus == focusActionStreamInput {
		m.actionStreamInput.Focus()
	} else {
		m.actionStreamInput.Blur()
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
					m.applyECSSelection(m.selectedService, m.selectedType, false)
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
					m.applyECSSelection(m.selectedService, m.selectedType, true)
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
	case focusActionStream:
		var cmd tea.Cmd
		m.actionStreamViewport, cmd = m.actionStreamViewport.Update(msg)
		cmds = append(cmds, cmd)
	case focusActionStreamInput:
		var cmd tea.Cmd
		m.actionStreamInput, cmd = m.actionStreamInput.Update(msg)
		cmds = append(cmds, cmd)
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
		if m.offline {
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
		if m.refreshActive {
			m.statusLine = "refresh already running"
			return nil, true
		}
		return m.startRefreshForCurrentScopeCmd(), true
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
