package security

import "awscope/internal/graph"

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

type Finding struct {
	CheckID       string
	Severity      Severity
	Title         string
	Service       string
	ControlRef    string
	GuidanceURL   string
	AffectedCount int
	Regions       []string
	Samples       []string
}

type Coverage struct {
	AssessedChecks  int
	SkippedChecks   int
	MissingServices []string
}

type Summary struct {
	Findings           []Finding
	AffectedBySeverity map[Severity]int
	Coverage           Coverage
}

type EvaluateInput struct {
	Nodes           []graph.ResourceNode
	SelectedRegions []string
	ScannedServices []string
	MaxKeyAgeDays   int
}
