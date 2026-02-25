package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

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

func intEnvOr(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
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

func formatScanPerformanceSummary(res core.ScanResult) string {
	p := res.Performance
	if p.TotalDuration <= 0 && len(p.PhaseDurations) == 0 && len(p.SlowSteps) == 0 {
		return "performance:\n  - unavailable"
	}

	var b strings.Builder
	delta := p.TotalDuration - p.TargetDuration
	targetStatus := "met"
	if !p.TargetMet {
		targetStatus = fmt.Sprintf("missed by %s", formatDurationAbs(delta))
	}
	fmt.Fprintf(&b, "performance: total=%s target=%s (%s)\n",
		formatDurationCompact(p.TotalDuration),
		formatDurationCompact(p.TargetDuration),
		targetStatus,
	)

	phaseOrder := []core.ScanProgressPhase{
		core.PhaseProvider,
		core.PhaseResolver,
		core.PhaseAudit,
		core.PhaseCost,
	}
	parts := make([]string, 0, len(phaseOrder))
	for _, ph := range phaseOrder {
		if d, ok := p.PhaseDurations[ph]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", ph, formatDurationCompact(d)))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "  phase %s\n", strings.Join(parts, " "))
	}

	if len(p.SlowSteps) > 0 {
		b.WriteString("  slow steps:\n")
		steps := append([]core.ScanSlowStep(nil), p.SlowSteps...)
		sort.Slice(steps, func(i, j int) bool {
			if steps[i].Duration != steps[j].Duration {
				return steps[i].Duration > steps[j].Duration
			}
			if steps[i].Phase != steps[j].Phase {
				return steps[i].Phase < steps[j].Phase
			}
			if steps[i].ProviderID != steps[j].ProviderID {
				return steps[i].ProviderID < steps[j].ProviderID
			}
			return steps[i].Region < steps[j].Region
		})
		if len(steps) > 10 {
			steps = steps[:10]
		}
		for _, s := range steps {
			fmt.Fprintf(&b, "    %-8s %-16s %-14s %s\n",
				s.Phase, s.ProviderID, s.Region, formatDurationCompact(s.Duration))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatDurationCompact(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func formatDurationAbs(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	return formatDurationCompact(d)
}
