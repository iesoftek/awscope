package diagram

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type View string

const (
	ViewOverview View = "overview"
	ViewNetwork  View = "network"
	ViewEventing View = "eventing"
	ViewSecurity View = "security"
	ViewFull     View = "full"
)

func ParseView(s string) (View, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ViewOverview):
		return ViewOverview, nil
	case string(ViewNetwork):
		return ViewNetwork, nil
	case string(ViewEventing):
		return ViewEventing, nil
	case string(ViewSecurity):
		return ViewSecurity, nil
	case string(ViewFull):
		return ViewFull, nil
	default:
		return ViewOverview, fmt.Errorf("unsupported --view %q (supported: overview|network|eventing|security|full)", s)
	}
}

type IncludeIsolated string

const (
	IncludeIsolatedSummary IncludeIsolated = "summary"
	IncludeIsolatedFull    IncludeIsolated = "full"
	IncludeIsolatedNone    IncludeIsolated = "none"
)

func ParseIncludeIsolated(s string) (IncludeIsolated, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(IncludeIsolatedSummary):
		return IncludeIsolatedSummary, nil
	case string(IncludeIsolatedFull):
		return IncludeIsolatedFull, nil
	case string(IncludeIsolatedNone):
		return IncludeIsolatedNone, nil
	default:
		return IncludeIsolatedSummary, fmt.Errorf("unsupported --include-isolated %q (supported: summary|full|none)", s)
	}
}

type Layout string

const (
	LayoutDot  Layout = "dot"
	LayoutSFDP Layout = "sfdp"
)

func ParseLayout(s string) (Layout, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(LayoutDot):
		return LayoutDot, nil
	case string(LayoutSFDP):
		return LayoutSFDP, nil
	default:
		return LayoutDot, fmt.Errorf("unsupported --layout %q (supported: dot|sfdp)", s)
	}
}

type ProcessOptions struct {
	View            View
	MaxNodes        int
	MaxEdges        int
	ComponentLimit  int
	IncludeIsolated IncludeIsolated
	NoFold          bool
}

type viewProfile struct {
	DefaultMaxNodes int
	DefaultMaxEdges int

	Types       map[string]struct{}
	Kinds       map[string]struct{}
	FoldTypes   map[string]struct{}
	AnchorTypes map[string]struct{}
}

