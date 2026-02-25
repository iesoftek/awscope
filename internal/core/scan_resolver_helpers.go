package core

import (
	"fmt"
	"strings"

	"awscope/internal/graph"
)

func collectInstanceTargetGroupsByRegion(nodes []graph.ResourceNode) map[string][]string {
	out := map[string][]string{}
	for _, n := range nodes {
		if n.Service != "elbv2" || n.Type != "elbv2:target-group" || strings.TrimSpace(n.PrimaryID) == "" {
			continue
		}
		targetType := ""
		if raw, ok := n.Attributes["targetType"]; ok && raw != nil {
			targetType = strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
			if targetType == "<nil>" {
				targetType = ""
			}
		}
		if targetType != "" && targetType != "instance" {
			continue
		}
		_, _, region, _, _, err := graph.ParseResourceKey(n.Key)
		if err != nil || strings.TrimSpace(region) == "" {
			continue
		}
		out[region] = append(out[region], strings.TrimSpace(n.PrimaryID))
	}
	return out
}
