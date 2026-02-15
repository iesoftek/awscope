package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkiam "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newIAM func(cfg awsSDK.Config) iamAPI
}

func New() *Provider {
	return &Provider{
		newIAM: func(cfg awsSDK.Config) iamAPI { return sdkiam.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "iam" }
func (p *Provider) DisplayName() string { return "IAM" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeGlobal
}

type iamAPI interface {
	ListRoles(ctx context.Context, params *sdkiam.ListRolesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListRolesOutput, error)
	ListAttachedRolePolicies(ctx context.Context, params *sdkiam.ListAttachedRolePoliciesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListAttachedRolePoliciesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("iam provider requires account identity")
	}

	// IAM is global, but the SDK still expects a region in config for endpoint resolution.
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	api := p.newIAM(cfg)
	now := time.Now().UTC()

	var (
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
	)

	roles, err := listAllRoles(ctx, api)
	if err != nil {
		return providers.ListResult{}, err
	}
	for _, r := range roles {
		roleNode := normalizeRole(req.Partition, req.AccountID, r, now)
		nodes = append(nodes, roleNode)

		attached, err := listAllAttachedRolePolicies(ctx, api, awsToString(r.RoleName))
		if err != nil {
			return providers.ListResult{}, err
		}
		for _, ap := range attached {
			pArn := awsToString(ap.PolicyArn)
			if pArn == "" {
				continue
			}
			pKey := graph.EncodeResourceKey(req.Partition, req.AccountID, "global", "iam:policy", pArn)
			nodes = append(nodes, stubNode(pKey, "iam", "iam:policy", shortArn(pArn), now, "iam"))
			edges = append(edges, graph.RelationshipEdge{
				From:        roleNode.Key,
				To:          pKey,
				Kind:        "attached-policy",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
	}

	return providers.ListResult{Nodes: nodes, Edges: edges}, nil
}

func listAllRoles(ctx context.Context, api iamAPI) ([]types.Role, error) {
	var out []types.Role
	var marker *string
	for {
		resp, err := api.ListRoles(ctx, &sdkiam.ListRolesInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Roles...)
		if !resp.IsTruncated {
			break
		}
		marker = resp.Marker
		if marker == nil || *marker == "" {
			break
		}
	}
	return out, nil
}

func listAllAttachedRolePolicies(ctx context.Context, api iamAPI, roleName string) ([]types.AttachedPolicy, error) {
	var out []types.AttachedPolicy
	var marker *string
	for {
		resp, err := api.ListAttachedRolePolicies(ctx, &sdkiam.ListAttachedRolePoliciesInput{
			RoleName: &roleName,
			Marker:   marker,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.AttachedPolicies...)
		if !resp.IsTruncated {
			break
		}
		marker = resp.Marker
		if marker == nil || *marker == "" {
			break
		}
	}
	return out, nil
}

func normalizeRole(partition, accountID string, r types.Role, now time.Time) graph.ResourceNode {
	arn := awsToString(r.Arn)
	display := awsToString(r.RoleName)
	if display == "" {
		display = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", arn)
	raw, _ := json.Marshal(r)

	attrs := map[string]any{
		"roleId": awsToString(r.RoleId),
		"path":   awsToString(r.Path),
	}
	if r.CreateDate != nil {
		attrs["created_at"] = r.CreateDate.UTC().Format("2006-01-02 15:04")
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "iam",
		Type:        "iam:role",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "iam",
	}
}

func shortArn(arn string) string {
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		Arn:         "",
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