func profileFor(v View) viewProfile {
	switch v {
	case ViewNetwork:
		return viewProfile{
			DefaultMaxNodes: 280,
			DefaultMaxEdges: 520,
			Types: setOf(
				"ec2:vpc", "ec2:subnet", "ec2:security-group", "ec2:instance", "ec2:volume",
				"autoscaling:group",
				"elbv2:load-balancer", "elbv2:listener", "elbv2:target-group",
				"ecs:service", "lambda:function", "rds:db-instance", "rds:db-cluster", "sagemaker:endpoint", "sagemaker:notebook-instance",
				"eks:cluster", "eks:nodegroup", "elasticache:replication-group", "elasticache:cache-cluster", "elasticache:subnet-group", "opensearch:domain", "redshift:cluster", "redshift:subnet-group", "msk:cluster", "efs:file-system", "efs:mount-target", "efs:access-point",
			),
			Kinds:       setOf("member-of", "attached-to", "targets", "forwards-to", "contains", "uses"),
			FoldTypes:   setOf("elbv2:rule", "ecs:task", "ecs:task-definition", "logs:log-group"),
			AnchorTypes: defaultAnchorTypes(),
		}
	case ViewEventing:
		return viewProfile{
			DefaultMaxNodes: 260,
			DefaultMaxEdges: 500,
			Types: setOf(
				"sns:topic", "sns:subscription", "sqs:queue", "lambda:function", "dynamodb:table", "s3:bucket", "ecs:service",
				"apigateway:rest-api", "apigateway:domain-name", "cloudfront:distribution",
			),
			Kinds:       setOf("targets", "uses", "member-of", "contains"),
			FoldTypes:   map[string]struct{}{},
			AnchorTypes: defaultAnchorTypes(),
		}
	case ViewSecurity:
		return viewProfile{
			DefaultMaxNodes: 260,
			DefaultMaxEdges: 500,
			Types: setOf(
				"iam:user", "iam:group", "iam:role", "iam:policy", "iam:access-key",
				"identitycenter:permission-set", "identitycenter:assignment", "identitycenter:user", "identitycenter:group",
				"cloudtrail:trail", "config:recorder", "config:delivery-channel",
				"guardduty:detector", "securityhub:hub", "securityhub:standard-subscription", "accessanalyzer:analyzer", "wafv2:web-acl", "acm:certificate",
				"kms:key", "kms:alias", "secretsmanager:secret", "lambda:function", "ec2:instance", "rds:db-instance", "s3:bucket",
			),
			Kinds:       setOf("member-of", "attached-to", "uses", "contains"),
			FoldTypes:   setOf("iam:access-key"),
			AnchorTypes: defaultAnchorTypes(),
		}
	case ViewFull:
		return viewProfile{
			DefaultMaxNodes: 0,
			DefaultMaxEdges: 0,
			Types:           nil,
			Kinds:           nil,
			FoldTypes:       nil,
			AnchorTypes:     defaultAnchorTypes(),
		}
	default:
		return viewProfile{
			DefaultMaxNodes: 240,
			DefaultMaxEdges: 420,
			Types: setOf(
				"ec2:vpc", "ec2:subnet", "ec2:instance", "ec2:security-group", "ec2:volume",
				"ecs:cluster", "ecs:service", "autoscaling:group", "autoscaling:launch-configuration",
				"elbv2:load-balancer", "elbv2:listener", "elbv2:target-group",
				"rds:db-instance", "rds:db-cluster",
				"lambda:function", "dynamodb:table", "sqs:queue", "sns:topic", "s3:bucket",
				"sagemaker:endpoint", "sagemaker:endpoint-config", "sagemaker:model", "sagemaker:notebook-instance",
				"kms:key", "secretsmanager:secret", "iam:role", "iam:user", "iam:group",
				"identitycenter:permission-set", "identitycenter:assignment", "identitycenter:user", "identitycenter:group",
				"cloudtrail:trail", "config:recorder", "config:delivery-channel",
				"guardduty:detector", "securityhub:hub", "securityhub:standard-subscription", "accessanalyzer:analyzer", "wafv2:web-acl", "acm:certificate",
				"cloudfront:distribution", "apigateway:rest-api", "apigateway:domain-name", "ecr:repository", "eks:cluster", "eks:nodegroup", "elasticache:replication-group", "elasticache:cache-cluster", "elasticache:subnet-group", "opensearch:domain", "redshift:cluster", "redshift:subnet-group", "msk:cluster", "efs:file-system", "efs:mount-target", "efs:access-point",
				"logs:log-group",
			),
			Kinds: setOf("member-of", "attached-to", "uses", "targets", "forwards-to", "contains", "belongs-to"),
			FoldTypes: setOf(
				"logs:log-group", "sns:subscription", "elbv2:rule", "ecs:task", "ecs:task-definition", "iam:policy", "iam:access-key", "kms:alias",
			),
			AnchorTypes: defaultAnchorTypes(),
		}
	}
}

func defaultAnchorTypes() map[string]struct{} {
	return setOf(
		"elbv2:load-balancer", "elbv2:target-group", "ecs:service", "ec2:instance", "lambda:function", "rds:db-instance", "rds:db-cluster",
		"eks:cluster", "opensearch:domain", "redshift:cluster", "msk:cluster", "elasticache:replication-group",
	)
}

func setOf(items ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		out[it] = struct{}{}
	}
	return out
}

