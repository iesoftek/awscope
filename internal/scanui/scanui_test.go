package scanui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"awscope/internal/core"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
)

type testScopeProvider struct {
	id    string
	scope providers.ScopeKind
}

func (p testScopeProvider) ID() string                 { return p.id }
func (p testScopeProvider) DisplayName() string        { return p.id }
func (p testScopeProvider) Scope() providers.ScopeKind { return p.scope }
func (p testScopeProvider) List(context.Context, awsSDK.Config, providers.ListRequest) (providers.ListResult, error) {
	return providers.ListResult{}, nil
}

func TestTrackProgressEventLifecycle(t *testing.T) {
	m := &model{active: map[string]activeStep{}}
	t0 := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	start := core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: "iam",
		Region:     "global",
		Message:    "listing",
	}
	k := stepKey(start)
	m.trackProgressEvent(start, t0)
	if len(m.active) != 1 {
		t.Fatalf("active len: got %d want 1", len(m.active))
	}
	if len(m.activeOrder) != 1 || m.activeOrder[0] != k {
		t.Fatalf("active order: %#v", m.activeOrder)
	}
	s0 := m.active[k]

	hb := start
	hb.Message = "listing page=2"
	m.trackProgressEvent(hb, t0.Add(5*time.Second))
	if len(m.active) != 1 {
		t.Fatalf("active len after heartbeat: got %d want 1", len(m.active))
	}
	s1 := m.active[k]
	if !s1.StartedAt.Equal(s0.StartedAt) {
		t.Fatalf("startedAt changed: before=%s after=%s", s0.StartedAt, s1.StartedAt)
	}
	if !s1.UpdatedAt.After(s0.UpdatedAt) {
		t.Fatalf("updatedAt did not advance: before=%s after=%s", s0.UpdatedAt, s1.UpdatedAt)
	}
	if s1.Message != "listing page=2" {
		t.Fatalf("message: got %q", s1.Message)
	}

	done := start
	done.Message = "done"
	m.trackProgressEvent(done, t0.Add(6*time.Second))
	if len(m.active) != 0 {
		t.Fatalf("active not cleared on done: %#v", m.active)
	}
	m.compactActiveOrder()
	if len(m.activeOrder) != 0 {
		t.Fatalf("activeOrder not compacted: %#v", m.activeOrder)
	}
}

func TestActivePhaseCounts(t *testing.T) {
	m := &model{
		active: map[string]activeStep{
			"a": {Phase: core.PhaseProvider},
			"b": {Phase: core.PhaseProvider},
			"c": {Phase: core.PhaseResolver},
			"d": {Phase: core.PhaseAudit},
			"e": {Phase: core.PhaseCost},
		},
	}
	total, p, r, a, c := m.activePhaseCounts()
	if total != 5 || p != 2 || r != 1 || a != 1 || c != 1 {
		t.Fatalf("counts mismatch: total=%d p=%d r=%d a=%d c=%d", total, p, r, a, c)
	}
}

func TestRenderActiveRosterTruncatesWithMore(t *testing.T) {
	now := time.Date(2026, 2, 25, 10, 2, 0, 0, time.UTC)
	m := &model{
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "iam", Region: "global", StartedAt: now.Add(-60 * time.Second)},
			"k2": {Key: "k2", Phase: core.PhaseProvider, ProviderID: "ecs", Region: "us-west-2", StartedAt: now.Add(-20 * time.Second)},
			"k3": {Key: "k3", Phase: core.PhaseAudit, ProviderID: "cloudtrail", Region: "us-east-1", StartedAt: now.Add(-120 * time.Second)},
		},
		activeOrder: []string{"k1", "k2", "k3"},
	}
	full := m.renderActiveRoster(200, now)
	fullPlain := stripANSI(full)
	if !strings.Contains(fullPlain, "[iam/global") || !strings.Contains(fullPlain, "[ecs/us-west-2") || !strings.Contains(fullPlain, "[cloudtrail/us-east-1") {
		t.Fatalf("full roster missing tokens: %q", full)
	}

	short := m.renderActiveRoster(40, now)
	shortPlain := stripANSI(short)
	if !strings.Contains(shortPlain, "(+1 more)") && !strings.Contains(shortPlain, "(+2 more)") {
		t.Fatalf("expected overflow marker in short roster, got %q", short)
	}
}

