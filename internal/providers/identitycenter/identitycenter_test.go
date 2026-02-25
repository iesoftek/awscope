package identitycenter

import (
	"context"
	"testing"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkidentitystore "github.com/aws/aws-sdk-go-v2/service/identitystore"
	isTypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	sdkssoadmin "github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoTypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
)

type fakeSSO struct{}

func (fakeSSO) ListInstances(ctx context.Context, params *sdkssoadmin.ListInstancesInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListInstancesOutput, error) {
	instArn := "arn:aws:sso:::instance/ssoins-1"
	storeID := "d-1234567890"
	return &sdkssoadmin.ListInstancesOutput{
		Instances: []ssoTypes.InstanceMetadata{
			{InstanceArn: &instArn, IdentityStoreId: &storeID, Name: awsSDK.String("main"), Status: ssoTypes.InstanceStatusActive},
		},
	}, nil
}
func (fakeSSO) ListPermissionSets(ctx context.Context, params *sdkssoadmin.ListPermissionSetsInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListPermissionSetsOutput, error) {
	ps := "arn:aws:sso:::permissionSet/ssoins-1/ps-1"
	return &sdkssoadmin.ListPermissionSetsOutput{PermissionSets: []string{ps}}, nil
}
func (fakeSSO) DescribePermissionSet(ctx context.Context, params *sdkssoadmin.DescribePermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.DescribePermissionSetOutput, error) {
	name := "Admin"
	return &sdkssoadmin.DescribePermissionSetOutput{PermissionSet: &ssoTypes.PermissionSet{Name: &name}}, nil
}
func (fakeSSO) ListManagedPoliciesInPermissionSet(ctx context.Context, params *sdkssoadmin.ListManagedPoliciesInPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListManagedPoliciesInPermissionSetOutput, error) {
	return &sdkssoadmin.ListManagedPoliciesInPermissionSetOutput{}, nil
}
func (fakeSSO) ListCustomerManagedPolicyReferencesInPermissionSet(ctx context.Context, params *sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput, error) {
	return &sdkssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput{}, nil
}
func (fakeSSO) ListAccountsForProvisionedPermissionSet(ctx context.Context, params *sdkssoadmin.ListAccountsForProvisionedPermissionSetInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListAccountsForProvisionedPermissionSetOutput, error) {
	return &sdkssoadmin.ListAccountsForProvisionedPermissionSetOutput{AccountIds: []string{"123456789012"}}, nil
}
func (fakeSSO) ListAccountAssignments(ctx context.Context, params *sdkssoadmin.ListAccountAssignmentsInput, optFns ...func(*sdkssoadmin.Options)) (*sdkssoadmin.ListAccountAssignmentsOutput, error) {
	pid := "u-1"
	return &sdkssoadmin.ListAccountAssignmentsOutput{
		AccountAssignments: []ssoTypes.AccountAssignment{
			{PrincipalId: &pid, PrincipalType: ssoTypes.PrincipalTypeUser},
		},
	}, nil
}

type fakeIDS struct{}

func (fakeIDS) ListUsers(ctx context.Context, params *sdkidentitystore.ListUsersInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListUsersOutput, error) {
	uid := "u-1"
	name := "alice"
	return &sdkidentitystore.ListUsersOutput{Users: []isTypes.User{{UserId: &uid, UserName: &name}}}, nil
}
func (fakeIDS) ListGroups(ctx context.Context, params *sdkidentitystore.ListGroupsInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListGroupsOutput, error) {
	gid := "g-1"
	name := "admins"
	return &sdkidentitystore.ListGroupsOutput{Groups: []isTypes.Group{{GroupId: &gid, DisplayName: &name}}}, nil
}
func (fakeIDS) ListGroupMemberships(ctx context.Context, params *sdkidentitystore.ListGroupMembershipsInput, optFns ...func(*sdkidentitystore.Options)) (*sdkidentitystore.ListGroupMembershipsOutput, error) {
	return &sdkidentitystore.ListGroupMembershipsOutput{
		GroupMemberships: []isTypes.GroupMembership{
			{MemberId: &isTypes.MemberIdMemberUserId{Value: "u-1"}},
		},
	}, nil
}

func TestProvider_List_EmitsIdentityCenterGraph(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newSSO = func(cfg awsSDK.Config) ssoAdminAPI { return fakeSSO{} }
	p.newIDS = func(cfg awsSDK.Config) identityStoreAPI { return fakeIDS{} }

	res, err := p.List(ctx, awsSDK.Config{Region: "us-east-1"}, providers.ListRequest{
		AccountID: "123456789012",
		Partition: "aws",
		Regions:   []string{"us-east-1"},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
}