func DefaultProcessOptions(v View) ProcessOptions {
	p := profileFor(v)
	return ProcessOptions{
		View:            v,
		MaxNodes:        p.DefaultMaxNodes,
		MaxEdges:        p.DefaultMaxEdges,
		ComponentLimit:  3,
		IncludeIsolated: IncludeIsolatedSummary,
	}
}

func ProcessModel(in Model, opts ProcessOptions) Model {
	out := in
	if out.TotalNodes == 0 {
		out.TotalNodes = len(out.Nodes)
	}
	if out.TotalEdges == 0 {
		out.TotalEdges = len(out.Edges)
	}

	view := opts.View
	if out.Scope.Full {
		view = ViewFull
	}
	if view == "" {
		if out.Scope.View != "" {
			view = out.Scope.View
		} else {
			view = ViewOverview
		}
	}
	includeIsolated := opts.IncludeIsolated
	if includeIsolated == "" {
		if out.Scope.IncludeIsolated != "" {
			includeIsolated = out.Scope.IncludeIsolated
		} else {
			includeIsolated = IncludeIsolatedSummary
		}
	}
	profile := profileFor(view)

	if opts.ComponentLimit <= 0 {
		opts.ComponentLimit = 3
	}
	if opts.MaxNodes < 0 {
		opts.MaxNodes = 0
	}
	if opts.MaxEdges < 0 {
		opts.MaxEdges = 0
	}
	if opts.MaxNodes == 0 && view != ViewFull {
		opts.MaxNodes = profile.DefaultMaxNodes
	}
	if opts.MaxEdges == 0 && view != ViewFull {
		opts.MaxEdges = profile.DefaultMaxEdges
	}

	nodes := append([]Node(nil), out.Nodes...)
	edges := normalizeEdges(out.Edges)
	notes := append([]string(nil), out.Notes...)

	if view != ViewFull {
		var droppedNodes, droppedEdges int
		nodes, edges, droppedNodes, droppedEdges = filterByProfile(nodes, edges, profile)
		if droppedNodes > 0 || droppedEdges > 0 {
			notes = append(notes, fmt.Sprintf("view filtering removed %d node(s), %d edge(s)", droppedNodes, droppedEdges))
		}
		if !opts.NoFold {
			var foldedNodeCount, foldedGroupCount int
			nodes, edges, foldedNodeCount, foldedGroupCount = foldLeafNoise(nodes, edges, profile)
			if foldedNodeCount > 0 {
				notes = append(notes, fmt.Sprintf("folded %d noisy node(s) into %d summary group(s)", foldedNodeCount, foldedGroupCount))
			}
		}
		var droppedByComponentNodes, droppedByComponentEdges int
		var componentNotes []string
		nodes, edges, droppedByComponentNodes, droppedByComponentEdges, componentNotes = selectComponents(nodes, edges, opts.ComponentLimit, includeIsolated, profile)
		if droppedByComponentNodes > 0 || droppedByComponentEdges > 0 {
			notes = append(notes, fmt.Sprintf("component selection removed %d node(s), %d edge(s)", droppedByComponentNodes, droppedByComponentEdges))
		}
		notes = append(notes, componentNotes...)
	}

	var cappedNodes, cappedEdges int
	nodes, edges, cappedNodes, cappedEdges = capByScore(nodes, edges, opts.MaxNodes, opts.MaxEdges)
	if cappedNodes > 0 || cappedEdges > 0 {
		notes = append(notes, fmt.Sprintf("caps removed %d node(s), %d edge(s)", cappedNodes, cappedEdges))
	}

	sortNodes(nodes)
	sortEdges(edges)

	out.Scope.View = view
	out.Scope.Full = view == ViewFull
	out.Scope.IncludeIsolated = includeIsolated
	out.Nodes = nodes
	out.Edges = edges
	out.OmittedNodes = max(0, out.TotalNodes-len(nodes))
	out.OmittedEdges = max(0, out.TotalEdges-len(edges))
	out.Notes = notes
	return out
}

