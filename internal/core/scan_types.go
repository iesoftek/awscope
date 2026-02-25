package core

import (
	"context"

	"awscope/internal/aws"
	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type App struct {
	store              *store.Store
	loader             awsLoader
	listServiceCostAgg func(ctx context.Context, accountID string, regions []string) ([]store.CostAgg, error)
}

func New(st *store.Store) *App {
	return &App{
		store:  st,
		loader: aws.NewLoader(),
		listServiceCostAgg: func(ctx context.Context, accountID string, regions []string) ([]store.CostAgg, error) {
			return st.ListServiceCostAggByRegions(ctx, accountID, regions)
		},
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

type ScanServiceCount struct {
	Service   string
	Resources int
}

type ScanRegionCount struct {
	Region    string
	Resources int
	SharePct  float64
}

type ScanPricingSummary struct {
	KnownUSD     float64
	UnknownCount int
	Currency     string
}

type ScanSummary struct {
	ServiceCounts    []ScanServiceCount
	ImportantRegions []ScanRegionCount
	Pricing          ScanPricingSummary
}

type ScanResult struct {
	Resources int
	Edges     int
	AccountID string
	Partition string

	// StepFailures contains best-effort step errors (e.g. AccessDenied) encountered during scan.
	// The scan still completes successfully when these are present.
	StepFailures []ScanStepFailure

	Summary ScanSummary
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
