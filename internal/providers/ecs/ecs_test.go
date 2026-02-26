package ecs

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func TestNormalizeService_EmitsEdges(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

	sArn := "arn:aws:ecs:us-east-1:123456789012:service/mycluster/myservice"
	cArn := "arn:aws:ecs:us-east-1:123456789012:cluster/mycluster"
	tdArn := "arn:aws:ecs:us-east-1:123456789012:task-definition/mytd:1"
	tgArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/tg/abc"

	sName := "myservice"
	status := "ACTIVE"

	svc := types.Service{
		ServiceArn:     &sArn,
		ServiceName:    &sName,
		ClusterArn:     &cArn,
		TaskDefinition: &tdArn,
		Status:         &status,
		DesiredCount:   2,
		RunningCount:   1,
		LoadBalancers: []types.LoadBalancer{
			{TargetGroupArn: &tgArn},
		},
	}

	_, stubs, edges := normalizeService("aws", "123456789012", "us-east-1", svc, nil, now)

	if len(edges) != 3 {
		t.Fatalf("edges: got %d want 3", len(edges))
	}
	if len(stubs) != 2 {
		t.Fatalf("stubs: got %d want 2", len(stubs))
	}
}

func TestNormalizeTask_IncludesGroupAndServiceNameAttrs(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)

	taskArn := "arn:aws:ecs:us-east-1:123456789012:task/mycluster/abc123"
	clusterArn := "arn:aws:ecs:us-east-1:123456789012:cluster/mycluster"
	tdArn := "arn:aws:ecs:us-east-1:123456789012:task-definition/mytd:1"
	group := "service:orders"
	lastStatus := "RUNNING"
	desiredStatus := "RUNNING"

	task := types.Task{
		TaskArn:           &taskArn,
		ClusterArn:        &clusterArn,
		TaskDefinitionArn: &tdArn,
		Group:             &group,
		LastStatus:        &lastStatus,
		DesiredStatus:     &desiredStatus,
	}

	_, _, edges := normalizeTask("aws", "123456789012", "us-east-1", "mycluster", task, nil, now)
	n, _, _ := normalizeTask("aws", "123456789012", "us-east-1", "mycluster", task, nil, now)

	if got := n.Attributes["group"]; got != group {
		t.Fatalf("group attr: got %v want %q", got, group)
	}
	if got := n.Attributes["serviceName"]; got != "orders" {
		t.Fatalf("serviceName attr: got %v want %q", got, "orders")
	}
	if len(edges) < 2 {
		t.Fatalf("expected existing edges preserved, got %d", len(edges))
	}
}
