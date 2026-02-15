package actions

import (
	"context"

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
