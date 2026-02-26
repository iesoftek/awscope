package actions

import (
	"context"
	"io"

	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type RiskLevel int

const (
	RiskLow RiskLevel = iota
	RiskMedium
	RiskHigh
)

type ExecContext struct {
	Store       *store.Store
	Loader      *aws.Loader
	AWSConfig   awsSDK.Config
	Profile     string
	AccountID   string
	Partition   string
	Region      string
	ActionRunID string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	// AutoApproveTeardownOnCancel allows actions with teardown prompts to
	// proceed without interactive confirmation when the action context is
	// canceled (for example, Esc in embedded TUI streaming mode).
	AutoApproveTeardownOnCancel bool
}

type Result struct {
	Status string
	Data   map[string]any
}

type Action interface {
	ID() string
	Title() string
	Description() string
	Risk() RiskLevel
	Applicable(node graph.ResourceNode) bool
	Execute(ctx context.Context, exec ExecContext, node graph.ResourceNode) (Result, error)
}

// TerminalAction is an optional capability for actions that need to take over
// the user's terminal (for example, interactive shells). Non-terminal actions
// do not need to implement this interface.
type TerminalAction interface {
	Action
	ExecuteTerminal(ctx context.Context, exec ExecContext, node graph.ResourceNode) (Result, error)
}

// EmbeddedTUIAction is an optional capability for terminal actions that can be
// executed inside the awscope TUI using stdin/stdout streaming.
type EmbeddedTUIAction interface {
	TerminalAction
	PreferEmbeddedTUI() bool
}
