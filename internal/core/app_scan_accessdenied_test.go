package core

import (
	"context"
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
