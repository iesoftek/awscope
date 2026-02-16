package iam

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkiam "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestNormalizeRole(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:iam::123456789012:role/MyRole"
	name := "MyRole"
	roleID := "AROAXXXXX"
	path := "/"

	r := types.Role{
		Arn:      &arn,
		RoleName: &name,
		RoleId:   &roleID,
		Path:     &path,
	}

	n := normalizeRole("aws", "123456789012", r, now)
	if n.DisplayName != "MyRole" {
		t.Fatalf("display: got %q", n.DisplayName)
	}
	if n.Type != "iam:role" || n.Service != "iam" {
		t.Fatalf("type/service: %#v", n)
	}
}

type fakeIAM struct {
	users         []types.User
	groups        []types.Group
	groupsForUser map[string][]types.Group
	keysForUser   map[string][]types.AccessKeyMetadata
	lastUsed      map[string]types.AccessKeyLastUsed
	credCSV       []byte
}

func (f *fakeIAM) GenerateCredentialReport(ctx context.Context, params *sdkiam.GenerateCredentialReportInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GenerateCredentialReportOutput, error) {
	state := types.ReportStateTypeComplete
	return &sdkiam.GenerateCredentialReportOutput{State: state}, nil
}
func (f *fakeIAM) GetCredentialReport(ctx context.Context, params *sdkiam.GetCredentialReportInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GetCredentialReportOutput, error) {
	return &sdkiam.GetCredentialReportOutput{Content: f.credCSV}, nil
}
func (f *fakeIAM) ListRoles(ctx context.Context, params *sdkiam.ListRolesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListRolesOutput, error) {
	return &sdkiam.ListRolesOutput{Roles: nil, IsTruncated: false}, nil
}
func (f *fakeIAM) ListAttachedRolePolicies(ctx context.Context, params *sdkiam.ListAttachedRolePoliciesInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListAttachedRolePoliciesOutput, error) {
	return &sdkiam.ListAttachedRolePoliciesOutput{AttachedPolicies: nil, IsTruncated: false}, nil
}
func (f *fakeIAM) ListUsers(ctx context.Context, params *sdkiam.ListUsersInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListUsersOutput, error) {
	return &sdkiam.ListUsersOutput{Users: f.users, IsTruncated: false}, nil
}
func (f *fakeIAM) ListGroups(ctx context.Context, params *sdkiam.ListGroupsInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListGroupsOutput, error) {
	return &sdkiam.ListGroupsOutput{Groups: f.groups, IsTruncated: false}, nil
}
func (f *fakeIAM) ListGroupsForUser(ctx context.Context, params *sdkiam.ListGroupsForUserInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListGroupsForUserOutput, error) {
	u := awsToString(params.UserName)
	return &sdkiam.ListGroupsForUserOutput{Groups: f.groupsForUser[u], IsTruncated: false}, nil
}
func (f *fakeIAM) ListAccessKeys(ctx context.Context, params *sdkiam.ListAccessKeysInput, optFns ...func(*sdkiam.Options)) (*sdkiam.ListAccessKeysOutput, error) {
	u := awsToString(params.UserName)
	return &sdkiam.ListAccessKeysOutput{AccessKeyMetadata: f.keysForUser[u], IsTruncated: false}, nil
}
func (f *fakeIAM) GetAccessKeyLastUsed(ctx context.Context, params *sdkiam.GetAccessKeyLastUsedInput, optFns ...func(*sdkiam.Options)) (*sdkiam.GetAccessKeyLastUsedOutput, error) {
	id := awsToString(params.AccessKeyId)
	lu := f.lastUsed[id]
	return &sdkiam.GetAccessKeyLastUsedOutput{AccessKeyLastUsed: &lu}, nil
}

