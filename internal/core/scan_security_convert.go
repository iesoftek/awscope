package core

import "awscope/internal/security"

func ScanSecuritySummaryFromEvaluator(s security.Summary) ScanSecuritySummary {
	out := ScanSecuritySummary{
		Findings:           make([]ScanSecurityFinding, 0, len(s.Findings)),
		AffectedBySeverity: map[ScanSecuritySeverity]int{},
		Coverage: ScanSecurityCoverage{
			AssessedChecks:  s.Coverage.AssessedChecks,
			SkippedChecks:   s.Coverage.SkippedChecks,
			MissingServices: append([]string{}, s.Coverage.MissingServices...),
		},
	}

	for _, f := range s.Findings {
		out.Findings = append(out.Findings, ScanSecurityFinding{
			CheckID:       f.CheckID,
			Severity:      ScanSecuritySeverity(f.Severity),
			Title:         f.Title,
			Service:       f.Service,
			ControlRef:    f.ControlRef,
			GuidanceURL:   f.GuidanceURL,
			AffectedCount: f.AffectedCount,
			Regions:       append([]string{}, f.Regions...),
			Samples:       append([]string{}, f.Samples...),
		})
	}

	for sev, n := range s.AffectedBySeverity {
		out.AffectedBySeverity[ScanSecuritySeverity(sev)] = n
	}
	return out
}
