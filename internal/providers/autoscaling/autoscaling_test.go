package autoscaling

import (
	"context"
	"strings"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkasg "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

type fakeASGAPI struct{}

func (fakeASGAPI) DescribeAutoScalingGroups(ctx context.Context, params *sdkasg.DescribeAutoScalingGroupsInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeAutoScalingGroupsOutput, error) {
	name := "asg-a"
	arn := "arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:uuid:autoScalingGroupName/asg-a"
	subnets := "subnet-1,subnet-2"
	lcName := "lc-a"
	instID := "i-123"
	health := "Healthy"
	az := "us-east-1a"
	status := "Active"
	now := time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC)
	return &sdkasg.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []types.AutoScalingGroup{
			{
				AutoScalingGroupName:    &name,
				AutoScalingGroupARN:     &arn,
				VPCZoneIdentifier:       &subnets,
				LaunchConfigurationName: &lcName,
				TargetGroupARNs:         []string{"arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg-a/abc"},
				CreatedTime:             &now,
				Status:                  &status,
				Instances: []types.Instance{
					{InstanceId: &instID, HealthStatus: &health, AvailabilityZone: &az, LifecycleState: types.LifecycleStateInService},
				},
			},
		},
	}, nil
}

func (fakeASGAPI) DescribeAutoScalingInstances(ctx context.Context, params *sdkasg.DescribeAutoScalingInstancesInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeAutoScalingInstancesOutput, error) {
	return &sdkasg.DescribeAutoScalingInstancesOutput{}, nil
}

func (fakeASGAPI) DescribeLaunchConfigurations(ctx context.Context, params *sdkasg.DescribeLaunchConfigurationsInput, optFns ...func(*sdkasg.Options)) (*sdkasg.DescribeLaunchConfigurationsOutput, error) {
	name := "lc-a"
	image := "ami-abc"
	key := "kp-a"
	role := "arn:aws:iam::123456789012:role/runner"
	return &sdkasg.DescribeLaunchConfigurationsOutput{
		LaunchConfigurations: []types.LaunchConfiguration{
			{
				LaunchConfigurationName: &name,
				ImageId:                 &image,
				KeyName:                 &key,
				IamInstanceProfile:      &role,
				SecurityGroups:          []string{"sg-1"},
				InstanceType:            awsSDK.String("t3.medium"),
			},
		},
	}, nil
}

func TestProvider_List_EmitsGroupAndEdges(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newASG = func(cfg awsSDK.Config) asgAPI { return fakeASGAPI{} }

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
	foundGroup := false
	foundUsesAMI := false
	for _, n := range res.Nodes {
		if n.Type == "autoscaling:group" {
			foundGroup = true
			break
		}
	}
	for _, e := range res.Edges {
		if e.Kind == "uses" && len(string(e.To)) > 0 && (strings.Contains(string(e.To), "|ec2%3Aami|") || strings.Contains(string(e.To), "|ec2:ami|")) {
			foundUsesAMI = true
		}
	}
	if !foundGroup {
		t.Fatalf("expected autoscaling:group node")
	}
	if !foundUsesAMI {
		t.Fatalf("expected group/lc uses ami edge")
	}
}
