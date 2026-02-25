package diagram

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

type MermaidRenderer struct{}

func (MermaidRenderer) Format() string { return "mermaid" }

func (MermaidRenderer) Render(model Model) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("flowchart LR\n")
	if len(model.Nodes) > 180 {
		b.WriteString("%% warning: large graph; readability may be limited in Mermaid\n")
	}
	if model.OmittedNodes > 0 || model.OmittedEdges > 0 {
		b.WriteString(fmt.Sprintf("%% truncated: omitted %d nodes, %d edges\n", model.OmittedNodes, model.OmittedEdges))
	}
	if len(model.Nodes) == 0 {
		b.WriteString("  empty[\"No resources found for selected scope\"]\n")
		return b.Bytes(), nil
	}

	idByKey := make(map[string]string, len(model.Nodes))
	nodesByRegionService := map[string]map[string][]Node{}
	for i, n := range model.Nodes {
		id := fmt.Sprintf("n%d", i)
		idByKey[n.Key] = id
		if _, ok := nodesByRegionService[n.Region]; !ok {
			nodesByRegionService[n.Region] = map[string][]Node{}
		}
		nodesByRegionService[n.Region][n.Service] = append(nodesByRegionService[n.Region][n.Service], n)
	}

	// Region groups.
	regions := sortedMapKeys(nodesByRegionService)
	for _, r := range regions {
		regionLabel := r
		if r == "global" {
			regionLabel = "global"
		}
		b.WriteString(fmt.Sprintf("  subgraph %s[\"%s\"]\n", mermaidID("region_"+r), escMermaid("Region "+regionLabel)))
		services := sortedMapKeys(nodesByRegionService[r])
		for _, svc := range services {
			b.WriteString(fmt.Sprintf("    subgraph %s[\"%s\"]\n", mermaidID("svc_"+r+"_"+svc), escMermaid(strings.ToUpper(svc))))
			for _, n := range nodesByRegionService[r][svc] {
				label := n.Name + "<br/>" + n.Type
				if strings.TrimSpace(n.Status) != "" {
					label += "<br/>[" + n.Status + "]"
				}
				b.WriteString(fmt.Sprintf("      %s[\"%s\"]\n", idByKey[n.Key], escMermaid(label)))
			}
			b.WriteString("    end\n")
		}
		b.WriteString("  end\n")
	}

	// Class definitions by service family.
	classDefs := map[string]string{
		"ec2":            "fill:#dbeafe,stroke:#3b82f6,stroke-width:1px",
		"ecs":            "fill:#dcfce7,stroke:#16a34a,stroke-width:1px",
		"elbv2":          "fill:#fef3c7,stroke:#d97706,stroke-width:1px",
		"iam":            "fill:#ede9fe,stroke:#7c3aed,stroke-width:1px",
		"kms":            "fill:#f5f3ff,stroke:#6d28d9,stroke-width:1px",
		"lambda":         "fill:#fff7ed,stroke:#ea580c,stroke-width:1px",
		"rds":            "fill:#e0f2fe,stroke:#0284c7,stroke-width:1px",
		"s3":             "fill:#ecfccb,stroke:#65a30d,stroke-width:1px",
		"logs":           "fill:#f1f5f9,stroke:#475569,stroke-width:1px",
		"dynamodb":       "fill:#cffafe,stroke:#0891b2,stroke-width:1px",
		"sqs":            "fill:#ffedd5,stroke:#c2410c,stroke-width:1px",
		"sns":            "fill:#fee2e2,stroke:#dc2626,stroke-width:1px",
		"secretsmanager": "fill:#fce7f3,stroke:#be185d,stroke-width:1px",
		"autoscaling":    "fill:#eef2ff,stroke:#4f46e5,stroke-width:1px",
		"sagemaker":      "fill:#ecfeff,stroke:#0e7490,stroke-width:1px",
		"identitycenter": "fill:#f0fdf4,stroke:#16a34a,stroke-width:1px",
		"cloudtrail":     "fill:#fef9c3,stroke:#ca8a04,stroke-width:1px",
		"config":         "fill:#ecfccb,stroke:#65a30d,stroke-width:1px",
		"guardduty":      "fill:#fee2e2,stroke:#dc2626,stroke-width:1px",
		"securityhub":    "fill:#ffe4e6,stroke:#e11d48,stroke-width:1px",
		"accessanalyzer": "fill:#faf5ff,stroke:#9333ea,stroke-width:1px",
		"wafv2":          "fill:#ffedd5,stroke:#ea580c,stroke-width:1px",
		"acm":            "fill:#e0f2fe,stroke:#0284c7,stroke-width:1px",
		"cloudfront":     "fill:#f0f9ff,stroke:#0369a1,stroke-width:1px",
		"apigateway":     "fill:#ecfdf5,stroke:#047857,stroke-width:1px",
		"ecr":            "fill:#eef2ff,stroke:#4338ca,stroke-width:1px",
		"eks":            "fill:#fdf2f8,stroke:#be185d,stroke-width:1px",
		"elasticache":    "fill:#fffbeb,stroke:#b45309,stroke-width:1px",
		"opensearch":     "fill:#f8fafc,stroke:#334155,stroke-width:1px",
		"redshift":       "fill:#f3e8ff,stroke:#7e22ce,stroke-width:1px",
		"msk":            "fill:#ecfeff,stroke:#0e7490,stroke-width:1px",
		"efs":            "fill:#f7fee7,stroke:#65a30d,stroke-width:1px",
	}

	serviceNodes := map[string][]string{}
	for _, n := range model.Nodes {
		serviceNodes[n.Service] = append(serviceNodes[n.Service], idByKey[n.Key])
	}
	for svc, def := range classDefs {
		nodes := serviceNodes[svc]
		if len(nodes) == 0 {
			continue
		}
		cn := mermaidID("cls_" + svc)
		b.WriteString(fmt.Sprintf("  classDef %s %s\n", cn, def))
		b.WriteString(fmt.Sprintf("  class %s %s\n", strings.Join(nodes, ","), cn))
	}

	for _, e := range model.Edges {
		f, okF := idByKey[e.From]
		t, okT := idByKey[e.To]
		if !okF || !okT {
			continue
		}
		if model.Scope.Full {
			lbl := e.Kind
			if e.Count > 1 {
				lbl = fmt.Sprintf("%s x%d", e.Kind, e.Count)
			}
			b.WriteString(fmt.Sprintf("  %s -->|%s| %s\n", f, escMermaid(lbl), t))
		} else if e.Count > 1 {
			b.WriteString(fmt.Sprintf("  %s -->|x%d| %s\n", f, e.Count, t))
		} else {
			b.WriteString(fmt.Sprintf("  %s --> %s\n", f, t))
		}
	}

	return b.Bytes(), nil
}

func mermaidID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []rune
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			out = append(out, r)
			continue
		}
		out = append(out, '_')
	}
	if len(out) == 0 {
		return "x"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "x_" + string(out)
	}
	return string(out)
}

func escMermaid(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	return s
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
