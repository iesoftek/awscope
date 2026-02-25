package main

import (
	"fmt"
	"io"
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

func formatSecuritySummary(sec core.ScanSecuritySummary) string {
	return formatSecuritySummaryWithOptions(sec, securitySummaryFormatOptions{
		ShowDetails: true,
		Color:       false,
	})
}

type securitySummaryFormatOptions struct {
	ShowDetails bool
	Color       bool
}

func formatSecuritySummaryWithOptions(sec core.ScanSecuritySummary, opts securitySummaryFormatOptions) string {
	var b strings.Builder
	b.WriteString(colorize("security findings:", ansiBold, opts.Color))
	b.WriteByte('\n')

	critical := sec.AffectedBySeverity[core.ScanSecuritySeverityCritical]
	high := sec.AffectedBySeverity[core.ScanSecuritySeverityHigh]
	medium := sec.AffectedBySeverity[core.ScanSecuritySeverityMedium]
	low := sec.AffectedBySeverity[core.ScanSecuritySeverityLow]
	fmt.Fprintf(&b, "  posture (affected): %s=%d %s=%d %s=%d %s=%d\n",
		severityLabel(core.ScanSecuritySeverityCritical, opts.Color), critical,
		severityLabel(core.ScanSecuritySeverityHigh, opts.Color), high,
		severityLabel(core.ScanSecuritySeverityMedium, opts.Color), medium,
		severityLabel(core.ScanSecuritySeverityLow, opts.Color), low,
	)
	fmt.Fprintf(&b, "  assessed checks: %d (skipped: %d)\n", sec.Coverage.AssessedChecks, sec.Coverage.SkippedChecks)
	if opts.ShowDetails {
		b.WriteString("  details: expanded\n")
	} else {
		b.WriteString("  details: collapsed\n")
	}

	if len(sec.Findings) == 0 {
		b.WriteString("  findings: none\n")
	} else {
		findings := append([]core.ScanSecurityFinding(nil), sec.Findings...)
		extra := 0
		if len(findings) > 12 {
			extra = len(findings) - 12
			findings = findings[:12]
		}

		order := []core.ScanSecuritySeverity{
			core.ScanSecuritySeverityCritical,
			core.ScanSecuritySeverityHigh,
			core.ScanSecuritySeverityMedium,
			core.ScanSecuritySeverityLow,
		}
		for _, sev := range order {
			var group []core.ScanSecurityFinding
			for _, f := range findings {
				if f.Severity == sev {
					group = append(group, f)
				}
			}
			if len(group) == 0 {
				continue
			}
			fmt.Fprintf(&b, "  %s:\n", severityLabel(sev, opts.Color))
			for _, f := range group {
				fmt.Fprintf(&b, "    [%s] %s | affected=%d", f.CheckID, f.Title, f.AffectedCount)
				if s := strings.TrimSpace(f.ControlRef); s != "" {
					fmt.Fprintf(&b, " | ref=%s", s)
				}
				b.WriteByte('\n')
				if !opts.ShowDetails {
					continue
				}
				if len(f.Regions) > 0 {
					regions := append([]string(nil), f.Regions...)
					if len(regions) > 5 {
						fmt.Fprintf(&b, "      regions: %s (+%d more)\n", strings.Join(regions[:5], ","), len(regions)-5)
					} else {
						fmt.Fprintf(&b, "      regions: %s\n", strings.Join(regions, ","))
					}
				}
				if len(f.Samples) > 0 {
					samples := append([]string(nil), f.Samples...)
					if len(samples) > 5 {
						fmt.Fprintf(&b, "      samples: %s (+%d more)\n", strings.Join(samples[:5], ", "), len(samples)-5)
					} else {
						fmt.Fprintf(&b, "      samples: %s\n", strings.Join(samples, ", "))
					}
				}
			}
		}
		if extra > 0 {
			fmt.Fprintf(&b, "  ... (+%d more findings)\n", extra)
		}
	}

	if len(sec.Coverage.MissingServices) > 0 {
		fmt.Fprintf(&b, "  coverage gaps: services not assessed: %s\n", strings.Join(sec.Coverage.MissingServices, ","))
	}
	return strings.TrimRight(b.String(), "\n")
}

func parseSecurityDetailView(view string) (showDetails bool, err error) {
	v := strings.TrimSpace(strings.ToLower(view))
	switch v {
	case "", "summary", "collapsed", "collapse":
		return false, nil
	case "detailed", "expanded", "expand":
		return true, nil
	default:
		return false, fmt.Errorf("invalid security view %q (expected summary|detailed)", view)
	}
}

func resolveColorEnabled(mode string, w io.Writer) (bool, error) {
	m := strings.TrimSpace(strings.ToLower(mode))
	switch m {
	case "", "auto":
		if _, ok := os.LookupEnv("NO_COLOR"); ok {
			return false, nil
		}
		if strings.TrimSpace(os.Getenv("FORCE_COLOR")) != "" {
			return true, nil
		}
		return isTTYWriter(w), nil
	case "always":
		return true, nil
	case "never":
		return false, nil
	default:
		return false, fmt.Errorf("invalid color mode %q (expected auto|always|never)", mode)
	}
}

func isTTYWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "1"
	ansiRed    = "31"
	ansiYellow = "33"
	ansiCyan   = "36"
)

func severityLabel(sev core.ScanSecuritySeverity, color bool) string {
	text := string(sev)
	if !color {
		return text
	}
	switch sev {
	case core.ScanSecuritySeverityCritical:
		return colorize(text, ansiBold+";"+ansiRed, true)
	case core.ScanSecuritySeverityHigh:
		return colorize(text, ansiRed, true)
	case core.ScanSecuritySeverityMedium:
		return colorize(text, ansiYellow, true)
	case core.ScanSecuritySeverityLow:
		return colorize(text, ansiCyan, true)
	default:
		return text
	}
}

func colorize(s, code string, enabled bool) string {
	if !enabled || strings.TrimSpace(code) == "" || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + ansiReset
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
