package cloudtrail

import (
	"context"
	"testing"
	"time"

	"awscope/internal/providers"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
)

type fakeCloudTrail struct{}

func (fakeCloudTrail) DescribeTrails(ctx context.Context, params *sdkcloudtrail.DescribeTrailsInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.DescribeTrailsOutput, error) {
	return &sdkcloudtrail.DescribeTrailsOutput{TrailList: []types.Trail{{
		Name:                      awsSDK.String("org-trail"),
		TrailARN:                  awsSDK.String("arn:aws:cloudtrail:us-east-1:123456789012:trail/org-trail"),
		HomeRegion:                awsSDK.String("us-east-1"),
		S3BucketName:              awsSDK.String("trail-bucket"),
		CloudWatchLogsLogGroupArn: awsSDK.String("arn:aws:logs:us-east-1:123456789012:log-group:trail"),
	}}}, nil
}

func (fakeCloudTrail) GetTrailStatus(ctx context.Context, params *sdkcloudtrail.GetTrailStatusInput, optFns ...func(*sdkcloudtrail.Options)) (*sdkcloudtrail.GetTrailStatusOutput, error) {
	now := time.Now().UTC()
	return &sdkcloudtrail.GetTrailStatusOutput{IsLogging: awsSDK.Bool(true), LatestDeliveryTime: &now}, nil
}

func TestProvider_List_ProducesTrailNode(t *testing.T) {
	p := New()
	p.newCloudTrail = func(cfg awsSDK.Config) cloudTrailAPI { return fakeCloudTrail{} }

	res, err := p.List(context.Background(), awsSDK.Config{}, providers.ListRequest{AccountID: "123456789012", Partition: "aws", Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Nodes) == 0 {
		t.Fatalf("expected nodes")
	}
	foundTrail := false
	for _, n := range res.Nodes {
		if n.Type == "cloudtrail:trail" {
			foundTrail = true
			if n.Attributes["status"] != "logging" {
				t.Fatalf("status=%v", n.Attributes["status"])
			}
		}
	}
	if !foundTrail {
		t.Fatalf("trail node not found")
	}
	if len(res.Edges) == 0 {
		t.Fatalf("expected edges")
	}
}
