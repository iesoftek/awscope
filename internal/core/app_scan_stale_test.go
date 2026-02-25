package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type staleProviderConfig struct {
	mu         sync.RWMutex
	resources  map[string][]string
	skipRegion map[string]bool
	fatal      bool
}

type staleProvider struct {
	id  string
	cfg *staleProviderConfig
}

func (p staleProvider) ID() string          { return p.id }
func (p staleProvider) DisplayName() string { return p.id }
func (p staleProvider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

func (p staleProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	region := ""
	if len(req.Regions) > 0 {
		region = req.Regions[0]
	}
	p.cfg.mu.RLock()
	skip := p.cfg.skipRegion[region]
	fatal := p.cfg.fatal
	ids := append([]string{}, p.cfg.resources[region]...)
	p.cfg.mu.RUnlock()

	if skip {
		if fatal {
			return providers.ListResult{}, fmt.Errorf("boom %s", region)
		}
		return providers.ListResult{}, fmt.Errorf("access denied for %s", region)
	}

	nodes := make([]graph.ResourceNode, 0, len(ids))
	for _, id := range ids {
		k := graph.EncodeResourceKey(req.Partition, req.AccountID, region, "stale:thing", id)
		nodes = append(nodes, graph.ResourceNode{
			Key:         k,
			DisplayName: id,
			Service:     p.id,
			Type:        "stale:thing",
			PrimaryID:   id,
			CollectedAt: time.Now().UTC(),
			Source:      "test",
		})
	}
	return providers.ListResult{Nodes: nodes}, nil
}

func TestScanWithProgress_StaleScopeOnlyForSuccessfulRegions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	pid := fmt.Sprintf("staleprov-%d", time.Now().UnixNano())
	cfg := &staleProviderConfig{resources: map[string][]string{}, skipRegion: map[string]bool{}}
	registry.Register(staleProvider{id: pid, cfg: cfg})

	cfg.mu.Lock()
	cfg.resources = map[string][]string{
		"us-east-1": {"east-1"},
		"us-west-2": {"west-1"},
	}
	cfg.skipRegion = map[string]bool{}
	cfg.fatal = false
	cfg.mu.Unlock()

	if _, err := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1", "us-west-2"},
		ProviderIDs: []string{pid},
		StalePolicy: StalePolicyHide,
	}, nil); err != nil {
		t.Fatalf("scan1: %v", err)
	}

	cfg.mu.Lock()
	cfg.resources = map[string][]string{
		"us-east-1": {"east-1"},
		"us-west-2": {},
	}
	cfg.skipRegion = map[string]bool{"us-west-2": true}
	cfg.fatal = false
	cfg.mu.Unlock()

	res2, err := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1", "us-west-2"},
		ProviderIDs: []string{pid},
		StalePolicy: StalePolicyHide,
	}, nil)
	if err != nil {
		t.Fatalf("scan2: %v", err)
	}
	if len(res2.StepFailures) == 0 {
		t.Fatalf("expected step failures for skipped region")
	}

	westCount, err := st.CountResourceSummariesByServiceAndRegions(ctx, pid, []string{"us-west-2"}, "west-1")
	if err != nil {
		t.Fatalf("count west: %v", err)
	}
	if westCount != 1 {
		t.Fatalf("expected west resource to stay visible when region scan failed, got count=%d", westCount)
	}
}

func TestScanWithProgress_FatalErrorDoesNotMarkStale(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	pid := fmt.Sprintf("staleprov-fatal-%d", time.Now().UnixNano())
	cfg := &staleProviderConfig{resources: map[string][]string{"us-east-1": {"east-1"}}, skipRegion: map[string]bool{}, fatal: false}
	registry.Register(staleProvider{id: pid, cfg: cfg})

	if _, err := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1"},
		ProviderIDs: []string{pid},
		StalePolicy: StalePolicyHide,
	}, nil); err != nil {
		t.Fatalf("scan1: %v", err)
	}

	cfg.mu.Lock()
	cfg.skipRegion = map[string]bool{"us-east-1": true}
	cfg.fatal = true
	cfg.mu.Unlock()

	if _, err := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1"},
		ProviderIDs: []string{pid},
		StalePolicy: StalePolicyHide,
	}, nil); err == nil {
		t.Fatalf("expected fatal scan error")
	}

	eastCount, err := st.CountResourceSummariesByServiceAndRegions(ctx, pid, []string{"us-east-1"}, "east-1")
	if err != nil {
		t.Fatalf("count east: %v", err)
	}
	if eastCount != 1 {
		t.Fatalf("expected east resource to remain visible after fatal scan, got count=%d", eastCount)
	}
}
