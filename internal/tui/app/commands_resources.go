package app

import (
	"awscope/internal/actions"
	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/core"
	"awscope/internal/graph"
	"awscope/internal/store"
	"awscope/internal/tui/components/graphlens"
	"awscope/internal/tui/widgets/table"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

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

func (m *model) tryRunActionCmd() tea.Cmd {
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

	if a, ok := actionsRegistry.Get(actionID); ok {
		if ea, ok := a.(actions.EmbeddedTUIAction); ok && ea.PreferEmbeddedTUI() {
			target := strings.TrimSpace(sel.DisplayName)
			if target == "" {
				target = strings.TrimSpace(sel.PrimaryID)
			}
			if target == "" {
				target = string(key)
			}
			return m.startActionStreamCmd(actionID, key, profile, target)
		}
		if _, isTerminal := a.(actions.TerminalAction); isTerminal {
			execCmd := &tuiActionExecCommand{
				ctx:      m.ctx,
				st:       m.st,
				actionID: actionID,
				key:      key,
				profile:  profile,
			}
			return tea.Exec(execCmd, func(err error) tea.Msg {
				if err != nil {
					return actionDoneMsg{err: err}
				}
				return actionDoneMsg{line: execCmd.doneLine}
			})
		}
	}

	return func() tea.Msg {
		res, err := core.RunAction(m.ctx, m.st, actionID, key, profile)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return actionDoneMsg{line: fmt.Sprintf("action %s %s (%s)", res.ActionID, res.Status, res.ActionRunID)}
	}
}

type tuiActionExecCommand struct {
	ctx      context.Context
	st       *store.Store
	actionID string
	key      graph.ResourceKey
	profile  string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	doneLine string
}

func (c *tuiActionExecCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *tuiActionExecCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *tuiActionExecCommand) SetStderr(w io.Writer) { c.stderr = w }

func (c *tuiActionExecCommand) Run() error {
	res, err := core.RunAction(c.ctx, c.st, c.actionID, c.key, c.profile, core.RunActionOptions{
		Stdin:  c.stdin,
		Stdout: c.stdout,
		Stderr: c.stderr,
	})
	if err != nil {
		return err
	}
	c.doneLine = fmt.Sprintf("action %s %s (%s)", res.ActionID, res.Status, res.ActionRunID)
	return nil
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
