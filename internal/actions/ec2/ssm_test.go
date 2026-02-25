package ec2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"awscope/internal/actions"
	"awscope/internal/graph"
)

func TestOpenSSMShellApplicable(t *testing.T) {
	a := OpenSSMShell{}
	node := graph.ResourceNode{
		Service:   "ec2",
		Type:      "ec2:instance",
		PrimaryID: "i-123",
		Attributes: map[string]any{
			"state": "running",
		},
	}
	if !a.Applicable(node) {
		t.Fatalf("expected running ec2 instance to be applicable")
	}

	node.Attributes["state"] = "stopped"
	if a.Applicable(node) {
		t.Fatalf("expected stopped ec2 instance to be inapplicable")
	}

	node.Type = "ec2:volume"
	if a.Applicable(node) {
		t.Fatalf("expected non-instance type to be inapplicable")
	}
}

func TestSSMStartSessionArgs(t *testing.T) {
	got := ssmStartSessionArgs("i-123", "us-east-1", "default")
	want := []string{"ssm", "start-session", "--target", "i-123", "--region", "us-east-1", "--profile", "default"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args mismatch: got=%v want=%v", got, want)
	}

	got = ssmStartSessionArgs("i-123", "us-east-1", "")
	want = []string{"ssm", "start-session", "--target", "i-123", "--region", "us-east-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args mismatch without profile: got=%v want=%v", got, want)
	}
}

func TestOpenSSMShellExecuteTerminalPreflightMissingAWS(t *testing.T) {
	oldLookPath := lookPath
	oldCmd := commandContext
	t.Cleanup(func() {
		lookPath = oldLookPath
		commandContext = oldCmd
	})

	lookPath = func(file string) (string, error) {
		if file == "aws" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	a := OpenSSMShell{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, graph.ResourceNode{
		Service:   "ec2",
		Type:      "ec2:instance",
		PrimaryID: "i-123",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "aws cli not found") {
		t.Fatalf("expected aws cli preflight error, got %v", err)
	}
}

func TestOpenSSMShellExecuteTerminalPreflightMissingPlugin(t *testing.T) {
	oldLookPath := lookPath
	oldCmd := commandContext
	t.Cleanup(func() {
		lookPath = oldLookPath
		commandContext = oldCmd
	})

	lookPath = func(file string) (string, error) {
		if file == "session-manager-plugin" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	a := OpenSSMShell{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, graph.ResourceNode{
		Service:   "ec2",
		Type:      "ec2:instance",
		PrimaryID: "i-123",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "session-manager-plugin") {
		t.Fatalf("expected plugin preflight error, got %v", err)
	}
}