func normalizeEdges(in []Edge) []Edge {
	out := make([]Edge, 0, len(in))
	for _, e := range in {
		ec := e
		if ec.Count <= 0 {
			ec.Count = 1
		}
		out = append(out, ec)
	}
	return out
}

func filterByProfile(nodes []Node, edges []Edge, profile viewProfile) ([]Node, []Edge, int, int) {
	if profile.Types == nil || profile.Kinds == nil {
		return nodes, edges, 0, 0
	}
	nodeMap := map[string]Node{}
	for _, n := range nodes {
		if _, ok := profile.Types[n.Type]; !ok {
			continue
		}
		nodeMap[n.Key] = n
	}
	filteredNodes := make([]Node, 0, len(nodeMap))
	for _, n := range nodeMap {
		filteredNodes = append(filteredNodes, n)
	}
	filteredEdges := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := profile.Kinds[e.Kind]; !ok {
			continue
		}
		if _, ok := nodeMap[e.From]; !ok {
			continue
		}
		if _, ok := nodeMap[e.To]; !ok {
			continue
		}
		filteredEdges = append(filteredEdges, e)
	}
	return filteredNodes, foldParallelEdges(filteredEdges), max(0, len(nodes)-len(filteredNodes)), max(0, len(edges)-len(filteredEdges))
}

type adjacent struct {
	other string
	kind  string
	dir   string // "out" if node->other, "in" if other->node
}

func foldLeafNoise(nodes []Node, edges []Edge, profile viewProfile) ([]Node, []Edge, int, int) {
	if len(nodes) == 0 || len(edges) == 0 || len(profile.FoldTypes) == 0 {
		return nodes, edges, 0, 0
	}

	nodeByKey := map[string]Node{}
	for _, n := range nodes {
		nodeByKey[n.Key] = n
	}
	adj := map[string][]adjacent{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], adjacent{other: e.To, kind: e.Kind, dir: "out"})
		adj[e.To] = append(adj[e.To], adjacent{other: e.From, kind: e.Kind, dir: "in"})
	}

	type foldGroup struct {
		Key      string
		Region   string
		Service  string
		Type     string
		Parent   string
		Kind     string
		Dir      string
		Count    int
		Isolated bool
	}
	groupByKey := map[string]*foldGroup{}
	folded := map[string]struct{}{}

	for _, n := range nodes {
		if _, ok := profile.FoldTypes[n.Type]; !ok {
			continue
		}
		a := adj[n.Key]
		if len(a) > 1 {
			continue
		}

		var gk string
		g := &foldGroup{
			Region:  n.Region,
			Service: n.Service,
			Type:    n.Type,
		}
		if len(a) == 0 {
			g.Isolated = true
			gk = fmt.Sprintf("fold:isolated:%s:%s:%s", n.Region, n.Service, n.Type)
		} else {
			g.Parent = a[0].other
			g.Kind = a[0].kind
			g.Dir = a[0].dir
			gk = fmt.Sprintf("fold:leaf:%s:%s:%s:%s:%s", n.Type, g.Parent, g.Kind, g.Dir, n.Region)
		}
		ex, ok := groupByKey[gk]
		if !ok {
			g.Key = gk
			groupByKey[gk] = g
			ex = g
		}
		ex.Count++
		folded[n.Key] = struct{}{}
	}
	if len(folded) == 0 {
		return nodes, foldParallelEdges(edges), 0, 0
	}

	remaining := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if _, ok := folded[n.Key]; ok {
			continue
		}
		remaining = append(remaining, n)
	}

	for _, g := range groupByKey {
		name := fmt.Sprintf("%s x%d", friendlyTypeLabel(g.Type), g.Count)
		remaining = append(remaining, Node{
			Key:     g.Key,
			Service: g.Service,
			Type:    g.Type,
			Region:  g.Region,
			Name:    name,
			Status:  "",
			Attrs: map[string]any{
				"folded": true,
				"count":  g.Count,
			},
		})
	}

	rebuilt := make([]Edge, 0, len(edges)+len(groupByKey))
	for _, e := range edges {
		_, fromFolded := folded[e.From]
		_, toFolded := folded[e.To]
		if fromFolded || toFolded {
			continue
		}
		rebuilt = append(rebuilt, e)
	}
	for _, g := range groupByKey {
		if g.Isolated {
			continue
		}
		ec := Edge{Kind: g.Kind, Count: g.Count}
		if g.Dir == "out" {
			ec.From = g.Key
			ec.To = g.Parent
		} else {
			ec.From = g.Parent
			ec.To = g.Key
		}
		rebuilt = append(rebuilt, ec)
	}
	rebuilt = foldParallelEdges(rebuilt)
	return remaining, rebuilt, len(folded), len(groupByKey)
}

