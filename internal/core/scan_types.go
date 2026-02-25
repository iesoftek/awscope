package core

import (
	"context"

	"awscope/internal/aws"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type App struct {
	store  *store.Store
	loader awsLoader
}

func New(st *store.Store) *App {
	return &App{
		store:  st,
		loader: aws.NewLoader(),
	}
}

type awsLoader interface {
	Load(ctx context.Context, profile, region string) (awsSDK.Config, aws.Identity, error)
}

type ScanOptions struct {
	Profile     string
	Regions     []string
	ProviderIDs []string

	MaxConcurrency      int
	ResolverConcurrency int
}

type ScanResult struct {
	Resources int
	Edges     int
	AccountID string
	Partition string

	// StepFailures contains best-effort step errors (e.g. AccessDenied) encountered during scan.
	// The scan still completes successfully when these are present.
	StepFailures []ScanStepFailure
}

type ScanStepFailure struct {
	Phase      ScanProgressPhase
	ProviderID string
	Region     string
	Error      string
}

type tgRef struct {
	region string
	arn    string
}

func (a *App) Scan(ctx context.Context, opts ScanOptions) (ScanResult, error) {
	return a.ScanWithProgress(ctx, opts, nil)
}
