package ec2

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"awscope/internal/actions"
	"awscope/internal/graph"
)

func TestOpenSSHInstanceConnectApplicable(t *testing.T) {
	a := OpenSSHInstanceConnect{}
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
}

func TestOpenSSHInstanceConnectExecuteTerminalArgs(t *testing.T) {
	home := t.TempDir()
	pub := filepath.Join(home, ".ssh", "id_rsa.pub")
	if err := os.MkdirAll(filepath.Dir(pub), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(pub, []byte("ssh-rsa AAAA test"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("Setenv HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	oldLookPath := lookPath
	oldCmd := commandContext
	t.Cleanup(func() {
		lookPath = oldLookPath
		commandContext = oldCmd
	})
	lookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	type invocation struct {
		name string
		args []string
	}
	var calls []invocation
	commandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		calls = append(calls, invocation{name: name, args: append([]string(nil), arg...)})
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	a := OpenSSHInstanceConnect{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region:  "us-west-2",
		Profile: "default",
	}, graph.ResourceNode{
		Service:   "ec2",
		Type:      "ec2:instance",
		PrimaryID: "i-123",
	})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 command invocations, got %d", len(calls))
	}
	if calls[0].name != "aws" {
		t.Fatalf("first invocation should be aws, got %q", calls[0].name)
	}
	sendKeyArgs := strings.Join(calls[0].args, " ")
	mustContain(t, sendKeyArgs, "ec2-instance-connect send-ssh-public-key")
	mustContain(t, sendKeyArgs, "--instance-id i-123")
	mustContain(t, sendKeyArgs, "--instance-os-user ubuntu")
	mustContain(t, sendKeyArgs, "--region us-west-2")
	mustContain(t, sendKeyArgs, "--profile default")
	mustContain(t, sendKeyArgs, "file://"+pub)

	if calls[1].name != "ssh" {
		t.Fatalf("second invocation should be ssh, got %q", calls[1].name)
	}
	sshArgs := strings.Join(calls[1].args, " ")
	mustContain(t, sshArgs, "StrictHostKeyChecking=no")
	mustContain(t, sshArgs, "ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id i-123 --region us-west-2 --profile default")
	mustContain(t, sshArgs, "ubuntu@i-123")
}

func TestOpenSSHInstanceConnectExecuteTerminalMissingPublicKey(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("Setenv HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	oldLookPath := lookPath
	oldCmd := commandContext
	t.Cleanup(func() {
		lookPath = oldLookPath
		commandContext = oldCmd
	})
	lookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	commandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	a := OpenSSHInstanceConnect{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, graph.ResourceNode{
		Service:   "ec2",
		Type:      "ec2:instance",
		PrimaryID: "i-123",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "no ssh public key found") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