func friendlyTypeLabel(typ string) string {
	switch typ {
	case "logs:log-group":
		return "log groups"
	case "sns:subscription":
		return "subscriptions"
	case "elbv2:rule":
		return "rules"
	case "ecs:task":
		return "tasks"
	case "ecs:task-definition":
		return "task defs"
	case "iam:policy":
		return "policies"
	case "iam:access-key":
		return "access keys"
	case "kms:alias":
		return "aliases"
	default:
		return typ
	}
}

func foldParallelEdges(edges []Edge) []Edge {
	if len(edges) <= 1 {
		return edges
	}
	type key struct {
		from string
		kind string
		to   string
	}
	agg := map[key]int{}
	for _, e := range edges {
		if e.Count <= 0 {
			e.Count = 1
		}
		k := key{from: e.From, kind: e.Kind, to: e.To}
		agg[k] += e.Count
	}
	out := make([]Edge, 0, len(agg))
	for k, count := range agg {
		out = append(out, Edge{
			From:  k.from,
			To:    k.to,
			Kind:  k.kind,
			Count: count,
		})
	}
	sortEdges(out)
	return out
}

type component struct {
	ID        int
	Nodes     []string
	EdgeCount int
	MinKey    string
	HasAnchor bool
}

func selectComponents(nodes []Node, edges []Edge, limit int, includeIsolated IncludeIsolated, profile viewProfile) ([]Node, []Edge, int, int, []string) {
	if len(nodes) == 0 || limit <= 0 {
		return nodes, edges, 0, 0, nil
	}
	nodeByKey := map[string]Node{}
	for _, n := range nodes {
		nodeByKey[n.Key] = n
	}
	adj := map[string][]string{}
	for _, n := range nodes {
		adj[n.Key] = nil
	}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	seen := map[string]struct{}{}
	comps := make([]component, 0)
	compByNode := map[string]int{}
	for _, n := range nodes {
		if _, ok := seen[n.Key]; ok {
			continue
		}
		stack := []string{n.Key}
		seen[n.Key] = struct{}{}
		c := component{ID: len(comps), MinKey: n.Key}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c.Nodes = append(c.Nodes, k)
			compByNode[k] = c.ID
			if k < c.MinKey {
				c.MinKey = k
			}
			if _, ok := profile.AnchorTypes[nodeByKey[k].Type]; ok {
				c.HasAnchor = true
			}
			for _, nb := range adj[k] {
				if _, ok := seen[nb]; ok {
					continue
				}
				seen[nb] = struct{}{}
				stack = append(stack, nb)
			}
		}
		comps = append(comps, c)
	}
	for i := range comps {
		comps[i].ID = i
	}
	for _, e := range edges {
		cid, ok := compByNode[e.From]
		if !ok {
			continue
		}
		if compByNode[e.To] != cid {
			continue
		}
		comps[cid].EdgeCount += e.Count
	}

	ranked := append([]component(nil), comps...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].EdgeCount != ranked[j].EdgeCount {
			return ranked[i].EdgeCount > ranked[j].EdgeCount
		}
		if len(ranked[i].Nodes) != len(ranked[j].Nodes) {
			return len(ranked[i].Nodes) > len(ranked[j].Nodes)
		}
		return ranked[i].MinKey < ranked[j].MinKey
	})

	keepComps := map[int]struct{}{}
	for i := 0; i < len(ranked) && i < limit; i++ {
		keepComps[ranked[i].ID] = struct{}{}
	}
	for _, c := range comps {
		if c.HasAnchor {
			keepComps[c.ID] = struct{}{}
		}
	}

	omitCompByType := map[string]int{}
	isolatedByType := map[string]int{}

	keepNodes := map[string]Node{}
	for _, c := range comps {
		isolatedComp := len(c.Nodes) == 1 && c.EdgeCount == 0
		_, keep := keepComps[c.ID]
		if isolatedComp {
			switch includeIsolated {
			case IncludeIsolatedFull:
				keep = true
			case IncludeIsolatedNone:
				keep = false
			default:
				keep = false
			}
		}
		if keep {
			for _, k := range c.Nodes {
				keepNodes[k] = nodeByKey[k]
			}
			continue
		}
		for _, k := range c.Nodes {
			n := nodeByKey[k]
			key := n.Service + "|" + n.Type
			if isolatedComp {
				isolatedByType[key]++
			} else {
				omitCompByType[key]++
			}
		}
	}

	kept := make([]Node, 0, len(keepNodes))
	for _, n := range keepNodes {
		kept = append(kept, n)
	}

	notes := make([]string, 0, 2)
	if len(omitCompByType) > 0 {
		notes = append(notes, "omitted lower-priority disconnected components")
	}

	if includeIsolated == IncludeIsolatedSummary && len(isolatedByType) > 0 {
		keys := make([]string, 0, len(isolatedByType))
		for k := range isolatedByType {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts := strings.SplitN(k, "|", 2)
			service, typ := parts[0], parts[1]
			count := isolatedByType[k]
			kept = append(kept, Node{
				Key:     fmt.Sprintf("summary:isolated:%s:%s", service, typ),
				Service: service,
				Type:    typ,
				Region:  "summary",
				Name:    fmt.Sprintf("isolated %s x%d", friendlyTypeLabel(typ), count),
				Attrs: map[string]any{
					"summary": true,
					"count":   count,
				},
			})
		}
		notes = append(notes, fmt.Sprintf("summarized %d isolated node group(s)", len(isolatedByType)))
	}

	keepSet := map[string]struct{}{}
	for _, n := range kept {
		keepSet[n.Key] = struct{}{}
	}
	keptEdges := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := keepSet[e.From]; !ok {
			continue
		}
		if _, ok := keepSet[e.To]; !ok {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	keptEdges = foldParallelEdges(keptEdges)
	return kept, keptEdges, max(0, len(nodes)-len(kept)), max(0, len(edges)-len(keptEdges)), notes
}

