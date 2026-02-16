package iam

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkiam "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
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
	GenerateCredentialReport(ctx context.Context, params *sdkiam.GenerateCredentialReportInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GenerateCredentialReportOutput, error)
	GetCredentialReport(ctx context.Context, params *sdkiam.GetCredentialReportInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GetCredentialReportOutput, error)

	ListRoles(ctx context.Context, params *sdkiam.ListRolesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListRolesOutput, error)
	ListAttachedRolePolicies(ctx context.Context, params *sdkiam.ListAttachedRolePoliciesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListAttachedRolePoliciesOutput, error)

	ListUsers(ctx context.Context, params *sdkiam.ListUsersInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListUsersOutput, error)

	ListGroups(ctx context.Context, params *sdkiam.ListGroupsInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListGroupsOutput, error)
	ListGroupsForUser(ctx context.Context, params *sdkiam.ListGroupsForUserInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListGroupsForUserOutput, error)

	ListAccessKeys(ctx context.Context, params *sdkiam.ListAccessKeysInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListAccessKeysOutput, error)
	GetAccessKeyLastUsed(ctx context.Context, params *sdkiam.GetAccessKeyLastUsedInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GetAccessKeyLastUsedOutput, error)
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

	roles, err := listAllRoles(ctx, api)
	if err != nil {
		return providers.ListResult{}, err
	}
	for _, r := range roles {
		roleNode := normalizeRole(req.Partition, req.AccountID, r, now)
		addNode(roleNode)

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
			addNode(stubNode(pKey, "iam", "iam:policy", shortArn(pArn), now, "iam"))
			edges = append(edges, graph.RelationshipEdge{
				From:        roleNode.Key,
				To:          pKey,
				Kind:        "attached-policy",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
	}

	credRows, credErr := fetchCredentialReport(ctx, api)
	if credErr != nil && !isIAMErrorCode(credErr, "AccessDenied") && !isIAMErrorCode(credErr, "AccessDeniedException") {
		// Best effort: do not fail IAM scan for credential report failures.
		credRows = nil
	}

	// Groups: best-effort
	groupNodes := map[string]graph.ResourceNode{} // group name -> node
	if groups, err := listAllGroups(ctx, api); err == nil {
		for _, g := range groups {
			gn := normalizeGroup(req.Partition, req.AccountID, g, now)
			groupNodes[awsToString(g.GroupName)] = gn
			addNode(gn)
		}
	}

	users, err := listAllUsers(ctx, api)
	if err != nil {
		return providers.ListResult{}, err
	}
	for _, u := range users {
		un := normalizeUserBase(req.Partition, req.AccountID, u, now)

		userName := awsToString(u.UserName)
		if userName == "" {
			// Unusual, but avoid crashing on empty usernames.
			userName = un.DisplayName
		}

		// Groups membership (best-effort).
		var groupNames []string
		if userName != "" {
			if gs, err := listAllGroupsForUser(ctx, api, userName); err == nil {
				for _, g := range gs {
					name := awsToString(g.GroupName)
					if name != "" {
						groupNames = append(groupNames, name)
					}
					gn, ok := groupNodes[name]
					if !ok {
						gn = normalizeGroup(req.Partition, req.AccountID, g, now)
						groupNodes[name] = gn
						addNode(gn)
					}
					edges = append(edges, graph.RelationshipEdge{
						From:        un.Key,
						To:          gn.Key,
						Kind:        "member-of",
						Meta:        map[string]any{},
						CollectedAt: now,
					})
				}
			}
		}
		sort.Strings(groupNames)

		// Access keys (best-effort).
		var (
			keySummaries []string
			keyCount     int
		)
		if userName != "" {
			if kms, err := listAllAccessKeys(ctx, api, userName); err == nil {
				for _, km := range kms {
					kn := normalizeAccessKey(req.Partition, req.AccountID, userName, km, now)
					addNode(kn)
					edges = append(edges, graph.RelationshipEdge{
						From:        un.Key,
						To:          kn.Key,
						Kind:        "contains",
						Meta:        map[string]any{},
						CollectedAt: now,
					})
					keyCount++

					// Best-effort: last used.
					if id := awsToString(km.AccessKeyId); id != "" {
						if lu, err := api.GetAccessKeyLastUsed(ctx, &sdkiam.GetAccessKeyLastUsedInput{AccessKeyId: &id}); err == nil && lu != nil {
							if lu.AccessKeyLastUsed.LastUsedDate != nil {
								kn.Attributes["last_used_at"] = lu.AccessKeyLastUsed.LastUsedDate.UTC().Format("2006-01-02 15:04")
							}
							if strings.TrimSpace(awsToString(lu.AccessKeyLastUsed.Region)) != "" {
								kn.Attributes["last_used_region"] = strings.TrimSpace(awsToString(lu.AccessKeyLastUsed.Region))
							}
							if strings.TrimSpace(awsToString(lu.AccessKeyLastUsed.ServiceName)) != "" {
								kn.Attributes["last_used_service"] = strings.TrimSpace(awsToString(lu.AccessKeyLastUsed.ServiceName))
							}
							// Update deduped node.
							nodesByKey[kn.Key] = kn
						}
					}

					// Summary string.
					suffix := shortenKeyID(awsToString(km.AccessKeyId))
					st := string(km.Status)
					age := ""
					if v, ok := kn.Attributes["age_days"].(int); ok {
						age = fmt.Sprintf("%dd", v)
					} else if v, ok := kn.Attributes["age_days"].(int64); ok {
						age = fmt.Sprintf("%dd", v)
					} else if v, ok := kn.Attributes["age_days"].(float64); ok {
						age = fmt.Sprintf("%dd", int(v))
					}
					if age != "" {
						keySummaries = append(keySummaries, fmt.Sprintf("%s(%s, age=%s)", suffix, st, age))
					} else {
						keySummaries = append(keySummaries, fmt.Sprintf("%s(%s)", suffix, st))
					}
				}
			}
		}
		sort.Strings(keySummaries)

		// Credential report data, if available.
		var (
			passwordEnabledSet bool
			passwordEnabled    bool
			passwordLastUsed   string
			mfaActiveSet       bool
			mfaActive          bool
		)
		if row, ok := credRows[userName]; ok {
			if v, ok := row["password_enabled"]; ok {
				passwordEnabledSet = true
				passwordEnabled = strings.EqualFold(strings.TrimSpace(v), "true")
			}
			if v, ok := row["password_last_used"]; ok {
				passwordLastUsed = normalizeReportTime(v)
			}
			if v, ok := row["mfa_active"]; ok {
				mfaActiveSet = true
				mfaActive = strings.EqualFold(strings.TrimSpace(v), "true")
			}
		}

		consoleAccess := "unknown"
		if passwordEnabledSet {
			if passwordEnabled {
				consoleAccess = "console"
			} else if keyCount > 0 {
				consoleAccess = "programmatic"
			}
		} else if keyCount > 0 {
			consoleAccess = "programmatic"
		}

		// Enrich user attributes.
		if passwordEnabledSet {
			un.Attributes["password_enabled"] = passwordEnabled
		}
		if strings.TrimSpace(passwordLastUsed) != "" {
			un.Attributes["password_last_used"] = passwordLastUsed
		} else if passwordEnabledSet {
			un.Attributes["password_last_used"] = "-"
		}
		if mfaActiveSet {
			un.Attributes["mfa_active"] = mfaActive
		}
		un.Attributes["console_access"] = consoleAccess
		un.Attributes["status"] = consoleAccess

		un.Attributes["groups_count"] = len(groupNames)
		if len(groupNames) > 0 {
			un.Attributes["groups"] = strings.Join(groupNames, ", ")
		}

		un.Attributes["access_keys_count"] = keyCount
		if len(keySummaries) > 0 {
			// Cap to keep details readable.
			if len(keySummaries) > 8 {
				un.Attributes["access_keys"] = strings.Join(keySummaries[:8], ", ") + fmt.Sprintf(" (+%d more)", len(keySummaries)-8)
			} else {
				un.Attributes["access_keys"] = strings.Join(keySummaries, ", ")
			}
		}

		addNode(un)
	}

	nodes := make([]graph.ResourceNode, 0, len(nodesByKey))
	for _, n := range nodesByKey {
		nodes = append(nodes, n)
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

func listAllUsers(ctx context.Context, api iamAPI) ([]types.User, error) {
	var out []types.User
	var marker *string
	for {
		resp, err := api.ListUsers(ctx, &sdkiam.ListUsersInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Users...)
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

func listAllGroups(ctx context.Context, api iamAPI) ([]types.Group, error) {
	var out []types.Group
	var marker *string
	for {
		resp, err := api.ListGroups(ctx, &sdkiam.ListGroupsInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Groups...)
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

func listAllGroupsForUser(ctx context.Context, api iamAPI, userName string) ([]types.Group, error) {
	var out []types.Group
	var marker *string
	for {
		resp, err := api.ListGroupsForUser(ctx, &sdkiam.ListGroupsForUserInput{
			UserName: &userName,
			Marker:   marker,
		})
		if err != nil {
			if isIAMErrorCode(err, "AccessDenied") || isIAMErrorCode(err, "AccessDeniedException") || isIAMErrorCode(err, "NoSuchEntity") {
				return nil, err
			}
			return nil, err
		}
		out = append(out, resp.Groups...)
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

func listAllAccessKeys(ctx context.Context, api iamAPI, userName string) ([]types.AccessKeyMetadata, error) {
	var out []types.AccessKeyMetadata
	var marker *string
	for {
		resp, err := api.ListAccessKeys(ctx, &sdkiam.ListAccessKeysInput{
			UserName: &userName,
			Marker:   marker,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.AccessKeyMetadata...)
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

func normalizeUserBase(partition, accountID string, u types.User, now time.Time) graph.ResourceNode {
	arn := awsToString(u.Arn)
	display := awsToString(u.UserName)
	if display == "" {
		display = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, "global", "iam:user", arn)
	raw, _ := json.Marshal(u)

	attrs := map[string]any{
		"userId": awsToString(u.UserId),
		"path":   awsToString(u.Path),
	}
	if u.CreateDate != nil {
		attrs["created_at"] = u.CreateDate.UTC().Format("2006-01-02 15:04")
	}

	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "iam",
		Type:        "iam:user",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "iam",
	}
}

func normalizeGroup(partition, accountID string, g types.Group, now time.Time) graph.ResourceNode {
	arn := awsToString(g.Arn)
	display := awsToString(g.GroupName)
	if display == "" {
		display = arn
	}
	key := graph.EncodeResourceKey(partition, accountID, "global", "iam:group", arn)
	raw, _ := json.Marshal(g)

	attrs := map[string]any{
		"groupId": awsToString(g.GroupId),
		"path":    awsToString(g.Path),
	}
	if g.CreateDate != nil {
		attrs["created_at"] = g.CreateDate.UTC().Format("2006-01-02 15:04")
	}

	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "iam",
		Type:        "iam:group",
		Arn:         arn,
		PrimaryID:   arn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "iam",
	}
}

func normalizeAccessKey(partition, accountID string, userName string, k types.AccessKeyMetadata, now time.Time) graph.ResourceNode {
	id := awsToString(k.AccessKeyId)
	// No ARN for access keys.
	key := graph.EncodeResourceKey(partition, accountID, "global", "iam:access-key", id)
	raw, _ := json.Marshal(k)

	display := strings.TrimSpace(userName)
	if display == "" {
		display = "access-key"
	}
	display = display + " / " + shortenKeyID(id)

	attrs := map[string]any{
		"userName": userName,
		"status":   string(k.Status),
	}
	if k.CreateDate != nil {
		attrs["created_at"] = k.CreateDate.UTC().Format("2006-01-02 15:04")
		age := int(now.Sub(k.CreateDate.UTC()).Hours() / 24)
		if age < 0 {
			age = 0
		}
		attrs["age_days"] = age
	}
	attrs["last_used_at"] = "-"

	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "iam",
		Type:        "iam:access-key",
		Arn:         "",
		PrimaryID:   id,
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

func shortenKeyID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if len(id) <= 8 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
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

func fetchCredentialReport(ctx context.Context, api iamAPI) (map[string]map[string]string, error) {
	// Generate may return "COMPLETE" immediately, but often is asynchronous.
	for i := 0; i < 20; i++ {
		resp, err := api.GenerateCredentialReport(ctx, &sdkiam.GenerateCredentialReportInput{})
		if err != nil {
			return nil, err
		}
		if resp != nil && resp.State == types.ReportStateTypeComplete {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	rep, err := api.GetCredentialReport(ctx, &sdkiam.GetCredentialReportInput{})
	if err != nil {
		return nil, err
	}
	if rep == nil || len(rep.Content) == 0 {
		return nil, fmt.Errorf("empty credential report")
	}
	return parseCredentialReportCSV(rep.Content)
}

func parseCredentialReportCSV(b []byte) (map[string]map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}

	out := map[string]map[string]string{}
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		row := map[string]string{}
		for i := 0; i < len(rec) && i < len(header); i++ {
			row[header[i]] = strings.TrimSpace(rec[i])
		}
		user := strings.TrimSpace(row["user"])
		if user == "" {
			continue
		}
		out[user] = row
	}
	return out, nil
}

func normalizeReportTime(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	l := strings.ToLower(v)
	switch l {
	case "not_supported", "n/a", "no_information":
		return "-"
	}
	return v
}

func isIAMErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == code
	}
	return false
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
