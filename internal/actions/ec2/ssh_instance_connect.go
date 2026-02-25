package ec2

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"
)

func init() {
	registry.Register(OpenSSHInstanceConnect{})
}

type OpenSSHInstanceConnect struct{}

func (OpenSSHInstanceConnect) ID() string    { return "ec2.ssh" }
func (OpenSSHInstanceConnect) Title() string { return "Open SSH (Instance Connect)" }
func (OpenSSHInstanceConnect) Description() string {
	return "Open SSH to this EC2 instance using EC2 Instance Connect + open-tunnel"
}
func (OpenSSHInstanceConnect) Risk() actions.RiskLevel {
	return actions.RiskMedium
}

func (OpenSSHInstanceConnect) Applicable(node graph.ResourceNode) bool {
	if node.Service != "ec2" || node.Type != "ec2:instance" || strings.TrimSpace(node.PrimaryID) == "" {
		return false
	}
	return strings.EqualFold(instanceStateAttr(node), "running")
}

func (a OpenSSHInstanceConnect) Execute(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	return a.ExecuteTerminal(ctx, execCtx, node)
}

func (OpenSSHInstanceConnect) ExecuteTerminal(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	if err := requireRegion(execCtx.Region); err != nil {
		return actions.Result{}, err
	}
	instanceID := strings.TrimSpace(node.PrimaryID)
	if instanceID == "" {
		return actions.Result{}, fmt.Errorf("ec2 instance missing primary id")
	}

	if _, err := lookPath("aws"); err != nil {
		return actions.Result{}, fmt.Errorf("aws cli not found in PATH; install AWS CLI v2 to run ec2.ssh")
	}
	if _, err := lookPath("ssh"); err != nil {
		return actions.Result{}, fmt.Errorf("ssh binary not found in PATH; install OpenSSH client")
	}

	pubKeyPath, err := defaultPublicKeyPath()
	if err != nil {
		return actions.Result{}, err
	}
	osUser := "ubuntu"

	sendKeyArgs := []string{
		"ec2-instance-connect", "send-ssh-public-key",
		"--instance-id", instanceID,
		"--instance-os-user", osUser,
		"--region", execCtx.Region,
		"--ssh-public-key", "file://" + pubKeyPath,
	}
	if p := strings.TrimSpace(execCtx.Profile); p != "" {
		sendKeyArgs = append(sendKeyArgs, "--profile", p)
	}
	sendKey := commandContext(ctx, "aws", sendKeyArgs...)
	sendKey.Stdin = nonNilReader(execCtx.Stdin, os.Stdin)
	sendKey.Stdout = nonNilWriter(execCtx.Stdout, os.Stdout)
	sendKey.Stderr = nonNilWriter(execCtx.Stderr, os.Stderr)
	if err := sendKey.Run(); err != nil {
		return actions.Result{}, fmt.Errorf("failed to send ssh public key: %w", err)
	}

	proxy := fmt.Sprintf("ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id %s --region %s", instanceID, execCtx.Region)
	if p := strings.TrimSpace(execCtx.Profile); p != "" {
		proxy += " --profile " + p
	}
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", proxy,
		osUser + "@" + instanceID,
	}
	sshCmd := commandContext(ctx, "ssh", sshArgs...)
	sshCmd.Stdin = nonNilReader(execCtx.Stdin, os.Stdin)
	sshCmd.Stdout = nonNilWriter(execCtx.Stdout, os.Stdout)
	sshCmd.Stderr = nonNilWriter(execCtx.Stderr, os.Stderr)
	if err := sshCmd.Run(); err != nil {
		return actions.Result{}, err
	}

	return actions.Result{
		Status: "SUCCEEDED",
		Data: map[string]any{
			"mode":         "ec2-instance-connect-ssh",
			"instanceId":   instanceID,
			"region":       execCtx.Region,
			"profile":      execCtx.Profile,
			"instanceUser": osUser,
			"publicKey":    pubKeyPath,
		},
	}, nil
}

func defaultPublicKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to resolve home directory for ssh key lookup: %w", err)
	}
	candidates := []string{
		filepath.Join(home, ".ssh", "id_rsa.pub"),
		filepath.Join(home, ".ssh", "id_ed25519.pub"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("no SSH public key found; expected one of %s", strings.Join(candidates, ", "))
}

func nonNilReader(v io.Reader, fallback io.Reader) io.Reader {
	if v != nil {
		return v
	}
	return fallback
}

func nonNilWriter(v io.Writer, fallback io.Writer) io.Writer {
	if v != nil {
		return v
	}
	return fallback
}
