package app

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	"awscope/internal/aws"
	"awscope/internal/catalog"
	"awscope/internal/providers/registry"
	"awscope/internal/store"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/components/navigator"
	"awscope/internal/tui/icons"
	"awscope/internal/tui/theme"
	"awscope/internal/tui/widgets/table"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mistakenelf/teacup/statusbar"
)

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
	initialPreset := catalog.ResourceTablePreset(selected, selectedType)

	nav := navigator.New(serviceIDs, fallbackTypesForService)
	nav.SetSelection(selected, selectedType)
	if selected != "" {
		nav.ToggleExpandedService(selected)
	}

	resTable := table.New(table.WithColumns(buildResourceColumns(80, initialPreset, false)), table.WithRows(nil))
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

	actionStreamViewport := viewport.New(80, 18)
	actionStreamInput := textinput.New()
	actionStreamInput.Placeholder = "type response and press enter (y/N)"
	actionStreamInput.CharLimit = 256
	actionStreamInput.Width = 48

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
		actionStreamViewport:      actionStreamViewport,
		actionStreamInput:         actionStreamInput,
		actionStreamFollowTail:    true,
		actionStreamWrap:          true,
		actionStreamColorize:      true,
		actionStreamMaxBytes:      defaultActionStreamMaxBytes,
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

	modeStr := strings.TrimSpace(m.iconMode)
	if modeStr == "" {
		modeStr = strings.TrimSpace(os.Getenv("AWSCOPE_ICONS"))
	}
	set := icons.New(icons.ParseMode(modeStr))
	m.icons = set
	m.nav.SetIcons(set)
	m.lens.SetIcons(set)

	profileExplicit := strings.TrimSpace(opts.Profile) != "" || strings.TrimSpace(os.Getenv("AWS_PROFILE")) != ""
	m.profileName = effectiveProfile(opts.Profile)

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
