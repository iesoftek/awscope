package app

import (
	"fmt"
	"strings"

	"awscope/internal/actions"
	"awscope/internal/graph"
	"awscope/internal/store"
)

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
