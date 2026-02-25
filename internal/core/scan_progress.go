package core

type ScanProgressPhase string

const (
	PhaseProvider ScanProgressPhase = "provider"
	PhaseResolver ScanProgressPhase = "resolver"
	PhaseAudit    ScanProgressPhase = "audit"
	PhaseCost     ScanProgressPhase = "cost"
)

type ScanProgressEvent struct {
	Phase      ScanProgressPhase
	ProviderID string
	Region     string
	Message    string

	TotalSteps     int
	CompletedSteps int

	ResourcesSoFar int
	EdgesSoFar     int

	// Step-level detail for the most recently completed unit of work (provider/region or resolver/region).
	// This is intended for UI/logging only and may be empty for "starting" events.
	StepResourcesAdded int
	StepEdgesAdded     int
	StepTypeCounts     map[string]int

	// Optional "human" sample list for the step (e.g. instance names) so scan output is more informative.
	StepSampleLabel string   // e.g. "instances"
	StepSampleTotal int      // total items of that kind in this step (not just the sample count)
	StepSampleItems []string // sample items, already best-effort display names

	// Optional error for a completed step when running in best-effort mode.
	// If set, the step produced no writes and its +res/+edges are expected to be 0.
	StepError string
}

type ScanProgressFn func(ev ScanProgressEvent)