func TestRenderActiveRosterDisambiguatesSameProviderRegionAcrossPhases(t *testing.T) {
	now := time.Date(2026, 2, 25, 10, 5, 0, 0, time.UTC)
	m := &model{
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "elbv2", Region: "us-east-1", StartedAt: now.Add(-3 * time.Second)},
			"k2": {Key: "k2", Phase: core.PhaseResolver, ProviderID: "elbv2", Region: "us-east-1", StartedAt: now.Add(-4 * time.Second)},
		},
		activeOrder: []string{"k1", "k2"},
	}
	out := m.renderActiveRoster(200, now)
	outPlain := stripANSI(out)
	if !strings.Contains(outPlain, "[provider:elbv2/us-east-1") || !strings.Contains(outPlain, "[resolver:elbv2/us-east-1") {
		t.Fatalf("expected phase-prefixed labels, got %q", out)
	}
}

func TestViewAnchorsProgressLineAtBottom(t *testing.T) {
	m := &model{
		width:     80,
		height:    6,
		start:     time.Now().Add(-5 * time.Second),
		spin:      spinner.New(),
		progress:  progress.New(),
		total:     10,
		completed: 3,
		curEv: core.ScanProgressEvent{
			Phase:      core.PhaseProvider,
			ProviderID: "iam",
			Region:     "global",
			Message:    "listing",
		},
		active: map[string]activeStep{
			"k1": {Key: "k1", Phase: core.PhaseProvider, ProviderID: "iam", Region: "global", StartedAt: time.Now().Add(-2 * time.Second)},
		},
		activeOrder: []string{"k1"},
	}
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("expected %d lines, got %d", m.height, len(lines))
	}
	if !strings.Contains(stripANSI(lines[len(lines)-2]), "Scanning") {
		t.Fatalf("second-last line is not progress headline: %q", lines[len(lines)-2])
	}
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "active:") {
		t.Fatalf("last line is not active roster line: %q", lines[len(lines)-1])
	}
}

func TestSeedExpectedChecklistIncludesAllPhases(t *testing.T) {
	pidRegional := fmt.Sprintf("scanui-regional-%d", time.Now().UnixNano())
	pidGlobal := fmt.Sprintf("scanui-global-%d", time.Now().UnixNano())
	pidAccount := fmt.Sprintf("scanui-account-%d", time.Now().UnixNano())
	pidCloudTrail := fmt.Sprintf("scanui-cloudtrail-%d", time.Now().UnixNano())
	registry.Register(testScopeProvider{id: pidRegional, scope: providers.ScopeRegional})
	registry.Register(testScopeProvider{id: pidGlobal, scope: providers.ScopeGlobal})
	registry.Register(testScopeProvider{id: pidAccount, scope: providers.ScopeAccount})
	registry.Register(testScopeProvider{id: pidCloudTrail, scope: providers.ScopeRegional})

	m := &model{
		opts: Options{
			ProviderIDs: []string{pidRegional, pidGlobal, pidAccount, pidCloudTrail, "ec2", "elbv2", "cloudtrail"},
			Regions:     []string{"us-east-1", "us-west-2"},
		},
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
	}
	m.seedExpectedChecklist()
	if len(m.checklistOrder) == 0 {
		t.Fatalf("expected preseeded checklist items")
	}
	// provider
	requireChecklistKey(t, m, "provider|"+pidRegional+"|us-east-1")
	requireChecklistKey(t, m, "provider|"+pidGlobal+"|global")
	requireChecklistKey(t, m, "provider|"+pidAccount+"|account")
	// resolver (ec2+elbv2)
	requireChecklistKey(t, m, "resolver|elbv2|us-east-1")
	// audit (cloudtrail + non-global regions)
	requireChecklistKey(t, m, "audit|cloudtrail|us-west-2")
	// cost
	requireChecklistKey(t, m, "cost|"+pidRegional+"|all")
}

