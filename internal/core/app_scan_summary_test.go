package core

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type summaryProvider struct {
	id          string
	perRegionN  int
	displayName string
}

func (p summaryProvider) ID() string { return p.id }

func (p summaryProvider) DisplayName() string {
	if p.displayName != "" {
		return p.displayName
	}
	return p.id
}

func (p summaryProvider) Scope() providers.ScopeKind { return providers.ScopeRegional }

func (p summaryProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	region := "unknown"
	if len(req.Regions) > 0 {
		region = req.Regions[0]
	}
	nodes := make([]graph.ResourceNode, 0, p.perRegionN)
	now := time.Now().UTC()
	for i := 0; i < p.perRegionN; i++ {
		pid := p.id + "-" + region + "-" + string(rune('a'+i))
		nodes = append(nodes, graph.ResourceNode{
			Key:         graph.EncodeResourceKey(req.Partition, req.AccountID, region, "test:thing", pid),
			DisplayName: pid,
			Service:     p.id,
			Type:        "test:thing",
			PrimaryID:   pid,
			CollectedAt: now,
			Source:      "test",
		})
	}
	return providers.ListResult{Nodes: nodes}, nil
}

func TestScanWithProgress_BuildsDetailedSummary(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	providerA := "summarysvc-a"
	providerB := "summarysvc-b"
	if _, ok := registry.Get(providerA); !ok {
		registry.Register(summaryProvider{id: providerA, perRegionN: 2})
	}
	if _, ok := registry.Get(providerB); !ok {
		registry.Register(summaryProvider{id: providerB, perRegionN: 1})
	}

	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1", "us-west-2", "global"},
		ProviderIDs: []string{providerA, providerB},
	}, nil)
	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}

	if res.Resources != 9 {
		t.Fatalf("resources: got %d want 9", res.Resources)
	}

	if len(res.Summary.ServiceCounts) < 2 {
		t.Fatalf("service counts: %#v", res.Summary.ServiceCounts)
	}
	if res.Summary.ServiceCounts[0].Service != providerA || res.Summary.ServiceCounts[0].Resources != 6 {
		t.Fatalf("service[0]: %#v", res.Summary.ServiceCounts[0])
	}
	if res.Summary.ServiceCounts[1].Service != providerB || res.Summary.ServiceCounts[1].Resources != 3 {
		t.Fatalf("service[1]: %#v", res.Summary.ServiceCounts[1])
	}

	if len(res.Summary.ImportantRegions) != 2 {
		t.Fatalf("important regions: %#v", res.Summary.ImportantRegions)
	}
	if res.Summary.ImportantRegions[0].Region != "us-east-1" || res.Summary.ImportantRegions[0].Resources != 3 {
		t.Fatalf("region[0]: %#v", res.Summary.ImportantRegions[0])
	}
	if res.Summary.ImportantRegions[1].Region != "us-west-2" || res.Summary.ImportantRegions[1].Resources != 3 {
		t.Fatalf("region[1]: %#v", res.Summary.ImportantRegions[1])
	}

	if res.Summary.Pricing.KnownUSD != 0 {
		t.Fatalf("known usd: got %f want 0", res.Summary.Pricing.KnownUSD)
	}
	if res.Summary.Pricing.UnknownCount != 9 {
		t.Fatalf("unknown count: got %d want 9", res.Summary.Pricing.UnknownCount)
	}
	if res.Summary.Pricing.Currency != "USD" {
		t.Fatalf("currency: got %q want USD", res.Summary.Pricing.Currency)
	}
}

func TestScanWithProgress_ImportantRegionsIncludeGlobalWhenOnlyGlobal(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	providerID := "summarysvc-global"
	if _, ok := registry.Get(providerID); !ok {
		registry.Register(summaryProvider{id: providerID, perRegionN: 2})
	}

	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"global"},
		ProviderIDs: []string{providerID},
	}, nil)
	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}

	if len(res.Summary.ImportantRegions) != 1 {
		t.Fatalf("important regions: %#v", res.Summary.ImportantRegions)
	}
	if res.Summary.ImportantRegions[0].Region != "global" {
		t.Fatalf("region: got %#v", res.Summary.ImportantRegions[0])
	}
}

func TestScanWithProgress_PricingSummaryFailureIsBestEffort(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}
	app.listServiceCostAgg = func(ctx context.Context, accountID string, regions []string) ([]store.CostAgg, error) {
		return nil, fmt.Errorf("boom")
	}

	providerID := "summarysvc-fail"
	if _, ok := registry.Get(providerID); !ok {
		registry.Register(summaryProvider{id: providerID, perRegionN: 1})
	}

	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:     "default",
		Regions:     []string{"us-east-1"},
		ProviderIDs: []string{providerID},
	}, nil)
	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}

	found := false
	for _, f := range res.StepFailures {
		if f.Phase == PhaseCost && f.ProviderID == "summary" && f.Region == "all" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cost/summary step failure, got %#v", res.StepFailures)
	}
}
