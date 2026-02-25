package identitycenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkidentitystore "github.com/aws/aws-sdk-go-v2/service/identitystore"
	isTypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	sdkssoadmin "github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoTypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSSO func(cfg awsSDK.Config) ssoAdminAPI
	newIDS func(cfg awsSDK.Config) identityStoreAPI
}

func New() *Provider {
	return &Provider{
		newSSO: func(cfg awsSDK.Config) ssoAdminAPI { return sdkssoadmin.NewFromConfig(cfg) },
		newIDS: func(cfg awsSDK.Config) identityStoreAPI { return sdkidentitystore.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "identitycenter" }
func (p *Provider) DisplayName() string { return "Identity Center" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeAccount
}

type ssoAdminAPI interface {
	ListInstances(ctx context.Context, params *sdkssoadmin.ListInstancesInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListInstancesOutput, error)
	ListPermissionSets(ctx context.Context, params *sdkssoadmin.ListPermissionSetsInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListPermissionSetsOutput, error)
	DescribePermissionSet(ctx context.Context, params *sdkssoadmin.DescribePermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.DescribePermissionSetOutput, error)
	ListManagedPoliciesInPermissionSet(ctx context.Context, params *sdkssoadmin.ListManagedPoliciesInPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListManagedPoliciesInPermissionSetOutput, error)
	ListCustomerManagedPolicyReferencesInPermissionSet(ctx context.Context, params *sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput, error)
	ListAccountsForProvisionedPermissionSet(ctx context.Context, params *sdkssoadmin.ListAccountsForProvisionedPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListAccountsForProvisionedPermissionSetOutput, error)
	ListAccountAssignments(ctx context.Context, params *sdkssoadmin.ListAccountAssignmentsInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListAccountAssignmentsOutput, error)
}

type identityStoreAPI interface {
	ListUsers(ctx context.Context, params *sdkidentitystore.ListUsersInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListUsersOutput, error)
	ListGroups(ctx context.Context, params *sdkidentitystore.ListGroupsInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListGroupsOutput, error)
	ListGroupMemberships(ctx context.Context, params *sdkidentitystore.ListGroupMembershipsInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListGroupMembershipsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("identitycenter provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("identitycenter provider requires account identity")
	}

	now := time.Now().UTC()
	nodesByKey := map[graph.ResourceKey]graph.ResourceNode{}
	addNode := func(n graph.ResourceNode) {
		if n.Key == "" {
			return
		}
		if _, ok := nodesByKey[n.Key]; ok {
			return
		}
		nodesByKey[n.Key] = n
	}
	var edges []graph.RelationshipEdge

	type discovered struct {
		region   string
		instance ssoTypes.InstanceMetadata
	}
	var instances []discovered
	var sawSuccess bool
	var lastErr error
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		sso := p.newSSO(c)
		out, err := sso.ListInstances(ctx, &sdkssoadmin.ListInstancesInput{})
		if err != nil {
			lastErr = err
			continue
		}
		sawSuccess = true
		for _, inst := range out.Instances {
			instances = append(instances, discovered{region: region, instance: inst})
		}
	}

	if len(instances) == 0 {
		if !sawSuccess && lastErr != nil {
			return providers.ListResult{}, lastErr
		}
		return providers.ListResult{}, nil
	}

	for _, d := range instances {
		inst := d.instance
		instanceArn := strings.TrimSpace(awsToString(inst.InstanceArn))
		identityStoreID := strings.TrimSpace(awsToString(inst.IdentityStoreId))
		if instanceArn == "" || identityStoreID == "" {
			continue
		}

		instanceKey := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:instance", instanceArn)
		instAttrs := map[string]any{
			"identityStoreId": identityStoreID,
			"ownerAccountId":  strings.TrimSpace(awsToString(inst.OwnerAccountId)),
			"name":            strings.TrimSpace(awsToString(inst.Name)),
			"status":          strings.TrimSpace(string(inst.Status)),
		}
		raw, _ := json.Marshal(inst)
		addNode(graph.ResourceNode{
			Key:         instanceKey,
			DisplayName: firstNonEmpty(strings.TrimSpace(awsToString(inst.Name)), instanceArn),
			Service:     "identitycenter",
			Type:        "identitycenter:instance",
			Arn:         instanceArn,
			PrimaryID:   instanceArn,
			Attributes:  instAttrs,
			Raw:         raw,
			CollectedAt: now,
			Source:      "identitycenter",
		})

		// Identity Store users/groups and memberships (best-effort).
		c := cfg
		c.Region = d.region
		ids := p.newIDS(c)
		userByID := map[string]graph.ResourceKey{}
		groupByID := map[string]graph.ResourceKey{}

		var usersToken *string
		for {
			out, err := ids.ListUsers(ctx, &sdkidentitystore.ListUsersInput{
				IdentityStoreId: &identityStoreID,
				NextToken:       usersToken,
			})
			if err != nil {
				break
			}
			for _, u := range out.Users {
				uid := strings.TrimSpace(awsToString(u.UserId))
				if uid == "" {
					continue
				}
				key := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:user", uid)
				userByID[uid] = key
				raw, _ := json.Marshal(u)
				display := firstNonEmpty(strings.TrimSpace(awsToString(u.DisplayName)), strings.TrimSpace(awsToString(u.UserName)))
				addNode(graph.ResourceNode{
					Key:         key,
					DisplayName: firstNonEmpty(display, uid),
					Service:     "identitycenter",
					Type:        "identitycenter:user",
					PrimaryID:   uid,
					Attributes: map[string]any{
						"userName": strings.TrimSpace(awsToString(u.UserName)),
						"status":   strings.TrimSpace(string(u.UserStatus)),
					},
					Raw:         raw,
					CollectedAt: now,
					Source:      "identitycenter",
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			usersToken = out.NextToken
		}

		var groupsToken *string
		for {
			out, err := ids.ListGroups(ctx, &sdkidentitystore.ListGroupsInput{
				IdentityStoreId: &identityStoreID,
				NextToken:       groupsToken,
			})
			if err != nil {
				break
			}
			for _, g := range out.Groups {
				gid := strings.TrimSpace(awsToString(g.GroupId))
				if gid == "" {
					continue
				}
				key := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:group", gid)
				groupByID[gid] = key
				raw, _ := json.Marshal(g)
				addNode(graph.ResourceNode{
					Key:         key,
					DisplayName: firstNonEmpty(strings.TrimSpace(awsToString(g.DisplayName)), gid),
					Service:     "identitycenter",
					Type:        "identitycenter:group",
					PrimaryID:   gid,
					Attributes: map[string]any{
						"description": strings.TrimSpace(awsToString(g.Description)),
					},
					Raw:         raw,
					CollectedAt: now,
					Source:      "identitycenter",
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			groupsToken = out.NextToken
		}

		groupIDs := make([]string, 0, len(groupByID))
		for gid := range groupByID {
			groupIDs = append(groupIDs, gid)
		}
		sort.Strings(groupIDs)
		for _, gid := range groupIDs {
			groupKey := groupByID[gid]
			var memToken *string
			for {
				out, err := ids.ListGroupMemberships(ctx, &sdkidentitystore.ListGroupMembershipsInput{
					IdentityStoreId: &identityStoreID,
					GroupId:         &gid,
					NextToken:       memToken,
				})
				if err != nil {
					break
				}
				for _, m := range out.GroupMemberships {
					uid := groupMemberUserID(m.MemberId)
					if uid == "" {
						continue
					}
					userKey, ok := userByID[uid]
					if !ok {
						userKey = graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:user", uid)
					}
					edges = append(edges, graph.RelationshipEdge{
						From:        userKey,
						To:          groupKey,
						Kind:        "member-of",
						Meta:        map[string]any{"direct": true},
						CollectedAt: now,
					})
				}
				if out.NextToken == nil || *out.NextToken == "" {
					break
				}
				memToken = out.NextToken
			}
		}

		// Permission sets and account assignments.
		sso := p.newSSO(c)
		var psToken *string
		for {
			psOut, err := sso.ListPermissionSets(ctx, &sdkssoadmin.ListPermissionSetsInput{
				InstanceArn: &instanceArn,
				NextToken:   psToken,
			})
			if err != nil {
				if isAPIErrorCode(err, "AccessDeniedException") || isAPIErrorCode(err, "AccessDenied") {
					break
				}
				return providers.ListResult{}, err
			}

			for _, psArn := range psOut.PermissionSets {
				psArn = strings.TrimSpace(psArn)
				if psArn == "" {
					continue
				}
				psKey := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:permission-set", psArn)
				psName := psArn
				psAttrs := map[string]any{"instanceArn": instanceArn}
				if desc, err := sso.DescribePermissionSet(ctx, &sdkssoadmin.DescribePermissionSetInput{
					InstanceArn:      &instanceArn,
					PermissionSetArn: &psArn,
				}); err == nil && desc.PermissionSet != nil {
					ps := desc.PermissionSet
					if strings.TrimSpace(awsToString(ps.Name)) != "" {
						psName = strings.TrimSpace(awsToString(ps.Name))
					}
					psAttrs["description"] = strings.TrimSpace(awsToString(ps.Description))
					psAttrs["sessionDuration"] = strings.TrimSpace(awsToString(ps.SessionDuration))
					psAttrs["relayState"] = strings.TrimSpace(awsToString(ps.RelayState))
				}

				if mpo, err := sso.ListManagedPoliciesInPermissionSet(ctx, &sdkssoadmin.ListManagedPoliciesInPermissionSetInput{
					InstanceArn:      &instanceArn,
					PermissionSetArn: &psArn,
				}); err == nil {
					psAttrs["managedPolicies"] = len(mpo.AttachedManagedPolicies)
				}
				if cpo, err := sso.ListCustomerManagedPolicyReferencesInPermissionSet(ctx, &sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput{
					InstanceArn:      &instanceArn,
					PermissionSetArn: &psArn,
				}); err == nil {
					psAttrs["customerManagedPolicies"] = len(cpo.CustomerManagedPolicyReferences)
				}
				addNode(graph.ResourceNode{
					Key:         psKey,
					DisplayName: psName,
					Service:     "identitycenter",
					Type:        "identitycenter:permission-set",
					Arn:         psArn,
					PrimaryID:   psArn,
					Attributes:  psAttrs,
					Raw:         []byte(`{}`),
					CollectedAt: now,
					Source:      "identitycenter",
				})
				edges = append(edges, graph.RelationshipEdge{From: instanceKey, To: psKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})

				var acctToken *string
				for {
					acctOut, err := sso.ListAccountsForProvisionedPermissionSet(ctx, &sdkssoadmin.ListAccountsForProvisionedPermissionSetInput{
						InstanceArn:      &instanceArn,
						PermissionSetArn: &psArn,
						NextToken:        acctToken,
					})
					if err != nil {
						break
					}
					for _, accountID := range acctOut.AccountIds {
						accountID = strings.TrimSpace(accountID)
						if accountID == "" {
							continue
						}
						var asnToken *string
						for {
							asnOut, err := sso.ListAccountAssignments(ctx, &sdkssoadmin.ListAccountAssignmentsInput{
								InstanceArn:      &instanceArn,
								PermissionSetArn: &psArn,
								AccountId:        &accountID,
								NextToken:        asnToken,
							})
							if err != nil {
								break
							}
							for _, asn := range asnOut.AccountAssignments {
								principalID := strings.TrimSpace(awsToString(asn.PrincipalId))
								principalType := strings.TrimSpace(string(asn.PrincipalType))
								assignPrimary := strings.Join([]string{instanceArn, accountID, psArn, principalType, principalID}, "|")
								assignKey := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, "identitycenter:assignment", assignPrimary)

								addNode(graph.ResourceNode{
									Key:         assignKey,
									DisplayName: firstNonEmpty(principalID, assignPrimary),
									Service:     "identitycenter",
									Type:        "identitycenter:assignment",
									PrimaryID:   assignPrimary,
									Attributes: map[string]any{
										"account_id":     accountID,
										"instance_arn":   instanceArn,
										"permission_set": psArn,
										"principal_id":   principalID,
										"principal_type": principalType,
									},
									Raw:         []byte(`{}`),
									CollectedAt: now,
									Source:      "identitycenter",
								})
								edges = append(edges, graph.RelationshipEdge{From: instanceKey, To: assignKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
								edges = append(edges, graph.RelationshipEdge{From: assignKey, To: psKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})

								if principalID != "" {
									targetType := "identitycenter:user"
									if strings.EqualFold(principalType, string(ssoTypes.PrincipalTypeGroup)) {
										targetType = "identitycenter:group"
									}
									targetKey := graph.EncodeResourceKey(req.Partition, req.AccountID, d.region, targetType, principalID)
									edges = append(edges, graph.RelationshipEdge{From: assignKey, To: targetKey, Kind: "belongs-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
								}
							}
							if asnOut.NextToken == nil || *asnOut.NextToken == "" {
								break
							}
							asnToken = asnOut.NextToken
						}
					}
					if acctOut.NextToken == nil || *acctOut.NextToken == "" {
						break
					}
					acctToken = acctOut.NextToken
				}
			}
			if psOut.NextToken == nil || *psOut.NextToken == "" {
				break
			}
			psToken = psOut.NextToken
		}
	}

	nodes := make([]graph.ResourceNode, 0, len(nodesByKey))
	for _, n := range nodesByKey {
		nodes = append(nodes, n)
	}
	return providers.ListResult{Nodes: nodes, Edges: edges}, nil
}

func groupMemberUserID(member isTypes.MemberId) string {
	switch v := member.(type) {
	case *isTypes.MemberIdMemberUserId:
		return strings.TrimSpace(v.Value)
	default:
		return ""
	}
}

func isAPIErrorCode(err error, code string) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ae.ErrorCode()), strings.TrimSpace(code))
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}
