package aws

import (
	"context"
	"fmt"
	"sort"
	"strings"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type Identity struct {
	AccountID string
	Arn       string
	Partition string
}

type Loader struct {
	// Stub points allow tests to inject fake clients.
	newSTS func(cfg awsSDK.Config) stsAPI
	newEC2 func(cfg awsSDK.Config) ec2API
}

func NewLoader() *Loader {
	return &Loader{
		newSTS: func(cfg awsSDK.Config) stsAPI { return sts.NewFromConfig(cfg) },
		newEC2: func(cfg awsSDK.Config) ec2API { return ec2.NewFromConfig(cfg) },
	}
}

// Load loads AWS SDK config for a specific profile and region.
// Region is required for regional services; global services will override to "global" at the provider layer.
func (l *Loader) Load(ctx context.Context, profile, region string) (awsSDK.Config, Identity, error) {
	opts := []func(*config.LoadOptions) error{}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsSDK.Config{}, Identity{}, err
	}

	id, err := l.getIdentity(ctx, cfg)
	if err != nil {
		return awsSDK.Config{}, Identity{}, err
	}

	return cfg, id, nil
}

type stsAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

func (l *Loader) getIdentity(ctx context.Context, cfg awsSDK.Config) (Identity, error) {
	out, err := l.newSTS(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Identity{}, err
	}
	arn := ""
	if out.Arn != nil {
		arn = *out.Arn
	}
	partition := partitionFromArn(arn)

	accountID := ""
	if out.Account != nil {
		accountID = *out.Account
	}

	return Identity{
		AccountID: accountID,
		Arn:       arn,
		Partition: partition,
	}, nil
}

func partitionFromArn(arn string) string {
	// arn:{partition}:{service}:{region}:{account}:{resource}
	// Example STS: arn:aws:sts::123456789012:assumed-role/RoleName/session
	if arn == "" {
		return "aws"
	}
	parts := strings.Split(arn, ":")
	if len(parts) < 3 || parts[0] != "arn" {
		return "aws"
	}
	p := parts[1]
	if p == "" {
		return "aws"
	}
	return p
}

func RequireIdentity(id Identity) error {
	if id.AccountID == "" {
		return fmt.Errorf("missing account id (sts GetCallerIdentity returned empty)")
	}
	if id.Partition == "" {
		return fmt.Errorf("missing partition")
	}
	return nil
}

type ec2API interface {
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

// ListEnabledRegions uses EC2 DescribeRegions and filters out regions with opt-in status "not-opted-in".
// This requires ec2:DescribeRegions permission.
func (l *Loader) ListEnabledRegions(ctx context.Context, cfg awsSDK.Config) ([]string, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	out, err := l.newEC2(cfg).DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: awsBool(true),
	})
	if err != nil {
		return nil, err
	}
	var regions []string
	for _, r := range out.Regions {
		name := awsToString(r.RegionName)
		if name == "" {
			continue
		}
		// Keep regions that are enabled or don't require opt-in.
		switch strings.ToLower(awsToString(r.OptInStatus)) {
		case "opted-in", "opt-in-not-required":
			regions = append(regions, name)
		case "":
			// Be permissive if the API omits opt-in status.
			regions = append(regions, name)
		default:
			// drop not-opted-in and unknown statuses
		}
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("no enabled regions returned by DescribeRegions")
	}
	sort.Strings(regions)
	return regions, nil
}

func awsBool(v bool) *bool { return &v }

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
