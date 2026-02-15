package kms

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
	sdkkms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newKMS func(cfg awsSDK.Config) kmsAPI
}

func New() *Provider {
	return &Provider{
		newKMS: func(cfg awsSDK.Config) kmsAPI { return sdkkms.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "kms" }
func (p *Provider) DisplayName() string { return "KMS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type kmsAPI interface {
	ListKeys(ctx context.Context, params *sdkkms.ListKeysInput, optFns ...func(*sdkkms.Options)) (*sdkkms.ListKeysOutput, error)
	DescribeKey(ctx context.Context, params *sdkkms.DescribeKeyInput, optFns ...func(*sdkkms.Options)) (*sdkkms.DescribeKeyOutput, error)
	ListAliases(ctx context.Context, params *sdkkms.ListAliasesInput, optFns ...func(*sdkkms.Options)) (*sdkkms.ListAliasesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("kms provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("kms provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newKMS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api kmsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	keyIDToPrimary := map[string]string{}

	// Keys.
	var marker *string
	for {
		resp, err := api.ListKeys(ctx, &sdkkms.ListKeysInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, k := range resp.Keys {
			keyID := awsToString(k.KeyId)
			if keyID == "" {
				continue
			}
			desc, err := api.DescribeKey(ctx, &sdkkms.DescribeKeyInput{KeyId: &keyID})
			if err != nil {
				return nil, nil, err
			}
			if desc.KeyMetadata == nil {
				continue
			}
			primary := awsToString(desc.KeyMetadata.Arn)
			if primary == "" {
				primary = awsToString(desc.KeyMetadata.KeyId)
			}
			if primary != "" {
				keyIDToPrimary[keyID] = primary
			}
			nodes = append(nodes, normalizeKey(partition, accountID, region, *desc.KeyMetadata, now))
		}
		if resp.Truncated && resp.NextMarker != nil && *resp.NextMarker != "" {
			marker = resp.NextMarker
			continue
		}
		break
	}

	// Aliases.
	marker = nil
	for {
		resp, err := api.ListAliases(ctx, &sdkkms.ListAliasesInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, a := range resp.Aliases {
			an := awsToString(a.AliasName)
			aa := awsToString(a.AliasArn)
			if aa == "" {
				// Best-effort build.
				if an != "" && strings.HasPrefix(an, "alias/") {
					aa = fmt.Sprintf("arn:%s:kms:%s:%s:%s", partition, region, accountID, an)
				}
			}
			if aa == "" {
				continue
			}
			aliasNode := normalizeAlias(partition, accountID, region, an, aa, now)
			nodes = append(nodes, aliasNode)
			if tid := awsToString(a.TargetKeyId); tid != "" {
				primary := keyIDToPrimary[tid]
				if primary == "" {
					primary = tid
				}
				keyKey := graph.EncodeResourceKey(partition, accountID, region, "kms:key", primary)
				edges = append(edges, graph.RelationshipEdge{
					From:        aliasNode.Key,
					To:          keyKey,
					Kind:        "attached-to",
					Meta:        map[string]any{"direct": true},
					CollectedAt: now,
				})
			}
		}
		if resp.Truncated && resp.NextMarker != nil && *resp.NextMarker != "" {
			marker = resp.NextMarker
			continue
		}
		break
	}

	return nodes, edges, nil
}

func normalizeKey(partition, accountID, region string, md types.KeyMetadata, now time.Time) graph.ResourceNode {
	arn := awsToString(md.Arn)
	keyID := awsToString(md.KeyId)
	primary := arn
	if primary == "" {
		primary = keyID
	}
	display := keyID
	if display == "" {
		display = primary
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "kms:key", primary)
	raw, _ := json.Marshal(md)
	attrs := map[string]any{
		"keyId":    keyID,
		"keyState": string(md.KeyState),
		"manager":  string(md.KeyManager),
		"origin":   string(md.Origin),
	}
	if md.CreationDate != nil && !md.CreationDate.IsZero() {
		attrs["created_at"] = md.CreationDate.UTC().Format("2006-01-02 15:04")
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "kms",
		Type:        "kms:key",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "kms",
	}
}

func normalizeAlias(partition, accountID, region, name, arn string, now time.Time) graph.ResourceNode {
	if name == "" {
		name = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "kms:alias", arn)
	raw, _ := json.Marshal(map[string]any{"aliasName": name, "aliasArn": arn})
	return graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "kms",
		Type:        "kms:alias",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  map[string]any{},
		Raw:         raw,
		CollectedAt: now,
		Source:      "kms",
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