func TestTrackProgressEventUpdatesChecklistState(t *testing.T) {
	m := &model{
		active:         map[string]activeStep{},
		checklistItems: map[string]*stepItem{},
	}
	t0 := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	evStart := core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: "ec2",
		Region:     "us-east-1",
		Message:    "listing",
	}
	m.trackProgressEvent(evStart, t0)
	k := stepKey(evStart)
	it := m.checklistItems[k]
	if it == nil || it.State != stepRunning {
		t.Fatalf("expected running checklist state, got %#v", it)
	}

	evDone := evStart
	evDone.Message = "done"
	evDone.StepResourcesAdded = 10
	evDone.StepEdgesAdded = 8
	m.trackProgressEvent(evDone, t0.Add(3*time.Second))
	it = m.checklistItems[k]
	if it == nil || it.State != stepDone {
		t.Fatalf("expected done checklist state, got %#v", it)
	}
	if it.StepResourcesAdded != 10 || it.StepEdgesAdded != 8 {
		t.Fatalf("unexpected counters: %#v", it)
	}

	evErr := core.ScanProgressEvent{
		Phase:      core.PhaseAudit,
		ProviderID: "cloudtrail",
		Region:     "us-east-1",
		Message:    "done",
		StepError:  "rate exceeded",
	}
	m.trackProgressEvent(evErr, t0.Add(4*time.Second))
	ek := stepKey(evErr)
	eit := m.checklistItems[ek]
	if eit == nil || eit.State != stepError {
		t.Fatalf("expected error checklist state, got %#v", eit)
	}
}

func TestRenderChecklistBodyTruncates(t *testing.T) {
	m := &model{
		width:          120,
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
	}
	now := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	// populate enough rows to force truncation
	for i := 0; i < 8; i++ {
		region := fmt.Sprintf("us-east-%d", i+1)
		key := makeStepKey(core.PhaseProvider, "ec2", region)
		m.checklistItems[key] = &stepItem{
			Key:        key,
			Phase:      core.PhaseProvider,
			ProviderID: "ec2",
			Region:     region,
			State:      stepRunning,
			StartedAt:  now.Add(-10 * time.Second),
			Message:    "listing",
		}
		m.checklistOrder = append(m.checklistOrder, key)
		m.expectedKeys[key] = struct{}{}
	}
	lines := m.renderChecklistBody(80, 4, now)
	if len(lines) != 4 {
		t.Fatalf("expected height-limited lines, got %d", len(lines))
	}
	if !strings.Contains(stripANSI(lines[len(lines)-1]), "(+") {
		t.Fatalf("expected truncation marker, got %q", lines[len(lines)-1])
	}
}

func TestAutoCollapseDoneProvider(t *testing.T) {
	m := &model{
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
	}
	k := makeStepKey(core.PhaseProvider, "ec2", "us-east-1")
	m.checklistItems[k] = &stepItem{
		Key:        k,
		Phase:      core.PhaseProvider,
		ProviderID: "ec2",
		Region:     "us-east-1",
		State:      stepDone,
	}
	m.checklistOrder = append(m.checklistOrder, k)
	m.expectedKeys[k] = struct{}{}

	groups := m.rebuildChecklistGroups()
	auto := m.computeAutoCollapsed(groups)
	if !auto[providerRowID(core.PhaseProvider, "ec2")] {
		t.Fatalf("expected provider to auto-collapse when fully done")
	}
	if !auto[phaseRowID(core.PhaseProvider)] {
		t.Fatalf("expected phase to auto-collapse when all children are done")
	}
}

