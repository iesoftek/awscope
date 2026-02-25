package core

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/aws"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type denyProvider struct {
	id    string
	scope providers.ScopeKind
}

func (p denyProvider) ID() string          { return p.id }
func (p denyProvider) DisplayName() string { return p.id }
func (p denyProvider) Scope() providers.ScopeKind {
	return p.scope
}

func (p denyProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	apiErr := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "UnknownError"}
	re := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 403}},
		Err:      apiErr,
	}
	httpErr := &awshttp.ResponseError{
		ResponseError: re,
		RequestID:     "reqid",
	}
	return providers.ListResult{}, &smithy.OperationError{
		ServiceID:     "Lambda",
		OperationName: "ListFunctions",
		Err:           httpErr,
	}
}

type endpointFailProvider struct {
	id    string
	scope providers.ScopeKind
}

func (p endpointFailProvider) ID() string          { return p.id }
func (p endpointFailProvider) DisplayName() string { return p.id }
func (p endpointFailProvider) Scope() providers.ScopeKind {
	return p.scope
}

func (p endpointFailProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	return providers.ListResult{}, errors.New(`operation error AccessAnalyzer: ListAnalyzers, https response error StatusCode: 0, RequestID: , request send failed, Get "https://access-analyzer.il-central-1.amazonaws.com/analyzer": dial tcp: lookup access-analyzer.il-central-1.amazonaws.com: no such host`)
}

type regionUnsupportedProvider struct {
	id    string
	scope providers.ScopeKind
}

func (p regionUnsupportedProvider) ID() string          { return p.id }
func (p regionUnsupportedProvider) DisplayName() string { return p.id }
func (p regionUnsupportedProvider) Scope() providers.ScopeKind {
	return p.scope
}

func (p regionUnsupportedProvider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	apiErr := &smithy.GenericAPIError{Code: "UnknownOperationException", Message: "The requested operation is not supported in the called region."}
	re := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}},
		Err:      apiErr,
	}
	httpErr := &awshttp.ResponseError{
		ResponseError: re,
		RequestID:     "reqid",
	}
	return providers.ListResult{}, &smithy.OperationError{
		ServiceID:     "SageMaker",
		OperationName: "ListNotebookInstances",
		Err:           httpErr,
	}
}

func TestScanWithProgress_SkipsAccessDeniedSteps(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	pid := "testaccessdenied"
	if _, ok := registry.Get(pid); !ok {
		registry.Register(denyProvider{id: pid, scope: providers.ScopeRegional})
	}

	var sawStepErr bool
	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:        "default",
		Regions:        []string{"us-east-1"},
		ProviderIDs:    []string{pid},
		MaxConcurrency: 2,
	}, func(ev ScanProgressEvent) {
		if ev.Message == "done" && ev.ProviderID == pid && ev.Region == "us-east-1" && ev.StepError != "" {
			sawStepErr = true
		}
	})

	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}
	if !sawStepErr {
		t.Fatalf("expected StepError progress event")
	}
	if len(res.StepFailures) != 1 {
		t.Fatalf("expected 1 StepFailure, got %d", len(res.StepFailures))
	}
	if res.StepFailures[0].ProviderID != pid || res.StepFailures[0].Region != "us-east-1" {
		t.Fatalf("unexpected StepFailure: %#v", res.StepFailures[0])
	}
	if res.Resources != 0 || res.Edges != 0 {
		t.Fatalf("result: %#v", res)
	}

	// Sanity: final timestamps should not depend on writes.
	_ = time.Now()
}

func TestScanWithProgress_SkipsEndpointUnavailableSteps(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	pid := "testendpointunavailable"
	if _, ok := registry.Get(pid); !ok {
		registry.Register(endpointFailProvider{id: pid, scope: providers.ScopeRegional})
	}

	var sawStepErr bool
	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:        "default",
		Regions:        []string{"il-central-1"},
		ProviderIDs:    []string{pid},
		MaxConcurrency: 2,
	}, func(ev ScanProgressEvent) {
		if ev.Message == "done" && ev.ProviderID == pid && ev.Region == "il-central-1" && ev.StepError != "" {
			sawStepErr = true
		}
	})

	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}
	if !sawStepErr {
		t.Fatalf("expected StepError progress event")
	}
	if len(res.StepFailures) != 1 {
		t.Fatalf("expected 1 StepFailure, got %d", len(res.StepFailures))
	}
	if res.StepFailures[0].ProviderID != pid || res.StepFailures[0].Region != "il-central-1" {
		t.Fatalf("unexpected StepFailure: %#v", res.StepFailures[0])
	}
	if res.Resources != 0 || res.Edges != 0 {
		t.Fatalf("result: %#v", res)
	}
}

func TestScanWithProgress_SkipsRegionUnsupportedSteps(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	app := New(st)
	app.loader = fakeLoader{id: aws.Identity{AccountID: "123456789012", Partition: "aws", Arn: "arn:aws:sts::123456789012:assumed-role/x/y"}}

	pid := "testregionunsupported"
	if _, ok := registry.Get(pid); !ok {
		registry.Register(regionUnsupportedProvider{id: pid, scope: providers.ScopeRegional})
	}

	var sawStepErr bool
	res, scanErr := app.ScanWithProgress(ctx, ScanOptions{
		Profile:        "default",
		Regions:        []string{"ap-east-2"},
		ProviderIDs:    []string{pid},
		MaxConcurrency: 2,
	}, func(ev ScanProgressEvent) {
		if ev.Message == "done" && ev.ProviderID == pid && ev.Region == "ap-east-2" && ev.StepError != "" {
			sawStepErr = true
		}
	})

	if scanErr != nil {
		t.Fatalf("scan: %v", scanErr)
	}
	if !sawStepErr {
		t.Fatalf("expected StepError progress event")
	}
	if len(res.StepFailures) != 1 {
		t.Fatalf("expected 1 StepFailure, got %d", len(res.StepFailures))
	}
	if res.StepFailures[0].ProviderID != pid || res.StepFailures[0].Region != "ap-east-2" {
		t.Fatalf("unexpected StepFailure: %#v", res.StepFailures[0])
	}
	if res.Resources != 0 || res.Edges != 0 {
		t.Fatalf("result: %#v", res)
	}
}
