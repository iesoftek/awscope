package ecs

import (
	"context"
	"fmt"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"

	sdkecs "github.com/aws/aws-sdk-go-v2/service/ecs"
)

func init() {
	registry.Register(StopTask{})
}

type StopTask struct{}

func (StopTask) ID() string          { return "ecs.stop-task" }
func (StopTask) Title() string       { return "Stop task" }
func (StopTask) Description() string { return "Stop a running ECS task" }
func (StopTask) Risk() actions.RiskLevel {
	return actions.RiskMedium
}
func (StopTask) Applicable(node graph.ResourceNode) bool {
	return node.Service == "ecs" && node.Type == "ecs:task" && node.PrimaryID != ""
}

func (StopTask) Execute(ctx context.Context, exec actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	clusterArn, _ := node.Attributes["clusterArn"].(string)
	if clusterArn == "" {
		return actions.Result{}, fmt.Errorf("ecs task missing attribute clusterArn")
	}

	cli := sdkecs.NewFromConfig(exec.AWSConfig)
	_, err := cli.StopTask(ctx, &sdkecs.StopTaskInput{
		Cluster: &clusterArn,
		Task:    &node.PrimaryID,
		Reason:  awsString("awscope action ecs.stop-task"),
	})
	if err != nil {
		return actions.Result{}, err
	}
	return actions.Result{Status: "SUCCEEDED", Data: map[string]any{"task_arn": node.PrimaryID, "cluster_arn": clusterArn, "region": exec.Region}}, nil
}

func awsString(s string) *string { return &s }