func TestAutoExpandOnRunningOrError(t *testing.T) {
	m := &model{
		active:         map[string]activeStep{},
		checklistItems: map[string]*stepItem{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	pid := "ec2"
	pKey := providerRowID(core.PhaseProvider, pid)
	phKey := phaseRowID(core.PhaseProvider)
	m.manualOverride[pKey] = true
	m.manualOverride[phKey] = true
	m.collapsed[pKey] = true
	m.collapsed[phKey] = true

	ev := core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: pid,
		Region:     "us-east-1",
		Message:    "listing",
	}
	m.trackProgressEvent(ev, time.Now())
	if _, ok := m.manualOverride[pKey]; ok {
		t.Fatalf("provider manual override should clear on running event")
	}
	if _, ok := m.manualOverride[phKey]; ok {
		t.Fatalf("phase manual override should clear on running event")
	}
	if m.collapsed[pKey] {
		t.Fatalf("provider should be forced expanded on running event")
	}
}

func TestBalancedOrderingPrioritizesActiveProviders(t *testing.T) {
	m := &model{
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	runningKey := makeStepKey(core.PhaseProvider, "ecs", "us-west-2")
	doneKey := makeStepKey(core.PhaseProvider, "ec2", "us-east-1")
	m.checklistItems[runningKey] = &stepItem{
		Key:        runningKey,
		Phase:      core.PhaseProvider,
		ProviderID: "ecs",
		Region:     "us-west-2",
		State:      stepRunning,
		StartedAt:  time.Now().Add(-5 * time.Second),
		Message:    "listing",
	}
	m.checklistItems[doneKey] = &stepItem{
		Key:                doneKey,
		Phase:              core.PhaseProvider,
		ProviderID:         "ec2",
		Region:             "us-east-1",
		State:              stepDone,
		StepResourcesAdded: 4,
		StepEdgesAdded:     2,
	}
	m.checklistOrder = append(m.checklistOrder, runningKey, doneKey)
	m.expectedKeys[runningKey] = struct{}{}
	m.expectedKeys[doneKey] = struct{}{}

	lines := m.renderChecklistBody(140, 20, time.Now())
	plain := make([]string, 0, len(lines))
	for _, ln := range lines {
		plain = append(plain, stripANSI(ln))
	}
	ecsIdx := -1
	ec2Idx := -1
	for i, ln := range plain {
		if strings.Contains(ln, " ecs [") {
			ecsIdx = i
		}
		if strings.Contains(ln, " ec2 [") {
			ec2Idx = i
		}
	}
	if ecsIdx == -1 || ec2Idx == -1 {
		t.Fatalf("expected provider rows for ecs and ec2, got:\n%s", strings.Join(plain, "\n"))
	}
	if ecsIdx >= ec2Idx {
		t.Fatalf("expected active provider before done-collapsed provider, ecs=%d ec2=%d", ecsIdx, ec2Idx)
	}
}

func TestManualTogglePersistsUntilSafetyOverride(t *testing.T) {
	now := time.Now()
	m := &model{
		active:         map[string]activeStep{},
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	k := makeStepKey(core.PhaseProvider, "ec2", "us-east-1")
	m.checklistItems[k] = &stepItem{
		Key:        k,
		Phase:      core.PhaseProvider,
		ProviderID: "ec2",
		Region:     "us-east-1",
		State:      stepDone,
		StartedAt:  now.Add(-2 * time.Second),
		EndedAt:    now,
	}
	m.checklistOrder = append(m.checklistOrder, k)
	m.expectedKeys[k] = struct{}{}

	_ = m.renderChecklistBody(140, 20, now)
	m.manualOverride[phaseRowID(core.PhaseProvider)] = false
	_ = m.renderChecklistBody(140, 20, now)
	m.selectedRowID = providerRowID(core.PhaseProvider, "ec2")
	initial := m.collapsed[m.selectedRowID]
	m.toggleSelectedCollapse()
	if v, ok := m.manualOverride[m.selectedRowID]; !ok || v != !initial {
		t.Fatalf("expected first toggle to invert collapsed state, initial=%v override=%v ok=%v", initial, v, ok)
	}

	// toggle back
	m.toggleSelectedCollapse()
	if v, ok := m.manualOverride[m.selectedRowID]; !ok || v != initial {
		t.Fatalf("expected second toggle to restore collapsed state, initial=%v override=%v ok=%v", initial, v, ok)
	}

	// running event should clear manual override for safety
	ev := core.ScanProgressEvent{
		Phase:      core.PhaseProvider,
		ProviderID: "ec2",
		Region:     "us-east-1",
		Message:    "listing",
	}
	m.trackProgressEvent(ev, now.Add(3*time.Second))
	if _, ok := m.manualOverride[m.selectedRowID]; ok {
		t.Fatalf("expected manual override cleared on running event")
	}
}

func TestSelectionNavigationAcrossVisibleRows(t *testing.T) {
	now := time.Now()
	m := &model{
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	keys := []string{
		makeStepKey(core.PhaseProvider, "ec2", "us-east-1"),
		makeStepKey(core.PhaseProvider, "ecs", "us-west-2"),
	}
	for _, k := range keys {
		pid := strings.Split(k, "|")[1]
		region := strings.Split(k, "|")[2]
		m.checklistItems[k] = &stepItem{
			Key:        k,
			Phase:      core.PhaseProvider,
			ProviderID: pid,
			Region:     region,
			State:      stepPending,
		}
		m.checklistOrder = append(m.checklistOrder, k)
		m.expectedKeys[k] = struct{}{}
	}
	_ = m.renderChecklistBody(140, 20, now)
	if len(m.rowOrder) == 0 {
		t.Fatalf("expected non-empty row order")
	}
	first := m.selectedRowID
	m.moveSelection(+1)
	if m.selectedRowID == first {
		t.Fatalf("expected selection to move forward")
	}
	second := m.selectedRowID
	m.moveSelection(-1)
	if m.selectedRowID != first {
		t.Fatalf("expected selection to move back to first row")
	}
	m.moveSelection(+100)
	if m.selectedRowID == second && len(m.rowOrder) > 2 {
		t.Fatalf("expected selection clamped at last visible row")
	}
}

func TestCollapsedProviderSummaryRendering(t *testing.T) {
	now := time.Now()
	m := &model{
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	k1 := makeStepKey(core.PhaseProvider, "ec2", "us-east-1")
	k2 := makeStepKey(core.PhaseProvider, "ec2", "us-west-2")
	m.checklistItems[k1] = &stepItem{
		Key:                k1,
		Phase:              core.PhaseProvider,
		ProviderID:         "ec2",
		Region:             "us-east-1",
		State:              stepDone,
		StepResourcesAdded: 10,
		StepEdgesAdded:     8,
	}
	m.checklistItems[k2] = &stepItem{
		Key:                k2,
		Phase:              core.PhaseProvider,
		ProviderID:         "ec2",
		Region:             "us-west-2",
		State:              stepDone,
		StepResourcesAdded: 5,
		StepEdgesAdded:     4,
	}
	m.checklistOrder = append(m.checklistOrder, k1, k2)
	m.expectedKeys[k1] = struct{}{}
	m.expectedKeys[k2] = struct{}{}

	m.manualOverride[phaseRowID(core.PhaseProvider)] = false
	lines := m.renderChecklistBody(160, 20, now)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "(2 steps, +res=15, +edges=12)") {
		t.Fatalf("expected collapsed provider summary, got:\n%s", joined)
	}
}

func TestExpandedProviderUsesCompactRegionSummaries(t *testing.T) {
	now := time.Now()
	m := &model{
		checklistItems: map[string]*stepItem{},
		expectedKeys:   map[string]struct{}{},
		collapsed:      map[string]bool{},
		manualOverride: map[string]bool{},
	}
	kRun := makeStepKey(core.PhaseProvider, "ecs", "us-east-1")
	kDone1 := makeStepKey(core.PhaseProvider, "ecs", "us-west-2")
	kDone2 := makeStepKey(core.PhaseProvider, "ecs", "us-east-2")
	kPending := makeStepKey(core.PhaseProvider, "ecs", "eu-west-1")
	m.checklistItems[kRun] = &stepItem{
		Key:        kRun,
		Phase:      core.PhaseProvider,
		ProviderID: "ecs",
		Region:     "us-east-1",
		State:      stepRunning,
		StartedAt:  now.Add(-7 * time.Second),
		Message:    "listing",
	}
	m.checklistItems[kDone1] = &stepItem{
		Key:                kDone1,
		Phase:              core.PhaseProvider,
		ProviderID:         "ecs",
		Region:             "us-west-2",
		State:              stepDone,
		StepResourcesAdded: 6,
		StepEdgesAdded:     3,
	}
	m.checklistItems[kDone2] = &stepItem{
		Key:                kDone2,
		Phase:              core.PhaseProvider,
		ProviderID:         "ecs",
		Region:             "us-east-2",
		State:              stepDone,
		StepResourcesAdded: 4,
		StepEdgesAdded:     2,
	}
	m.checklistItems[kPending] = &stepItem{
		Key:        kPending,
		Phase:      core.PhaseProvider,
		ProviderID: "ecs",
		Region:     "eu-west-1",
		State:      stepPending,
	}
	m.checklistOrder = append(m.checklistOrder, kRun, kDone1, kDone2, kPending)
	m.expectedKeys[kRun] = struct{}{}
	m.expectedKeys[kDone1] = struct{}{}
	m.expectedKeys[kDone2] = struct{}{}
	m.expectedKeys[kPending] = struct{}{}

	lines := m.renderChecklistBody(160, 30, now)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "pending regions=1") {
		t.Fatalf("expected pending region summary line, got:\n%s", joined)
	}
	if !strings.Contains(joined, "done regions=2 +res=10 +edges=5") {
		t.Fatalf("expected done region summary line, got:\n%s", joined)
	}
	if strings.Contains(joined, "us-west-2       +res=6 +edges=3") {
		t.Fatalf("expected done region rows to be compacted, got:\n%s", joined)
	}
}

func requireChecklistKey(t *testing.T, m *model, key string) {
	t.Helper()
	if _, ok := m.checklistItems[key]; !ok {
		t.Fatalf("missing checklist key %q", key)
	}
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(s, "")
}
