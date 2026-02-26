package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"awscope/internal/actions"
	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type testTerminalAction struct {
	id             string
	called         bool
	gotStdin       bool
	gotStdout      bool
	gotStderr      bool
	gotAutoApprove bool
	fail           error
	execCalled     bool
}

func (a *testTerminalAction) ID() string          { return a.id }
func (a *testTerminalAction) Title() string       { return "test terminal action" }
func (a *testTerminalAction) Description() string { return "test terminal action" }
func (a *testTerminalAction) Risk() actions.RiskLevel {
	return actions.RiskLow
}
func (a *testTerminalAction) Applicable(node graph.ResourceNode) bool {
	return node.Type == "ec2:instance"
}
func (a *testTerminalAction) Execute(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	a.execCalled = true
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"path": "execute"}}, nil
}
func (a *testTerminalAction) ExecuteTerminal(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	a.called = true
	a.gotStdin = exec.Stdin != nil
	a.gotStdout = exec.Stdout != nil
	a.gotStderr = exec.Stderr != nil
	a.gotAutoApprove = exec.AutoApproveTeardownOnCancel
	if a.fail != nil {
		return actions.Result{}, a.fail
	}
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"path": "terminal"}}, nil
}

type testNonTerminalAction struct {
	id       string
	called   bool
	terminal bool
}

func (a *testNonTerminalAction) ID() string          { return a.id }
func (a *testNonTerminalAction) Title() string       { return "test non-terminal action" }
func (a *testNonTerminalAction) Description() string { return "test non-terminal action" }
func (a *testNonTerminalAction) Risk() actions.RiskLevel {
	return actions.RiskLow
}
func (a *testNonTerminalAction) Applicable(node graph.ResourceNode) bool {
	return node.Type == "ec2:instance"
}
func (a *testNonTerminalAction) Execute(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	a.called = true
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"path": "execute"}}, nil
}

func TestRunAction_UsesTerminalActionAndPassesStdio(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	key := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-123")
	if err := st.UpsertResources(ctx, []graph.ResourceNode{{
		Key:         key,
		DisplayName: "i-123",
		Service:     "ec2",
		Type:        "ec2:instance",
		PrimaryID:   "i-123",
		Attributes:  map[string]any{"state": "running"},
		Source:      "test",
	}}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	id := fmt.Sprintf("test.term.%d", time.Now().UnixNano())
	a := &testTerminalAction{id: id}
	actionsRegistry.Register(a)

	oldResolver := loadActionIdentity
	loadActionIdentity = func(ctx context.Context, profileName, region string) (awsSDK.Config, aws.Identity, *aws.Loader, error) {
		return awsSDK.Config{}, aws.Identity{AccountID: "111111111111", Partition: "aws"}, nil, nil
	}
	t.Cleanup(func() {
		loadActionIdentity = oldResolver
	})

	stdin := bytes.NewBufferString("")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	res, err := RunAction(ctx, st, id, key, "default", RunActionOptions{
		Stdin:                       stdin,
		Stdout:                      stdout,
		Stderr:                      stderr,
		AutoApproveTeardownOnCancel: true,
	})
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if res.Status != "SUCCEEDED" {
		t.Fatalf("status = %q, want SUCCEEDED", res.Status)
	}
	if !a.called {
		t.Fatalf("expected ExecuteTerminal to be called")
	}
	if a.execCalled {
		t.Fatalf("did not expect Execute fallback when terminal interface is present")
	}
	if !a.gotStdin || !a.gotStdout || !a.gotStderr {
		t.Fatalf("expected stdio to be passed (stdin=%v stdout=%v stderr=%v)", a.gotStdin, a.gotStdout, a.gotStderr)
	}
	if !a.gotAutoApprove {
		t.Fatalf("expected AutoApproveTeardownOnCancel to be passed")
	}

	status, err := st.GetActionRunStatus(ctx, res.ActionRunID)
	if err != nil {
		t.Fatalf("GetActionRunStatus: %v", err)
	}
	if status != "SUCCEEDED" {
		t.Fatalf("action run status = %q, want SUCCEEDED", status)
	}
}

func TestRunAction_FailedTerminalActionSetsFailedRun(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	key := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-456")
	if err := st.UpsertResources(ctx, []graph.ResourceNode{{
		Key:         key,
		DisplayName: "i-456",
		Service:     "ec2",
		Type:        "ec2:instance",
		PrimaryID:   "i-456",
		Attributes:  map[string]any{"state": "running"},
		Source:      "test",
	}}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	id := fmt.Sprintf("test.term.fail.%d", time.Now().UnixNano())
	a := &testTerminalAction{id: id, fail: errors.New("boom")}
	actionsRegistry.Register(a)

	oldResolver := loadActionIdentity
	loadActionIdentity = func(ctx context.Context, profileName, region string) (awsSDK.Config, aws.Identity, *aws.Loader, error) {
		return awsSDK.Config{}, aws.Identity{AccountID: "111111111111", Partition: "aws"}, nil, nil
	}
	t.Cleanup(func() {
		loadActionIdentity = oldResolver
	})

	res, err := RunAction(ctx, st, id, key, "default")
	if err == nil {
		t.Fatalf("expected error")
	}
	if res.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED", res.Status)
	}
	status, err := st.GetActionRunStatus(ctx, res.ActionRunID)
	if err != nil {
		t.Fatalf("GetActionRunStatus: %v", err)
	}
	if status != "FAILED" {
		t.Fatalf("action run status = %q, want FAILED", status)
	}
}

func TestRunAction_NonTerminalUsesExecute(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	key := graph.EncodeResourceKey("aws", "111111111111", "us-east-1", "ec2:instance", "i-789")
	if err := st.UpsertResources(ctx, []graph.ResourceNode{{
		Key:         key,
		DisplayName: "i-789",
		Service:     "ec2",
		Type:        "ec2:instance",
		PrimaryID:   "i-789",
		Attributes:  map[string]any{"state": "running"},
		Source:      "test",
	}}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	id := fmt.Sprintf("test.nterm.%d", time.Now().UnixNano())
	a := &testNonTerminalAction{id: id}
	actionsRegistry.Register(a)

	oldResolver := loadActionIdentity
	loadActionIdentity = func(ctx context.Context, profileName, region string) (awsSDK.Config, aws.Identity, *aws.Loader, error) {
		return awsSDK.Config{}, aws.Identity{AccountID: "111111111111", Partition: "aws"}, nil, nil
	}
	t.Cleanup(func() {
		loadActionIdentity = oldResolver
	})

	res, err := RunAction(ctx, st, id, key, "default")
	if err != nil {
		t.Fatalf("RunAction: %v", err)
	}
	if res.Status != "SUCCEEDED" {
		t.Fatalf("status = %q, want SUCCEEDED", res.Status)
	}
	if !a.called {
		t.Fatalf("expected Execute to be called")
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := store.Open(store.OpenOptions{Path: dbPath})
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}
