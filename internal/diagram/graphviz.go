package diagram

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type GraphvizRenderer struct{}

func (GraphvizRenderer) Format() string { return "graphviz" }

func (GraphvizRenderer) Render(model Model) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("digraph awscope {\n")
	if model.Scope.Full {
		b.WriteString("  graph [rankdir=LR, compound=true, newrank=true, splines=true, fontname=\"Helvetica\"];\n")
	} else {
		b.WriteString("  graph [rankdir=LR, compound=true, newrank=true, concentrate=true, splines=ortho, nodesep=0.35, ranksep=0.65, pad=0.2, pack=true, packmode=clust, fontname=\"Helvetica\"];\n")
	}
	b.WriteString("  node [shape=box, style=\"rounded\", fontname=\"Helvetica\", fontsize=10];\n")
	b.WriteString("  edge [fontname=\"Helvetica\", fontsize=9, color=\"#555555\"];\n")

	if len(model.Nodes) == 0 {
		b.WriteString("  empty [label=\"No resources found for selected scope\", shape=note];\n")
		b.WriteString("}\n")
		return b.Bytes(), nil
	}

	idByKey := make(map[string]string, len(model.Nodes))
	nodeByKey := make(map[string]Node, len(model.Nodes))
	for i, n := range model.Nodes {
		idByKey[n.Key] = fmt.Sprintf("n%d", i)
		nodeByKey[n.Key] = n
	}

	// Build membership hints from edges.
	vpcFor := map[string]string{}    // node key -> vpc key
	subnetFor := map[string]string{} // node key -> subnet key
	for _, e := range model.Edges {
		from, okF := nodeByKey[e.From]
		to, okT := nodeByKey[e.To]
		if !okF || !okT {
			continue
		}
		if e.Kind != "member-of" {
			continue
		}
		if to.Type == "ec2:vpc" {
			vpcFor[from.Key] = to.Key
		}
		if to.Type == "ec2:subnet" {
			subnetFor[from.Key] = to.Key
		}
	}
	for nodeKey, subnetKey := range subnetFor {
		if _, ok := vpcFor[nodeKey]; ok {
			continue
		}
		if vpcKey, ok := vpcFor[subnetKey]; ok {
			vpcFor[nodeKey] = vpcKey
		}
	}

	regionNodes := []Node{}
	globalNodes := []Node{}
	for _, n := range model.Nodes {
		if n.Region == "global" {
			globalNodes = append(globalNodes, n)
		} else {
			regionNodes = append(regionNodes, n)
		}
	}

	// Region cluster.
	b.WriteString(fmt.Sprintf("  subgraph cluster_region {\n    label=%s;\n", dotString("Region "+model.Scope.Region)))

	// VPC buckets + service buckets for region nodes.
	vpcBuckets := map[string][]Node{}
	serviceBuckets := map[string][]Node{}
	for _, n := range regionNodes {
		if n.Type == "ec2:vpc" {
			vpcBuckets[n.Key] = append(vpcBuckets[n.Key], n)
			continue
		}
		if vk, ok := vpcFor[n.Key]; ok && strings.TrimSpace(vk) != "" {
			vpcBuckets[vk] = append(vpcBuckets[vk], n)
			continue
		}
		serviceBuckets[n.Service] = append(serviceBuckets[n.Service], n)
	}

	vpcKeys := sortedKeys(vpcBuckets)
	for i, vpcKey := range vpcKeys {
		vpcID := fmt.Sprintf("cluster_vpc_%d", i)
		vpcName := "VPC"
		if vpc, ok := nodeByKey[vpcKey]; ok {
			vpcName = "VPC " + vpc.Name
		}
		b.WriteString(fmt.Sprintf("    subgraph %s {\n      label=%s;\n", vpcID, dotString(vpcName)))

		// subnet buckets inside VPC
		subnetBuckets := map[string][]Node{}
		var vpcLevel []Node
		for _, n := range vpcBuckets[vpcKey] {
			if n.Type == "ec2:vpc" {
				vpcLevel = append(vpcLevel, n)
				continue
			}
			if sk, ok := subnetFor[n.Key]; ok && strings.TrimSpace(sk) != "" {
				subnetBuckets[sk] = append(subnetBuckets[sk], n)
				continue
			}
			if n.Type == "ec2:subnet" {
				subnetBuckets[n.Key] = append(subnetBuckets[n.Key], n)
				continue
			}
			vpcLevel = append(vpcLevel, n)
		}

		for _, n := range vpcLevel {
			b.WriteString(renderDotNode(idByKey[n.Key], n))
		}
		for j, sk := range sortedKeys(subnetBuckets) {
			label := "Subnet"
			if sn, ok := nodeByKey[sk]; ok {
				label = "Subnet " + sn.Name
			}
			sid := fmt.Sprintf("cluster_subnet_%d_%d", i, j)
			b.WriteString(fmt.Sprintf("      subgraph %s {\n        label=%s;\n", sid, dotString(label)))
			for _, n := range subnetBuckets[sk] {
				b.WriteString(renderDotNode(idByKey[n.Key], n))
			}
			b.WriteString("      }\n")
		}

		b.WriteString("    }\n")
	}

	for i, svc := range sortedKeys(serviceBuckets) {
		b.WriteString(fmt.Sprintf("    subgraph cluster_service_%d {\n      label=%s;\n", i, dotString(strings.ToUpper(svc))))
		for _, n := range serviceBuckets[svc] {
			b.WriteString(renderDotNode(idByKey[n.Key], n))
		}
		b.WriteString("    }\n")
	}
	b.WriteString("  }\n")

	if len(globalNodes) > 0 {
		b.WriteString("  subgraph cluster_global {\n    label=\"Global\";\n")
		globalBuckets := map[string][]Node{}
		for _, n := range globalNodes {
			globalBuckets[n.Service] = append(globalBuckets[n.Service], n)
		}
		for i, svc := range sortedKeys(globalBuckets) {
			b.WriteString(fmt.Sprintf("    subgraph cluster_global_service_%d {\n      label=%s;\n", i, dotString(strings.ToUpper(svc))))
			for _, n := range globalBuckets[svc] {
				b.WriteString(renderDotNode(idByKey[n.Key], n))
			}
			b.WriteString("    }\n")
		}
		b.WriteString("  }\n")
	}

	for _, e := range model.Edges {
		fromID, ok1 := idByKey[e.From]
		toID, ok2 := idByKey[e.To]
		if !ok1 || !ok2 {
			continue
		}
		attrs := make([]string, 0, 3)
		if model.Scope.Full {
			lbl := e.Kind
			if e.Count > 1 {
				lbl = fmt.Sprintf("%s x%d", e.Kind, e.Count)
			}
			attrs = append(attrs, fmt.Sprintf("label=%s", dotString(lbl)))
		} else {
			if e.Count > 1 || e.Kind == "uses" || e.Kind == "targets" || e.Kind == "forwards-to" {
				lbl := e.Kind
				if e.Count > 1 {
					lbl = fmt.Sprintf("%s x%d", e.Kind, e.Count)
				}
				attrs = append(attrs, fmt.Sprintf("label=%s", dotString(lbl)))
			}
			if e.Kind == "uses" {
				attrs = append(attrs, "constraint=false")
			}
		}
		if len(attrs) == 0 {
			b.WriteString(fmt.Sprintf("  %s -> %s;\n", fromID, toID))
			continue
		}
		b.WriteString(fmt.Sprintf("  %s -> %s [%s];\n", fromID, toID, strings.Join(attrs, ", ")))
	}

	if len(model.Notes) > 0 {
		for i, note := range model.Notes {
			b.WriteString(fmt.Sprintf("  note_%d [shape=note, label=%s];\n", i, dotString(note)))
		}
	}
	if model.OmittedNodes > 0 || model.OmittedEdges > 0 {
		b.WriteString(fmt.Sprintf("  legend [shape=note, label=%s];\n", dotString(
			fmt.Sprintf("Condensed view: omitted %d nodes, %d edges", model.OmittedNodes, model.OmittedEdges),
		)))
	}

	b.WriteString("}\n")
	return b.Bytes(), nil
}

func renderDotNode(id string, n Node) string {
	label := n.Name
	if strings.TrimSpace(n.Type) != "" {
		label += "\\n" + n.Type
	}
	if strings.TrimSpace(n.Status) != "" {
		label += "\\n[" + n.Status + "]"
	}
	return fmt.Sprintf("      %s [label=%s];\n", id, dotString(label))
}

func dotString(s string) string {
	// Keep DOT robust against arbitrary resource names/attrs from AWS APIs.
	s = strings.ToValidUTF8(s, "?")
	return strconv.QuoteToASCII(s)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
