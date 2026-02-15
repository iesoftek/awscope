package providers

import (
	"context"

	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
)

type ScopeKind int

const (
	ScopeRegional ScopeKind = iota
	ScopeGlobal
	// ScopeAccount providers are called once per scan (like global), but may emit resources
	// across many regions. The core scan passes the user-selected regions so providers can
	// filter their output.
	ScopeAccount
)

type ListRequest struct {
	Profile   string
	AccountID string
	Partition string
	Regions   []string

	// Filter/search will be added at the core layer; providers can ignore it for v0.
}

type ListResult struct {
	Nodes []graph.ResourceNode
	Edges []graph.RelationshipEdge
}

type Provider interface {
	ID() string
	DisplayName() string
	Scope() ScopeKind

	// List returns normalized nodes and relationship edges for the request.
	// For regional providers, it should honor req.Regions and return nodes scoped to those regions.
	// For global providers, it should return nodes with region="global".
	List(ctx context.Context, cfg awsSDK.Config, req ListRequest) (ListResult, error)
}