func TestProvider_IAMUsersGroupsKeys_CredReportBacked(t *testing.T) {
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	userArn := "arn:aws:iam::123456789012:user/alice"
	userName := "alice"
	userID := "AIDAEXAMPLE"
	groupArn := "arn:aws:iam::123456789012:group/devs"
	groupName := "devs"
	groupID := "AGPAEXAMPLE"
	keyID := "AKIA1234567890EXAMPLE"

	u := types.User{Arn: &userArn, UserName: &userName, UserId: &userID, CreateDate: ptrTime(now.Add(-24 * time.Hour))}
	g := types.Group{Arn: &groupArn, GroupName: &groupName, GroupId: &groupID, CreateDate: ptrTime(now.Add(-48 * time.Hour))}
	km := types.AccessKeyMetadata{AccessKeyId: &keyID, Status: types.StatusTypeActive, CreateDate: ptrTime(now.Add(-72 * time.Hour))}

	lastUsedAt := now.Add(-1 * time.Hour)
	region := "us-east-1"
	svc := "ec2"
	lu := types.AccessKeyLastUsed{LastUsedDate: &lastUsedAt, Region: &region, ServiceName: &svc}

	cred := []byte("user,password_enabled,password_last_used,mfa_active\n" +
		"alice,true,2026-02-14T10:00:00+00:00,true\n")

	fake := &fakeIAM{
		users:         []types.User{u},
		groups:        []types.Group{g},
		groupsForUser: map[string][]types.Group{"alice": {g}},
		keysForUser:   map[string][]types.AccessKeyMetadata{"alice": {km}},
		lastUsed:      map[string]types.AccessKeyLastUsed{keyID: lu},
		credCSV:       cred,
	}

	p := &Provider{newIAM: func(cfg awsSDK.Config) iamAPI { return fake }}
	res, err := p.List(context.Background(), awsSDK.Config{Region: "us-east-1"}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"global"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var (
		foundUser  bool
		foundGroup bool
		foundKey   bool
	)
	for _, n := range res.Nodes {
		switch n.Type {
		case "iam:user":
			foundUser = true
			if n.DisplayName != "alice" {
				t.Fatalf("user display: %q", n.DisplayName)
			}
			if got, _ := n.Attributes["console_access"].(string); got != "console" {
				t.Fatalf("console_access: %v", n.Attributes["console_access"])
			}
			if got, _ := n.Attributes["password_last_used"].(string); got == "" {
				t.Fatalf("password_last_used missing")
			}
			if got, _ := n.Attributes["groups_count"].(int); got != 1 {
				// attributes are marshaled later; in provider they are ints
				t.Fatalf("groups_count: %v", n.Attributes["groups_count"])
			}
			if got, _ := n.Attributes["access_keys_count"].(int); got != 1 {
				t.Fatalf("access_keys_count: %v", n.Attributes["access_keys_count"])
			}
		case "iam:group":
			foundGroup = true
			if n.DisplayName != "devs" {
				t.Fatalf("group display: %q", n.DisplayName)
			}
		case "iam:access-key":
			foundKey = true
			if n.PrimaryID != keyID {
				t.Fatalf("key primary id: %q", n.PrimaryID)
			}
			if got, _ := n.Attributes["status"].(string); got != "Active" {
				t.Fatalf("key status: %v", n.Attributes["status"])
			}
			if got, ok := n.Attributes["age_days"].(int); !ok || got <= 0 {
				t.Fatalf("age_days: %v", n.Attributes["age_days"])
			}
			if got, _ := n.Attributes["last_used_region"].(string); got != "us-east-1" {
				t.Fatalf("last_used_region: %v", n.Attributes["last_used_region"])
			}
		}
	}
	if !foundUser || !foundGroup || !foundKey {
		t.Fatalf("missing nodes: user=%v group=%v key=%v", foundUser, foundGroup, foundKey)
	}

	var (
		hasMemberOf bool
		hasContains bool
	)
	for _, e := range res.Edges {
		if e.Kind == "member-of" {
			hasMemberOf = true
		}
		if e.Kind == "contains" {
			hasContains = true
		}
	}
	if !hasMemberOf || !hasContains {
		t.Fatalf("missing edges: member-of=%v contains=%v", hasMemberOf, hasContains)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
