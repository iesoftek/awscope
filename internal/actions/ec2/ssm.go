package ec2

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"
)

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext
)

func init() {
	registry.Register(OpenSSMShell{})
}

type OpenSSMShell struct{}

func (OpenSSMShell) ID() string    { return "ec2.ssm-shell" }
func (OpenSSMShell) Title() string { return "Open SSM shell" }
func (OpenSSMShell) Description() string {
	return "Open an AWS Systems Manager shell session to this EC2 instance"
}
func (OpenSSMShell) Risk() actions.RiskLevel {
	return actions.RiskMedium
}

func (OpenSSMShell) Applicable(node graph.ResourceNode) bool {
	if node.Service != "ec2" || node.Type != "ec2:instance" || strings.TrimSpace(node.PrimaryID) == "" {
		return false
	}
	return strings.EqualFold(instanceStateAttr(node), "running")
}

func (a OpenSSMShell) Execute(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	return a.ExecuteTerminal(ctx, execCtx, node)
}

func (OpenSSMShell) ExecuteTerminal(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	if err := requireRegion(execCtx.Region); err != nil {
		return actions.Result{}, err
	}
	target := strings.TrimSpace(node.PrimaryID)
	if target == "" {
		return actions.Result{}, fmt.Errorf("ec2 instance missing primary id")
	}

	if _, err := lookPath("aws"); err != nil {
		return actions.Result{}, fmt.Errorf("aws cli not found in PATH; install AWS CLI v2 to run ec2.ssm-shell")
	}
	if _, err := lookPath("session-manager-plugin"); err != nil {
		return actions.Result{}, fmt.Errorf("session-manager-plugin not found in PATH; install AWS Session Manager Plugin")
	}

	args := ssmStartSessionArgs(target, execCtx.Region, execCtx.Profile)
	cmd := commandContext(ctx, "aws", args...)
	cmd.Stdin = nonNilReader(execCtx.Stdin, os.Stdin)
	cmd.Stdout = nonNilWriter(execCtx.Stdout, os.Stdout)
	cmd.Stderr = nonNilWriter(execCtx.Stderr, os.Stderr)

	if err := cmd.Run(); err != nil {
		return actions.Result{}, err
	}
	return actions.Result{
		Status: "SUCCEEDED",
		Data: map[string]any{
			"mode":       "ssm-shell",
			"instanceId": target,
			"region":     execCtx.Region,
			"profile":    execCtx.Profile,
		},
	}, nil
}

func ssmStartSessionArgs(target, region, profile string) []string {
	args := []string{
		"ssm", "start-session",
		"--target", target,
		"--region", region,
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

func instanceStateAttr(node graph.ResourceNode) string {
	if node.Attributes == nil {
		return ""
	}
	v, _ := node.Attributes["state"]
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
