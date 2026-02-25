package main

import (
	"fmt"
	"strings"

	"awscope/internal/core"
	"awscope/internal/cost"
)

func parseCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func sanitizeFilename(s string) string {
	if strings.TrimSpace(s) == "" {
		return "all"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "._-")
}

func formatDetailedScanSummary(res core.ScanResult) string {
	var b strings.Builder
	b.WriteString("summary:\n")

	b.WriteString("  resources by service:\n")
	if len(res.Summary.ServiceCounts) == 0 {
		b.WriteString("    - none\n")
	} else {
		rows := res.Summary.ServiceCounts
		extra := 0
		if len(rows) > 12 {
			extra = len(rows) - 12
			rows = rows[:12]
		}
		width := 0
		for _, r := range rows {
			if len(r.Service) > width {
				width = len(r.Service)
			}
		}
		if width < 10 {
			width = 10
		}
		for _, r := range rows {
			fmt.Fprintf(&b, "    %-*s %6d\n", width, r.Service, r.Resources)
		}
		if extra > 0 {
			fmt.Fprintf(&b, "    ... (+%d more)\n", extra)
		}
	}

	b.WriteString("  important regions (top 5 by resource count):\n")
	if len(res.Summary.ImportantRegions) == 0 {
		b.WriteString("    - none\n")
	} else {
		width := 0
		for _, r := range res.Summary.ImportantRegions {
			if len(r.Region) > width {
				width = len(r.Region)
			}
		}
		if width < 10 {
			width = 10
		}
		for _, r := range res.Summary.ImportantRegions {
			fmt.Fprintf(&b, "    %-*s %6d (%.1f%%)\n", width, r.Region, r.Resources, r.SharePct)
		}
	}

	b.WriteString("  estimated monthly pricing:\n")
	fmt.Fprintf(&b, "    known total: %s\n", cost.FormatUSDPerMonthFull(res.Summary.Pricing.KnownUSD))
	fmt.Fprintf(&b, "    unknown resources: %d", res.Summary.Pricing.UnknownCount)

	return b.String()
}