func capByScore(nodes []Node, edges []Edge, maxNodes, maxEdges int) ([]Node, []Edge, int, int) {
	if len(nodes) == 0 {
		return nodes, edges, 0, 0
	}
	nodeByKey := map[string]Node{}
	for _, n := range nodes {
		nodeByKey[n.Key] = n
	}
	deg := degreeMap(edges)
	nodeScore := map[string]float64{}
	for _, n := range nodes {
		nodeScore[n.Key] = scoreNode(n, deg[n.Key])
	}

	droppedNodes := 0
	if maxNodes > 0 && len(nodes) > maxNodes {
		sort.SliceStable(nodes, func(i, j int) bool {
			si := nodeScore[nodes[i].Key]
			sj := nodeScore[nodes[j].Key]
			if si != sj {
				return si > sj
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
		droppedNodes = len(nodes) - maxNodes
		nodes = nodes[:maxNodes]
	}
	sortNodes(nodes)

	keepSet := map[string]struct{}{}
	for _, n := range nodes {
		keepSet[n.Key] = struct{}{}
	}
	keptEdges := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := keepSet[e.From]; !ok {
			continue
		}
		if _, ok := keepSet[e.To]; !ok {
			continue
		}
		keptEdges = append(keptEdges, e)
	}
	droppedEdges := len(edges) - len(keptEdges)

	if maxEdges > 0 && len(keptEdges) > maxEdges {
		sort.SliceStable(keptEdges, func(i, j int) bool {
			si := nodeScore[keptEdges[i].From] + nodeScore[keptEdges[i].To] + edgeKindWeight(keptEdges[i].Kind)
			sj := nodeScore[keptEdges[j].From] + nodeScore[keptEdges[j].To] + edgeKindWeight(keptEdges[j].Kind)
			if si != sj {
				return si > sj
			}
			if keptEdges[i].Kind != keptEdges[j].Kind {
				return keptEdges[i].Kind < keptEdges[j].Kind
			}
			if keptEdges[i].From != keptEdges[j].From {
				return keptEdges[i].From < keptEdges[j].From
			}
			return keptEdges[i].To < keptEdges[j].To
		})
		droppedEdges += len(keptEdges) - maxEdges
		keptEdges = keptEdges[:maxEdges]
	}
	keptEdges = foldParallelEdges(keptEdges)
	sortEdges(keptEdges)

	return nodes, keptEdges, max(0, droppedNodes), max(0, droppedEdges)
}

func edgeKindWeight(kind string) float64 {
	switch kind {
	case "targets", "forwards-to":
		return 2
	case "attached-to":
		return 1.5
	case "member-of":
		return 1.2
	default:
		return 1
	}
}

func scoreNode(n Node, degree int) float64 {
	score := 0.0
	if isAnchorType(n.Type) {
		score += 5
	}
	if isWorkloadType(n.Type) {
		score += 3
	}
	if isEntrypointType(n.Type) {
		score += 2
	}
	if isCriticalStatus(n.Status) {
		score += 2
	}
	score += math.Log2(1 + float64(max(0, degree)))
	cost := nodeCost(n)
	if cost > 0 {
		score += math.Min(2, math.Log10(1+cost))
	}
	return score
}

func nodeCost(n Node) float64 {
	if n.CostUSD != nil {
		return *n.CostUSD
	}
	if n.Attrs == nil {
		return 0
	}
	for _, k := range []string{"est_monthly_usd", "estMonthlyUSD"} {
		if v, ok := n.Attrs[k]; ok {
			switch tv := v.(type) {
			case float64:
				return tv
			case float32:
				return float64(tv)
			case int:
				return float64(tv)
			case int64:
				return float64(tv)
			case jsonNumber:
				f, _ := strconv.ParseFloat(string(tv), 64)
				return f
			case string:
				f, _ := strconv.ParseFloat(strings.TrimSpace(tv), 64)
				return f
			}
		}
	}
	return 0
}

type jsonNumber string

func isAnchorType(typ string) bool {
	switch typ {
	case "ec2:vpc", "ec2:subnet", "elbv2:load-balancer", "elbv2:target-group", "ecs:service", "ec2:instance", "lambda:function", "rds:db-instance", "rds:db-cluster":
		return true
	default:
		return false
	}
}

func isWorkloadType(typ string) bool {
	switch typ {
	case "ecs:service", "ec2:instance", "lambda:function", "rds:db-instance", "rds:db-cluster":
		return true
	default:
		return false
	}
}

func isEntrypointType(typ string) bool {
	switch typ {
	case "elbv2:load-balancer", "elbv2:listener", "sns:topic":
		return true
	default:
		return false
	}
}

func isCriticalStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return false
	}
	for _, token := range []string{"fail", "error", "degrad", "impaired", "alarm", "stopped"} {
		if strings.Contains(s, token) {
			return true
		}
	}
	return false
}
