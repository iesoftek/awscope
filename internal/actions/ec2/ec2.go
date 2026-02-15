package ec2

import (
	"context"
	"fmt"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"

	sdkec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registry.Register(StopInstance{})
	registry.Register(StartInstance{})
}

type StopInstance struct{}

func (StopInstance) ID() string          { return "ec2.stop" }
func (StopInstance) Title() string       { return "Stop instance" }
func (StopInstance) Description() string { return "Stop an EC2 instance" }
func (StopInstance) Risk() actions.RiskLevel {
	return actions.RiskMedium
}
func (StopInstance) Applicable(node graph.ResourceNode) bool {
	return node.Service == "ec2" && node.Type == "ec2:instance" && node.PrimaryID != ""
}
func (StopInstance) Execute(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	cli := sdkec2.NewFromConfig(exec.AWSConfig)
	_, err := cli.StopInstances(ctx, &sdkec2.StopInstancesInput{
		InstanceIds: []string{node.PrimaryID},
	})
	if err != nil {
		return actions.Result{}, err
	}
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"instance_id": node.PrimaryID, "region": exec.Region}}, nil
}

type StartInstance struct{}

func (StartInstance) ID() string          { return "ec2.start" }
func (StartInstance) Title() string       { return "Start instance" }
func (StartInstance) Description() string { return "Start an EC2 instance" }
func (StartInstance) Risk() actions.RiskLevel {
	return actions.RiskMedium
}
func (StartInstance) Applicable(node graph.ResourceNode) bool {
	return node.Service == "ec2" && node.Type == "ec2:instance" && node.PrimaryID != ""
}
func (StartInstance) Execute(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	cli := sdkec2.NewFromConfig(exec.AWSConfig)
	_, err := cli.StartInstances(ctx, &sdkec2.StartInstancesInput{
		InstanceIds: []string{node.PrimaryID},
	})
	if err != nil {
		return actions.Result{}, err
	}
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"instance_id": node.PrimaryID, "region": exec.Region}}, nil
}

func requireRegion(region string) error {
	if region == "" || region == "global" {
		return fmt.Errorf("action requires a regional resource, got region=%q", region)
	}
	return nil
}
