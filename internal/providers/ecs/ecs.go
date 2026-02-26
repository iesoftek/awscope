package ecs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newECS func(cfg awsSDK.Config) ecsAPI
}

func New() *Provider {
	return &Provider{
		newECS: func(cfg awsSDK.Config) ecsAPI { return sdkecs.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "ecs" }
func (p *Provider) DisplayName() string { return "ECS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type ecsAPI interface {
	ListClusters(ctx context.Context, params *sdkecs.ListClustersInput, optFns ...func(*sdkecs.Options)) (*sdkecs.ListClustersOutput, error)
	DescribeClusters(ctx context.Context, params *sdkecs.DescribeClustersInput, optFns ...func(*sdkecs.Options)) (*sdkecs.DescribeClustersOutput, error)

	ListServices(ctx context.Context, params *sdkecs.ListServicesInput, optFns ...func(*sdkecs.Options)) (*sdkecs.ListServicesOutput, error)
	DescribeServices(ctx context.Context, params *sdkecs.DescribeServicesInput, optFns ...func(*sdkecs.Options)) (*sdkecs.DescribeServicesOutput, error)

	ListTasks(ctx context.Context, params *sdkecs.ListTasksInput, optFns ...func(*sdkecs.Options)) (*sdkecs.ListTasksOutput, error)
	DescribeTasks(ctx context.Context, params *sdkecs.DescribeTasksInput, optFns ...func(*sdkecs.Options)) (*sdkecs.DescribeTasksOutput, error)

	DescribeTaskDefinition(ctx context.Context, params *sdkecs.DescribeTaskDefinitionInput, optFns ...func(*sdkecs.Options)) (*sdkecs.DescribeTaskDefinitionOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("ecs provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("ecs provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region

		nodes, edges, err := p.listRegion(ctx, p.newECS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api ecsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	clusterArns, err := listAllClusters(ctx, api)
	if err != nil {
		return nil, nil, err
	}

	clusters, err := describeClusters(ctx, api, clusterArns)
	if err != nil {
		return nil, nil, err
	}

	type clusterResult struct {
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
		err   error
	}
	results := make([]clusterResult, len(clusters))
	jobs := make(chan int, len(clusters))
	for i := range clusters {
		jobs <- i
	}
	close(jobs)
	workers := envIntOr("AWSCOPE_ECS_CLUSTER_CONCURRENCY", 8)
	if workers > len(clusters) {
		workers = len(clusters)
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			nodes, edges, err := p.collectCluster(ctx, api, partition, accountID, region, clusters[idx], now)
			results[idx] = clusterResult{nodes: nodes, edges: edges, err: err}
		}
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	wg.Wait()

	var (
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
	)
	for _, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		nodes = append(nodes, r.nodes...)
		edges = append(edges, r.edges...)
	}
	return nodes, edges, nil
}

func (p *Provider) collectCluster(ctx context.Context, api ecsAPI, partition, accountID, region string, c types.Cluster, now time.Time) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	var (
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
	)
	nodes = append(nodes, normalizeCluster(partition, accountID, region, c, now))

	tdSet := map[string]bool{}
	tdInfo := map[string]taskDefInfo{}

	clusterArn := awsToString(c.ClusterArn)
	serviceArns, err := listAllServices(ctx, api, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	services, err := describeServices(ctx, api, clusterArn, serviceArns)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range services {
		if td := strings.TrimSpace(awsToString(s.TaskDefinition)); td != "" {
			tdSet[td] = true
		}
	}

	taskArns, err := listAllTasks(ctx, api, clusterArn)
	if err != nil {
		return nil, nil, err
	}
	tasks, err := describeTasks(ctx, api, clusterArn, taskArns)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range tasks {
		if td := strings.TrimSpace(awsToString(t.TaskDefinitionArn)); td != "" {
			tdSet[td] = true
		}
	}

	tdArns := make([]string, 0, len(tdSet))
	for tdArn := range tdSet {
		tdArns = append(tdArns, tdArn)
	}
	tdNodes, tdInfo := describeTaskDefinitionsParallel(ctx, api, partition, accountID, region, tdArns, now)
	nodes = append(nodes, tdNodes...)

	for _, s := range services {
		n, stubs, es := normalizeService(partition, accountID, region, s, tdInfo, now)
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}

	clusterName := clusterNameFromArn(clusterArn)
	for _, t := range tasks {
		n, stubs, es := normalizeTask(partition, accountID, region, clusterName, t, tdInfo, now)
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}
	return nodes, edges, nil
}

func describeTaskDefinitionsParallel(ctx context.Context, api ecsAPI, partition, accountID, region string, tdArns []string, now time.Time) ([]graph.ResourceNode, map[string]taskDefInfo) {
	if len(tdArns) == 0 {
		return nil, map[string]taskDefInfo{}
	}
	type tdResult struct {
		node *graph.ResourceNode
		arn  string
		info taskDefInfo
	}
	results := make([]tdResult, len(tdArns))
	jobs := make(chan int, len(tdArns))
	for i := range tdArns {
		jobs <- i
	}
	close(jobs)

	workers := envIntOr("AWSCOPE_ECS_TASKDEF_CONCURRENCY", 20)
	if workers > len(tdArns) {
		workers = len(tdArns)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for idx := range jobs {
			tdArn := tdArns[idx]
			out, err := api.DescribeTaskDefinition(ctx, &sdkecs.DescribeTaskDefinitionInput{TaskDefinition: &tdArn})
			if err != nil || out == nil || out.TaskDefinition == nil {
				continue
			}
			td := *out.TaskDefinition
			n := normalizeTaskDefinition(partition, accountID, region, tdArn, td, now)
			results[idx] = tdResult{
				node: &n,
				arn:  tdArn,
				info: extractTaskDefInfo(td),
			}
		}
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	wg.Wait()

	nodes := make([]graph.ResourceNode, 0, len(results))
	info := map[string]taskDefInfo{}
	for _, r := range results {
		if r.node == nil {
			continue
		}
		nodes = append(nodes, *r.node)
		info[r.arn] = r.info
	}
	return nodes, info
}

func listAllClusters(ctx context.Context, api ecsAPI) ([]string, error) {
	var out []string
	var nextToken *string
	for {
		resp, err := api.ListClusters(ctx, &sdkecs.ListClustersInput{NextToken: nextToken})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.ClusterArns...)
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return out, nil
}

func describeClusters(ctx context.Context, api ecsAPI, arns []string) ([]types.Cluster, error) {
	if len(arns) == 0 {
		return nil, nil
	}
	var out []types.Cluster
	for i := 0; i < len(arns); i += 100 {
		j := i + 100
		if j > len(arns) {
			j = len(arns)
		}
		resp, err := api.DescribeClusters(ctx, &sdkecs.DescribeClustersInput{
			Clusters: arns[i:j],
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Clusters...)
	}
	return out, nil
}

func listAllServices(ctx context.Context, api ecsAPI, clusterArn string) ([]string, error) {
	var out []string
	var nextToken *string
	for {
		resp, err := api.ListServices(ctx, &sdkecs.ListServicesInput{
			Cluster:   &clusterArn,
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.ServiceArns...)
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return out, nil
}

func describeServices(ctx context.Context, api ecsAPI, clusterArn string, arns []string) ([]types.Service, error) {
	if len(arns) == 0 {
		return nil, nil
	}
	var out []types.Service
	for i := 0; i < len(arns); i += 10 {
		j := i + 10
		if j > len(arns) {
			j = len(arns)
		}
		resp, err := api.DescribeServices(ctx, &sdkecs.DescribeServicesInput{
			Cluster:  &clusterArn,
			Services: arns[i:j],
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Services...)
	}
	return out, nil
}

func listAllTasks(ctx context.Context, api ecsAPI, clusterArn string) ([]string, error) {
	if clusterArn == "" {
		return nil, nil
	}
	var out []string
	seen := map[string]struct{}{}
	statuses := []types.DesiredStatus{
		types.DesiredStatusRunning,
		types.DesiredStatusPending,
		types.DesiredStatusStopped,
	}
	for _, desired := range statuses {
		var nextToken *string
		for {
			resp, err := api.ListTasks(ctx, &sdkecs.ListTasksInput{
				Cluster:       &clusterArn,
				DesiredStatus: desired,
				NextToken:     nextToken,
			})
			if err != nil {
				return nil, err
			}
			for _, arn := range resp.TaskArns {
				if _, ok := seen[arn]; ok {
					continue
				}
				seen[arn] = struct{}{}
				out = append(out, arn)
			}
			if resp.NextToken == nil || *resp.NextToken == "" {
				break
			}
			nextToken = resp.NextToken
		}
	}
	return out, nil
}

func describeTasks(ctx context.Context, api ecsAPI, clusterArn string, arns []string) ([]types.Task, error) {
	if len(arns) == 0 {
		return nil, nil
	}
	var out []types.Task
	for i := 0; i < len(arns); i += 100 {
		j := i + 100
		if j > len(arns) {
			j = len(arns)
		}
		resp, err := api.DescribeTasks(ctx, &sdkecs.DescribeTasksInput{
			Cluster: &clusterArn,
			Tasks:   arns[i:j],
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Tasks...)
	}
	return out, nil
}

type taskDefInfo struct {
	VCPU     float64
	MemoryGB float64
	CPU      string
	Memory   string
}

func extractTaskDefInfo(td types.TaskDefinition) taskDefInfo {
	cpu := strings.TrimSpace(awsToString(td.Cpu))
	mem := strings.TrimSpace(awsToString(td.Memory))
	return taskDefInfo{
		VCPU:     parseECSTaskVCPU(cpu),
		MemoryGB: parseECSTaskMemGB(mem),
		CPU:      cpu,
		Memory:   mem,
	}
}

func parseECSTaskVCPU(cpu string) float64 {
	// ECS CPU is in CPU units as a string: e.g. "256" => 0.25 vCPU.
	cpu = strings.TrimSpace(cpu)
	if cpu == "" {
		return 0
	}
	n, err := strconv.ParseFloat(cpu, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n / 1024.0
}

func parseECSTaskMemGB(mem string) float64 {
	// ECS memory is MB as a string: e.g. "512" => 0.5 GB.
	mem = strings.TrimSpace(mem)
	if mem == "" {
		return 0
	}
	n, err := strconv.ParseFloat(mem, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n / 1024.0
}

func normalizeCluster(partition, accountID, region string, c types.Cluster, now time.Time) graph.ResourceNode {
	arn := awsToString(c.ClusterArn)
	primaryID := arn
	display := clusterNameFromArn(arn)
	if n := awsToString(c.ClusterName); n != "" {
		display = n
	}

	key := graph.EncodeResourceKey(partition, accountID, region, "ecs:cluster", primaryID)
	raw, _ := json.Marshal(c)

	attrs := map[string]any{
		"status":              awsToString(c.Status),
		"runningTasksCount":   c.RunningTasksCount,
		"pendingTasksCount":   c.PendingTasksCount,
		"activeServicesCount": c.ActiveServicesCount,
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ecs",
		Type:        "ecs:cluster",
		Arn:         arn,
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ecs",
	}
}

func normalizeService(partition, accountID, region string, s types.Service, tdInfo map[string]taskDefInfo, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := awsToString(s.ServiceArn)
	primaryID := arn
	display := serviceNameFromArn(arn)
	if n := awsToString(s.ServiceName); n != "" {
		display = n
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ecs:service", primaryID)
	raw, _ := json.Marshal(s)

	attrs := map[string]any{
		"status":       awsToString(s.Status),
		"desiredCount": s.DesiredCount,
		"runningCount": s.RunningCount,
		"pendingCount": s.PendingCount,
		"launchType":   string(s.LaunchType),
		"taskDefinition": func() string {
			return strings.TrimSpace(awsToString(s.TaskDefinition))
		}(),
	}
	if td := strings.TrimSpace(awsToString(s.TaskDefinition)); td != "" && tdInfo != nil {
		if info, ok := tdInfo[td]; ok {
			if info.CPU != "" {
				attrs["cpu"] = info.CPU
			}
			if info.Memory != "" {
				attrs["memory"] = info.Memory
			}
			if info.VCPU > 0 {
				attrs["vcpu"] = info.VCPU
			}
			if info.MemoryGB > 0 {
				attrs["memoryGb"] = info.MemoryGB
			}
		}
	}
	if s.CreatedAt != nil {
		attrs["created_at"] = s.CreatedAt.UTC().Format("2006-01-02 15:04")
	}

	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ecs",
		Type:        "ecs:service",
		Arn:         arn,
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ecs",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// service -> cluster
	if clusterArn := awsToString(s.ClusterArn); clusterArn != "" {
		clusterKey := graph.EncodeResourceKey(partition, accountID, region, "ecs:cluster", clusterArn)
		edges = append(edges, graph.RelationshipEdge{From: key, To: clusterKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// service -> task-definition
	if td := awsToString(s.TaskDefinition); td != "" {
		tdKey := graph.EncodeResourceKey(partition, accountID, region, "ecs:task-definition", td)
		stubs = append(stubs, stubNode(tdKey, "ecs", "ecs:task-definition", shortArn(td), now, "ecs"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: tdKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// service -> target group(s)
	for _, lb := range s.LoadBalancers {
		if tg := awsToString(lb.TargetGroupArn); tg != "" {
			tgKey := graph.EncodeResourceKey(partition, accountID, region, "elbv2:target-group", tg)
			stubs = append(stubs, stubNode(tgKey, "elbv2", "elbv2:target-group", shortArn(tg), now, "ecs"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: tgKey, Kind: "targets", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func normalizeTask(partition, accountID, region, clusterName string, t types.Task, tdInfo map[string]taskDefInfo, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	taskArn := awsToString(t.TaskArn)
	display := shortArn(taskArn)
	if display == "" {
		display = "task"
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ecs:task", taskArn)
	raw, _ := json.Marshal(t)

	attrs := map[string]any{
		"clusterArn":    awsToString(t.ClusterArn),
		"lastStatus":    awsToString(t.LastStatus),
		"desiredStatus": awsToString(t.DesiredStatus),
		"launchType":    string(t.LaunchType),
		"group":         awsToString(t.Group),
		"taskDefinition": func() string {
			return strings.TrimSpace(awsToString(t.TaskDefinitionArn))
		}(),
	}
	if g := strings.TrimSpace(awsToString(t.Group)); strings.HasPrefix(g, "service:") {
		if svc := strings.TrimSpace(strings.TrimPrefix(g, "service:")); svc != "" {
			attrs["serviceName"] = svc
		}
	}
	if td := strings.TrimSpace(awsToString(t.TaskDefinitionArn)); td != "" && tdInfo != nil {
		if info, ok := tdInfo[td]; ok {
			if info.CPU != "" {
				attrs["cpu"] = info.CPU
			}
			if info.Memory != "" {
				attrs["memory"] = info.Memory
			}
			if info.VCPU > 0 {
				attrs["vcpu"] = info.VCPU
			}
			if info.MemoryGB > 0 {
				attrs["memoryGb"] = info.MemoryGB
			}
		}
	}
	if t.CreatedAt != nil {
		attrs["created_at"] = t.CreatedAt.UTC().Format("2006-01-02 15:04")
	}

	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ecs",
		Type:        "ecs:task",
		Arn:         taskArn,
		PrimaryID:   taskArn,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ecs",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// task -> cluster
	if cArn := awsToString(t.ClusterArn); cArn != "" {
		clusterKey := graph.EncodeResourceKey(partition, accountID, region, "ecs:cluster", cArn)
		edges = append(edges, graph.RelationshipEdge{From: key, To: clusterKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// task -> task-definition
	if td := awsToString(t.TaskDefinitionArn); td != "" {
		tdKey := graph.EncodeResourceKey(partition, accountID, region, "ecs:task-definition", td)
		stubs = append(stubs, stubNode(tdKey, "ecs", "ecs:task-definition", shortArn(td), now, "ecs"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: tdKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// task -> service (heuristic via Group == "service:NAME")
	if t.Group != nil && strings.HasPrefix(*t.Group, "service:") && clusterName != "" {
		serviceName := strings.TrimPrefix(*t.Group, "service:")
		if serviceName != "" {
			sArn := fmt.Sprintf("arn:%s:ecs:%s:%s:service/%s/%s", partition, region, accountID, clusterName, serviceName)
			sKey := graph.EncodeResourceKey(partition, accountID, region, "ecs:service", sArn)
			stubs = append(stubs, stubNode(sKey, "ecs", "ecs:service", serviceName, now, "ecs"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: sKey, Kind: "belongs-to", Meta: map[string]any{"heuristic": true}, CollectedAt: now})
		}
	}

	return node, stubs, edges
}

func normalizeTaskDefinition(partition, accountID, region, tdArn string, td types.TaskDefinition, now time.Time) graph.ResourceNode {
	primary := tdArn
	display := shortArn(tdArn)
	if fam := strings.TrimSpace(awsToString(td.Family)); fam != "" {
		if td.Revision > 0 {
			display = fmt.Sprintf("%s:%d", fam, td.Revision)
		} else {
			display = fam
		}
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ecs:task-definition", primary)
	raw, _ := json.Marshal(td)
	attrs := map[string]any{
		"family":      awsToString(td.Family),
		"revision":    td.Revision,
		"cpu":         strings.TrimSpace(awsToString(td.Cpu)),
		"memory":      strings.TrimSpace(awsToString(td.Memory)),
		"networkMode": string(td.NetworkMode),
	}
	if td.RegisteredAt != nil && !td.RegisteredAt.IsZero() {
		attrs["created_at"] = td.RegisteredAt.UTC().Format("2006-01-02 15:04")
	}
	if len(td.RequiresCompatibilities) > 0 {
		var cs []string
		for _, c := range td.RequiresCompatibilities {
			cs = append(cs, string(c))
		}
		attrs["compatibilities"] = cs
	}
	if td.EphemeralStorage != nil && td.EphemeralStorage.SizeInGiB > 0 {
		attrs["ephemeralStorageGiB"] = td.EphemeralStorage.SizeInGiB
	}

	info := extractTaskDefInfo(td)
	if info.VCPU > 0 {
		attrs["vcpu"] = info.VCPU
	}
	if info.MemoryGB > 0 {
		attrs["memoryGb"] = info.MemoryGB
	}

	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ecs",
		Type:        "ecs:task-definition",
		Arn:         tdArn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ecs",
	}
}

func clusterNameFromArn(arn string) string {
	// arn:aws:ecs:region:acct:cluster/name
	if i := strings.LastIndex(arn, "cluster/"); i >= 0 {
		return arn[i+len("cluster/"):]
	}
	return arn
}

func serviceNameFromArn(arn string) string {
	// arn:aws:ecs:region:acct:service/cluster/name OR service/name depending on launch type / API behavior
	if i := strings.LastIndex(arn, "service/"); i >= 0 {
		return arn[i+len("service/"):]
	}
	return arn
}

func shortArn(arn string) string {
	if arn == "" {
		return ""
	}
	// Keep the last segment for display.
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

func envIntOr(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
