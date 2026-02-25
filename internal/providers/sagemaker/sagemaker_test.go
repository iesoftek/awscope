package sagemaker

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksm "github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type fakeSMAPI struct{}

func (fakeSMAPI) ListNotebookInstances(ctx context.Context, params *sdksm.ListNotebookInstancesInput, optFns ...func(*sdksm.Options)) (*sdksm.ListNotebookInstancesOutput, error) {
	name := "nb-1"
	arn := "arn:aws:sagemaker:us-east-1:123456789012:notebook-instance/nb-1"
	now := time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC)
	return &sdksm.ListNotebookInstancesOutput{
		NotebookInstances: []types.NotebookInstanceSummary{
			{NotebookInstanceName: &name, NotebookInstanceArn: &arn, NotebookInstanceStatus: types.NotebookInstanceStatusInService, CreationTime: &now},
		},
	}, nil
}

func (fakeSMAPI) DescribeNotebookInstance(ctx context.Context, params *sdksm.DescribeNotebookInstanceInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeNotebookInstanceOutput, error) {
	role := "arn:aws:iam::123456789012:role/sm-role"
	subnet := "subnet-1"
	kms := "arn:aws:kms:us-east-1:123456789012:key/abc"
	return &sdksm.DescribeNotebookInstanceOutput{RoleArn: &role, SubnetId: &subnet, SecurityGroups: []string{"sg-1"}, KmsKeyId: &kms}, nil
}

func (fakeSMAPI) ListModels(ctx context.Context, params *sdksm.ListModelsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListModelsOutput, error) {
	return &sdksm.ListModelsOutput{}, nil
}
func (fakeSMAPI) DescribeModel(ctx context.Context, params *sdksm.DescribeModelInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeModelOutput, error) {
	return &sdksm.DescribeModelOutput{}, nil
}
func (fakeSMAPI) ListEndpointConfigs(ctx context.Context, params *sdksm.ListEndpointConfigsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListEndpointConfigsOutput, error) {
	return &sdksm.ListEndpointConfigsOutput{}, nil
}
func (fakeSMAPI) DescribeEndpointConfig(ctx context.Context, params *sdksm.DescribeEndpointConfigInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeEndpointConfigOutput, error) {
	return &sdksm.DescribeEndpointConfigOutput{}, nil
}
func (fakeSMAPI) ListEndpoints(ctx context.Context, params *sdksm.ListEndpointsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListEndpointsOutput, error) {
	return &sdksm.ListEndpointsOutput{}, nil
}
func (fakeSMAPI) DescribeEndpoint(ctx context.Context, params *sdksm.DescribeEndpointInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeEndpointOutput, error) {
	return &sdksm.DescribeEndpointOutput{}, nil
}
func (fakeSMAPI) ListTrainingJobs(ctx context.Context, params *sdksm.ListTrainingJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListTrainingJobsOutput, error) {
	return &sdksm.ListTrainingJobsOutput{}, nil
}
func (fakeSMAPI) DescribeTrainingJob(ctx context.Context, params *sdksm.DescribeTrainingJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeTrainingJobOutput, error) {
	return &sdksm.DescribeTrainingJobOutput{}, nil
}
func (fakeSMAPI) ListProcessingJobs(ctx context.Context, params *sdksm.ListProcessingJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListProcessingJobsOutput, error) {
	return &sdksm.ListProcessingJobsOutput{}, nil
}
func (fakeSMAPI) DescribeProcessingJob(ctx context.Context, params *sdksm.DescribeProcessingJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeProcessingJobOutput, error) {
	return &sdksm.DescribeProcessingJobOutput{}, nil
}
func (fakeSMAPI) ListTransformJobs(ctx context.Context, params *sdksm.ListTransformJobsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListTransformJobsOutput, error) {
	return &sdksm.ListTransformJobsOutput{}, nil
}
func (fakeSMAPI) DescribeTransformJob(ctx context.Context, params *sdksm.DescribeTransformJobInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeTransformJobOutput, error) {
	return &sdksm.DescribeTransformJobOutput{}, nil
}
func (fakeSMAPI) ListDomains(ctx context.Context, params *sdksm.ListDomainsInput, optFns ...func(*sdksm.Options)) (*sdksm.ListDomainsOutput, error) {
	return &sdksm.ListDomainsOutput{}, nil
}
func (fakeSMAPI) DescribeDomain(ctx context.Context, params *sdksm.DescribeDomainInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeDomainOutput, error) {
	return &sdksm.DescribeDomainOutput{}, nil
}
func (fakeSMAPI) ListUserProfiles(ctx context.Context, params *sdksm.ListUserProfilesInput, optFns ...func(*sdksm.Options)) (*sdksm.ListUserProfilesOutput, error) {
	return &sdksm.ListUserProfilesOutput{}, nil
}
func (fakeSMAPI) DescribeUserProfile(ctx context.Context, params *sdksm.DescribeUserProfileInput, optFns ...func(*sdksm.Options)) (*sdksm.DescribeUserProfileOutput, error) {
	return &sdksm.DescribeUserProfileOutput{}, nil
}

func TestProvider_List_EmitsNotebookAndEdges(t *testing.T) {
	ctx := context.Background()
	p := New()
	p.newSM = func(cfg awsSDK.Config) smAPI { return fakeSMAPI{} }

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
