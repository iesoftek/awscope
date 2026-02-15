package core

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type fakeLoader struct {
	id aws.Identity
}

func (f fakeLoader) Load(ctx context.Context, profile, region string) (awsSDK.Config, aws.Identity, error) {
	return awsSDK.Config{Region: region}, f.id, nil
}

type fakeProvider struct {
	id    string
	scope providers.ScopeKind

	release <-chan struct{}
	cur     *int32
	maxSeen *int32
}

func (p fakeProvider) ID() string          { return p.id }
func (p fakeProvider) DisplayName() string { return p.id }
func (p fakeProvider) Scope() providers.ScopeKind {
	return p.scope
}

func (p fakeProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	// Concurrency probe.
	n := atomic.AddInt32(p.cur, 1)
	for {
		m := atomic.LoadInt32(p.maxSeen)
		if n <= m || atomic.CompareAndSwapInt32(p.maxSeen, m, n) {
			break
		}
	}

	// Block until released so multiple tasks overlap.
	select {
	case <-p.release:
	case <-ctx.Done():
		atomic.AddInt32(p.cur, -1)
		return providers.ListResult{}, ctx.Err()
	}

	region := "unknown"
	if len(req.Regions) > 0 {
		region = req.Regions[0]
	}
	key := graph.EncodeResourceKey(req.Partition, req.AccountID, region, "test:thing", p.id+"-"+region)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: p.id + "-" + region,
		Service:     "test",
		Type:        "test:thing",
		PrimaryID:   p.id + "-" + region,
		CollectedAt: time.Now().UTC(),
		Source:      "test",
	}
	edge := graph.RelationshipEdge{
		From:        key,
		To:          key,
		Kind:        "self",
		Meta:        map[string]any{"direct": true},
		CollectedAt: time.Now().UTC(),
	}

	atomic.AddInt32(p.cur, -1)
	return providers.ListResult{Nodes: []graph.ResourceNode{node}, Edges: []graph.RelationshipEdge{edge}}, nil
}

func TestScanWithProgress_RespectsMaxConcurrency(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	release := make(chan struct{})
	var cur, maxSeen int32

	pid := "testconcurrency"
	if _, ok := registry.Get(pid); !ok {
		registry.Register(fakeProvider{id: pid, scope: providers.ScopeRegional, release: release, cur: &cur, maxSeen: &maxSeen})
	}

	done := make(chan struct{})
	var res ScanResult
	var scanErr error
	go func() {
		defer close(done)
		res, scanErr = app.ScanWithProgress(ctx, ScanOptions{
			Profile:        "default",
			Regions:        []string{"us-east-1", "us-west-2", "eu-west-1", "ap-south-1"},
			ProviderIDs:    []string{pid},
			MaxConcurrency: 2,
		}, nil)
	}()

	// Wait until we observe the cap, then release work.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&maxSeen) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	<-done

	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}
	if atomic.LoadInt32(&maxSeen) > 2 {
		t.Fatalf("max concurrency: got %d want <=2", maxSeen)
	}
	if res.Resources != 4 || res.Edges != 4 {
		t.Fatalf("result: %#v", res)
	}
}
