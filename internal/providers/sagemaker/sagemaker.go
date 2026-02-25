package sagemaker

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
	sdksm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newSM func(cfg awsSDK.Config) smAPI
}

func New() *Provider {
	return &Provider{
		newSM: func(cfg awsSDK.Config) smAPI { return sdksm.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "sagemaker" }
func (p *Provider) DisplayName() string { return "SageMaker" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type smAPI interface {
	ListNotebookInstances(ctx context.Context, params *sdksm.ListNotebookInstancesInput, optFns ...func(*sdksm.Options)) (*sdksm.ListNotebookInstancesOutput, error)
	DescribeNotebookInstance(ctx context.Context, params *sdksm.DescribeNotebookInstanceInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeNotebookInstanceOutput, error)

	ListModels(ctx context.Context, params *sdksm.ListModelsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListModelsOutput, error)
	DescribeModel(ctx context.Context, params *sdksm.DescribeModelInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeModelOutput, error)

	ListEndpointConfigs(ctx context.Context, params *sdksm.ListEndpointConfigsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListEndpointConfigsOutput, error)
	DescribeEndpointConfig(ctx context.Context, params *sdksm.DescribeEndpointConfigInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeEndpointConfigOutput, error)

	ListEndpoints(ctx context.Context, params *sdksm.ListEndpointsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListEndpointsOutput, error)
	DescribeEndpoint(ctx context.Context, params *sdksm.DescribeEndpointInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeEndpointOutput, error)

	ListTrainingJobs(ctx context.Context, params *sdksm.ListTrainingJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListTrainingJobsOutput, error)
	DescribeTrainingJob(ctx context.Context, params *sdksm.DescribeTrainingJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeTrainingJobOutput, error)

	ListProcessingJobs(ctx context.Context, params *sdksm.ListProcessingJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListProcessingJobsOutput, error)
	DescribeProcessingJob(ctx context.Context, params *sdksm.DescribeProcessingJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeProcessingJobOutput, error)

	ListTransformJobs(ctx context.Context, params *sdksm.ListTransformJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListTransformJobsOutput, error)
	DescribeTransformJob(ctx context.Context, params *sdksm.DescribeTransformJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeTransformJobOutput, error)

	ListDomains(ctx context.Context, params *sdksm.ListDomainsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListDomainsOutput, error)
	DescribeDomain(ctx context.Context, params *sdksm.DescribeDomainInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeDomainOutput, error)

	ListUserProfiles(ctx context.Context, params *sdksm.ListUserProfilesInput, optFns ...func(*sdksm.Options)) (*sdksm.ListUserProfilesOutput, error)
	DescribeUserProfile(ctx context.Context, params *sdksm.DescribeUserProfileInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeUserProfileOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("sagemaker provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("sagemaker provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newSM(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api smAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// Notebook instances.
	var nextToken *string
	for {
		out, err := api.ListNotebookInstances(ctx, &sdksm.ListNotebookInstancesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.NotebookInstances {
			name := strings.TrimSpace(awsToString(s.NotebookInstanceName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeNotebookInstance(ctx, &sdksm.DescribeNotebookInstanceInput{NotebookInstanceName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeNotebook(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Models.
	nextToken = nil
	for {
		out, err := api.ListModels(ctx, &sdksm.ListModelsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.Models {
			name := strings.TrimSpace(awsToString(s.ModelName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeModel(ctx, &sdksm.DescribeModelInput{ModelName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeModel(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Endpoint configs.
	nextToken = nil
	for {
		out, err := api.ListEndpointConfigs(ctx, &sdksm.ListEndpointConfigsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.EndpointConfigs {
			name := strings.TrimSpace(awsToString(s.EndpointConfigName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeEndpointConfig(ctx, &sdksm.DescribeEndpointConfigInput{EndpointConfigName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeEndpointConfig(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Endpoints.
	nextToken = nil
	for {
		out, err := api.ListEndpoints(ctx, &sdksm.ListEndpointsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.Endpoints {
			name := strings.TrimSpace(awsToString(s.EndpointName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeEndpoint(ctx, &sdksm.DescribeEndpointInput{EndpointName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeEndpoint(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Training jobs.
	nextToken = nil
	for {
		out, err := api.ListTrainingJobs(ctx, &sdksm.ListTrainingJobsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.TrainingJobSummaries {
			name := strings.TrimSpace(awsToString(s.TrainingJobName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeTrainingJob(ctx, &sdksm.DescribeTrainingJobInput{TrainingJobName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeTrainingJob(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Processing jobs.
	nextToken = nil
	for {
		out, err := api.ListProcessingJobs(ctx, &sdksm.ListProcessingJobsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.ProcessingJobSummaries {
			name := strings.TrimSpace(awsToString(s.ProcessingJobName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeProcessingJob(ctx, &sdksm.DescribeProcessingJobInput{ProcessingJobName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeProcessingJob(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Transform jobs.
	nextToken = nil
	for {
		out, err := api.ListTransformJobs(ctx, &sdksm.ListTransformJobsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.TransformJobSummaries {
			name := strings.TrimSpace(awsToString(s.TransformJobName))
			if name == "" {
				continue
			}
			desc, err := api.DescribeTransformJob(ctx, &sdksm.DescribeTransformJobInput{TransformJobName: &name})
			if err != nil {
				continue
			}
			n, es := normalizeTransformJob(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Domains and user profiles.
	nextToken = nil
	var domainIDs []string
	for {
		out, err := api.ListDomains(ctx, &sdksm.ListDomainsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range out.Domains {
			domainID := strings.TrimSpace(awsToString(s.DomainId))
			if domainID == "" {
				continue
			}
			domainIDs = append(domainIDs, domainID)
			desc, err := api.DescribeDomain(ctx, &sdksm.DescribeDomainInput{DomainId: &domainID})
			if err != nil {
				continue
			}
			n, es := normalizeDomain(partition, accountID, region, s, desc, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	for _, domainID := range domainIDs {
		var profToken *string
		for {
			out, err := api.ListUserProfiles(ctx, &sdksm.ListUserProfilesInput{
				DomainIdEquals: &domainID,
				NextToken:      profToken,
			})
			if err != nil {
				break
			}
			for _, s := range out.UserProfiles {
				name := strings.TrimSpace(awsToString(s.UserProfileName))
				if name == "" {
					continue
				}
				desc, err := api.DescribeUserProfile(ctx, &sdksm.DescribeUserProfileInput{
					DomainId:        &domainID,
					UserProfileName: &name,
				})
				if err != nil {
					continue
				}
				n, es := normalizeUserProfile(partition, accountID, region, s, desc, now)
				nodes = append(nodes, n)
				edges = append(edges, es...)
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			profToken = out.NextToken
		}
	}

	return nodes, edges, nil
}

func normalizeNotebook(partition, accountID, region string, s types.NotebookInstanceSummary, d *sdksm.DescribeNotebookInstanceOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.NotebookInstanceName)
	arn := awsToString(s.NotebookInstanceArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:notebook-instance", firstNonEmpty(arn, name))
	attrs := map[string]any{
		"status": string(s.NotebookInstanceStatus),
	}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	if d != nil {
		attrs["instanceType"] = string(d.InstanceType)
		if d.NotebookInstanceStatus != "" {
			attrs["status"] = string(d.NotebookInstanceStatus)
		}
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:notebook-instance", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, awsToString(d.RoleArn), awsToString(d.SubnetId), d.SecurityGroups, awsToString(d.KmsKeyId), now, "sagemaker.notebook")
	return node, edges
}

func normalizeModel(partition, accountID, region string, s types.ModelSummary, d *sdksm.DescribeModelOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.ModelName)
	arn := awsToString(s.ModelArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:model", firstNonEmpty(arn, name))
	attrs := map[string]any{}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	if d != nil {
		var images []string
		if d.PrimaryContainer != nil {
			if img := strings.TrimSpace(awsToString(d.PrimaryContainer.Image)); img != "" {
				images = append(images, img)
			}
		}
		for _, c := range d.Containers {
			if img := strings.TrimSpace(awsToString(c.Image)); img != "" {
				images = append(images, img)
			}
		}
		if len(images) > 0 {
			attrs["containerImages"] = images
		}
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:model", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var subnet string
	var sgs []string
	var role string
	if d != nil {
		role = awsToString(d.ExecutionRoleArn)
		if d.VpcConfig != nil {
			if len(d.VpcConfig.Subnets) > 0 {
				subnet = d.VpcConfig.Subnets[0]
			}
			sgs = d.VpcConfig.SecurityGroupIds
		}
	}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, role, subnet, sgs, "", now, "sagemaker.model")
	return node, edges
}

func normalizeEndpointConfig(partition, accountID, region string, s types.EndpointConfigSummary, d *sdksm.DescribeEndpointConfigOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.EndpointConfigName)
	arn := awsToString(s.EndpointConfigArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:endpoint-config", firstNonEmpty(arn, name))
	attrs := map[string]any{}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:endpoint-config", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var edges []graph.RelationshipEdge
	if d != nil {
		for _, pv := range d.ProductionVariants {
			modelName := strings.TrimSpace(awsToString(pv.ModelName))
			if modelName == "" {
				continue
			}
			modelKey := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:model", modelName)
			edges = append(edges, graph.RelationshipEdge{From: key, To: modelKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
		if kms := strings.TrimSpace(awsToString(d.KmsKeyId)); kms != "" {
			if k, ok := kmsRefToKey(partition, accountID, region, kms); ok {
				edges = append(edges, graph.RelationshipEdge{From: key, To: k, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
			}
		}
	}
	return node, edges
}

func normalizeEndpoint(partition, accountID, region string, s types.EndpointSummary, d *sdksm.DescribeEndpointOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.EndpointName)
	arn := awsToString(s.EndpointArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:endpoint", firstNonEmpty(arn, name))
	attrs := map[string]any{"status": string(s.EndpointStatus)}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	if d != nil {
		attrs["status"] = string(d.EndpointStatus)
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:endpoint", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var edges []graph.RelationshipEdge
	if d != nil {
		cfgName := strings.TrimSpace(awsToString(d.EndpointConfigName))
		if cfgName != "" {
			cfgKey := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:endpoint-config", cfgName)
			edges = append(edges, graph.RelationshipEdge{From: key, To: cfgKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}
	return node, edges
}

func normalizeTrainingJob(partition, accountID, region string, s types.TrainingJobSummary, d *sdksm.DescribeTrainingJobOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.TrainingJobName)
	arn := awsToString(s.TrainingJobArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:training-job", firstNonEmpty(arn, name))
	attrs := map[string]any{"status": string(s.TrainingJobStatus)}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:training-job", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var subnet string
	var sgs []string
	var role string
	var kms string
	if d != nil {
		role = awsToString(d.RoleArn)
		if d.VpcConfig != nil {
			if len(d.VpcConfig.Subnets) > 0 {
				subnet = d.VpcConfig.Subnets[0]
			}
			sgs = d.VpcConfig.SecurityGroupIds
		}
		if d.OutputDataConfig != nil {
			kms = awsToString(d.OutputDataConfig.KmsKeyId)
		}
		if kms == "" && d.ResourceConfig != nil {
			kms = awsToString(d.ResourceConfig.VolumeKmsKeyId)
		}
	}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, role, subnet, sgs, kms, now, "sagemaker.training")
	return node, edges
}

func normalizeProcessingJob(partition, accountID, region string, s types.ProcessingJobSummary, d *sdksm.DescribeProcessingJobOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.ProcessingJobName)
	arn := awsToString(s.ProcessingJobArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:processing-job", firstNonEmpty(arn, name))
	attrs := map[string]any{"status": string(s.ProcessingJobStatus)}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:processing-job", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var subnet string
	var sgs []string
	var role string
	var kms string
	if d != nil {
		role = awsToString(d.RoleArn)
		if d.NetworkConfig != nil && d.NetworkConfig.VpcConfig != nil {
			if len(d.NetworkConfig.VpcConfig.Subnets) > 0 {
				subnet = d.NetworkConfig.VpcConfig.Subnets[0]
			}
			sgs = d.NetworkConfig.VpcConfig.SecurityGroupIds
		}
		if d.ProcessingOutputConfig != nil {
			kms = awsToString(d.ProcessingOutputConfig.KmsKeyId)
		}
	}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, role, subnet, sgs, kms, now, "sagemaker.processing")
	return node, edges
}

func normalizeTransformJob(partition, accountID, region string, s types.TransformJobSummary, d *sdksm.DescribeTransformJobOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.TransformJobName)
	arn := awsToString(s.TransformJobArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:transform-job", firstNonEmpty(arn, name))
	attrs := map[string]any{"status": string(s.TransformJobStatus)}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:transform-job", Arn: arn, PrimaryID: firstNonEmpty(arn, name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var edges []graph.RelationshipEdge
	if d != nil {
		if model := strings.TrimSpace(awsToString(d.ModelName)); model != "" {
			modelKey := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:model", model)
			edges = append(edges, graph.RelationshipEdge{From: key, To: modelKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}
	return node, edges
}

func normalizeDomain(partition, accountID, region string, s types.DomainDetails, d *sdksm.DescribeDomainOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	id := awsToString(s.DomainId)
	arn := awsToString(s.DomainArn)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:domain", firstNonEmpty(arn, id))
	attrs := map[string]any{"status": string(s.Status)}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(id, arn), Service: "sagemaker", Type: "sagemaker:domain", Arn: arn, PrimaryID: firstNonEmpty(arn, id), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}
	var subnet string
	var sgs []string
	var role string
	var kms string
	if d != nil {
		subnet = awsToStringSliceFirst(d.SubnetIds)
		sgs = d.DefaultUserSettings.SecurityGroups
		role = awsToString(d.DefaultUserSettings.ExecutionRole)
		kms = awsToString(d.KmsKeyId)
	}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, role, subnet, sgs, kms, now, "sagemaker.domain")
	if d != nil {
		if vpcID := strings.TrimSpace(awsToString(d.VpcId)); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}
	return node, edges
}

func normalizeUserProfile(partition, accountID, region string, s types.UserProfileDetails, d *sdksm.DescribeUserProfileOutput, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	name := awsToString(s.UserProfileName)
	arn := ""
	if d != nil {
		arn = awsToString(d.UserProfileArn)
	}
	domainID := awsToString(s.DomainId)
	key := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:user-profile", firstNonEmpty(arn, domainID+"/"+name))
	attrs := map[string]any{"status": string(s.Status), "domainId": domainID}
	if s.CreationTime != nil && !s.CreationTime.IsZero() {
		attrs["created_at"] = s.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, arn), Service: "sagemaker", Type: "sagemaker:user-profile", Arn: arn, PrimaryID: firstNonEmpty(arn, domainID+"/"+name), Attributes: attrs, Raw: raw, CollectedAt: now, Source: "sagemaker"}

	var role string
	var sgs []string
	if d != nil {
		role = awsToString(d.UserSettings.ExecutionRole)
		sgs = d.UserSettings.SecurityGroups
	}
	edges := makeRoleVpcKmsEdges(partition, accountID, region, key, role, "", sgs, "", now, "sagemaker.user-profile")
	if domainID != "" {
		domainKey := graph.EncodeResourceKey(partition, accountID, region, "sagemaker:domain", domainID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: domainKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	return node, edges
}

func makeRoleVpcKmsEdges(partition, accountID, region string, from graph.ResourceKey, roleArn, subnet string, sgs []string, kmsRef string, now time.Time, source string) []graph.RelationshipEdge {
	var edges []graph.RelationshipEdge
	if roleArn = strings.TrimSpace(roleArn); roleArn != "" {
		roleKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		edges = append(edges, graph.RelationshipEdge{From: from, To: roleKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": source}, CollectedAt: now})
	}
	if subnet = strings.TrimSpace(subnet); subnet != "" {
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
		edges = append(edges, graph.RelationshipEdge{From: from, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true, "source": source}, CollectedAt: now})
	}
	for _, sg := range sgs {
		sg = strings.TrimSpace(sg)
		if sg == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
		edges = append(edges, graph.RelationshipEdge{From: from, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true, "source": source}, CollectedAt: now})
	}
	if kmsRef = strings.TrimSpace(kmsRef); kmsRef != "" {
		if toKey, ok := kmsRefToKey(partition, accountID, region, kmsRef); ok {
			edges = append(edges, graph.RelationshipEdge{From: from, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": source}, CollectedAt: now})
		}
	}
	return edges
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func awsToStringSliceFirst(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}

func kmsRefToKey(partition, accountID, fallbackRegion, ref string) (graph.ResourceKey, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "arn:") && strings.Contains(ref, ":alias/") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:alias", ref), true
	}
	if strings.HasPrefix(ref, "arn:") {
		r := arnRegion(ref)
		if r == "" {
			r = fallbackRegion
		}
		return graph.EncodeResourceKey(partition, accountID, r, "kms:key", ref), true
	}
	if strings.HasPrefix(ref, "alias/") {
		arn := fmt.Sprintf("arn:%s:kms:%s:%s:%s", partition, fallbackRegion, accountID, ref)
		return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:alias", arn), true
	}
	return graph.EncodeResourceKey(partition, accountID, fallbackRegion, "kms:key", ref), true
}

func arnRegion(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}
