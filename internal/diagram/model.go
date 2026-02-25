package diagram

import (
	"sort"
	"strings"

	"awscope/internal/graph"
	"awscope/internal/store"
)

type Scope struct {
	AccountID           string
	Region              string
	IncludeGlobalLinked bool
	Full                bool
	View                View
	IncludeIsolated     IncludeIsolated
	Layout              string
}

type Node struct {
	Key     string
	Service string
	Type    string
	Region  string
	Name    string
	Status  string
	Attrs   map[string]any
	CostUSD *float64
}

type Edge struct {
	From string
	To   string
	Kind string
	// Count folds duplicate/parallel edges to reduce graph noise.
	Count int
}

type Model struct {
	Scope Scope
	Nodes []Node
	Edges []Edge

	TotalNodes   int
	TotalEdges   int
	OmittedNodes int
	OmittedEdges int
	Notes        []string
}

type CondenseOptions struct {
	MaxNodes int
	MaxEdges int
}

func BuildModel(scope Scope, resources []store.ResourceSummary, rels []graph.RelationshipEdge) Model {
	nodes := make([]Node, 0, len(resources))
	nodeByKey := make(map[string]Node, len(resources))
	for _, r := range resources {
		n := Node{
			Key:     string(r.Key),
			Service: strings.TrimSpace(r.Service),
			Type:    strings.TrimSpace(r.Type),
			Region:  strings.TrimSpace(r.Region),
			Name:    bestName(r),
			Status:  deriveStatus(r.Attributes),
			Attrs:   r.Attributes,
			CostUSD: r.EstMonthlyUSD,
		}
		nodes = append(nodes, n)
		nodeByKey[n.Key] = n
	}

	edges := make([]Edge, 0, len(rels))
	seen := map[string]struct{}{}
	for _, e := range rels {
		f := strings.TrimSpace(string(e.From))
		t := strings.TrimSpace(string(e.To))
		if f == "" || t == "" {
			continue
		}
		if _, ok := nodeByKey[f]; !ok {
			continue
		}
		if _, ok := nodeByKey[t]; !ok {
			continue
		}
		k := f + "|" + strings.TrimSpace(e.Kind) + "|" + t
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		edges = append(edges, Edge{From: f, To: t, Kind: strings.TrimSpace(e.Kind), Count: 1})
	}

	sortNodes(nodes)
	sortEdges(edges)
	return Model{
		Scope:      scope,
		Nodes:      nodes,
		Edges:      edges,
		TotalNodes: len(nodes),
		TotalEdges: len(edges),
	}
}

func CondenseDefault(m Model, opts CondenseOptions) Model {
	p := DefaultProcessOptions(ViewOverview)
	p.MaxNodes = opts.MaxNodes
	p.MaxEdges = opts.MaxEdges
	return ProcessModel(m, p)
}

func bestName(r store.ResourceSummary) string {
	n := strings.TrimSpace(r.DisplayName)
	if n == "" && strings.TrimSpace(r.Tags["Name"]) != "" {
		n = strings.TrimSpace(r.Tags["Name"])
	}
	if n == "" {
		n = strings.TrimSpace(r.PrimaryID)
	}
	if n == "" {
		n = string(r.Key)
	}
	return n
}

func deriveStatus(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	for _, k := range []string{"state", "status", "keyState", "lastStatus", "desiredStatus", "console_access"} {
		if v, ok := attrs[k].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func degreeMap(edges []Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.From]++
		out[e.To]++
	}
	return out
}

func sortNodes(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Region != nodes[j].Region {
			return nodes[i].Region < nodes[j].Region
		}
		if nodes[i].Service != nodes[j].Service {
			return nodes[i].Service < nodes[j].Service
		}
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type < nodes[j].Type
		}
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		return nodes[i].Key < nodes[j].Key
	})
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Count < edges[j].Count
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
