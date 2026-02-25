package config

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
	sdkconfig "github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/aws/aws-sdk-go-v2/service/configservice/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newConfig func(cfg awsSDK.Config) configAPI
}

func New() *Provider {
	return &Provider{newConfig: func(cfg awsSDK.Config) configAPI { return sdkconfig.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "config" }
func (p *Provider) DisplayName() string { return "AWS Config" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type configAPI interface {
	DescribeConfigurationRecorders(ctx context.Context, params *sdkconfig.DescribeConfigurationRecordersInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeConfigurationRecordersOutput, error)
	DescribeConfigurationRecorderStatus(ctx context.Context, params *sdkconfig.DescribeConfigurationRecorderStatusInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeConfigurationRecorderStatusOutput, error)
	DescribeDeliveryChannels(ctx context.Context, params *sdkconfig.DescribeDeliveryChannelsInput, optFns ...func(*sdkconfig.Options)) (*sdkconfig.DescribeDeliveryChannelsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("config provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("config provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newConfig(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api configAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	statusByName := map[string]types.ConfigurationRecorderStatus{}
	if statusOut, err := api.DescribeConfigurationRecorderStatus(ctx, &sdkconfig.DescribeConfigurationRecorderStatusInput{}); err == nil {
		for _, s := range statusOut.ConfigurationRecordersStatus {
			name := strings.TrimSpace(awsToString(s.Name))
			if name != "" {
				statusByName[name] = s
			}
		}
	}

	recordersOut, err := api.DescribeConfigurationRecorders(ctx, &sdkconfig.DescribeConfigurationRecordersInput{})
	if err != nil {
		return nil, nil, err
	}

	channelsOut, err := api.DescribeDeliveryChannels(ctx, &sdkconfig.DescribeDeliveryChannelsInput{})
	if err != nil {
		return nil, nil, err
	}

	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	for _, r := range recordersOut.ConfigurationRecorders {
		n, stubs, es := normalizeRecorder(partition, accountID, region, r, statusByName[strings.TrimSpace(awsToString(r.Name))], now)
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}

	for _, d := range channelsOut.DeliveryChannels {
		n, stubs, es := normalizeDeliveryChannel(partition, accountID, region, d, now)
		nodes = append(nodes, n)
		nodes = append(nodes, stubs...)
		edges = append(edges, es...)
	}

	// Link recorders to channels when both exist in region.
	if len(recordersOut.ConfigurationRecorders) == 1 && len(channelsOut.DeliveryChannels) > 0 {
		r := recordersOut.ConfigurationRecorders[0]
		rName := strings.TrimSpace(awsToString(r.Name))
		rKey := graph.EncodeResourceKey(partition, accountID, region, "config:recorder", firstNonEmpty(strings.TrimSpace(awsToString(r.Arn)), rName))
		for _, d := range channelsOut.DeliveryChannels {
			dName := strings.TrimSpace(awsToString(d.Name))
			dKey := graph.EncodeResourceKey(partition, accountID, region, "config:delivery-channel", dName)
			edges = append(edges, graph.RelationshipEdge{From: rKey, To: dKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}

	return nodes, edges, nil
}

func normalizeRecorder(partition, accountID, region string, r types.ConfigurationRecorder, rs types.ConfigurationRecorderStatus, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(r.Arn))
	name := strings.TrimSpace(awsToString(r.Name))
	primary := firstNonEmpty(arn, name)
	key := graph.EncodeResourceKey(partition, accountID, region, "config:recorder", primary)
	status := "unknown"
	if strings.TrimSpace(string(rs.LastStatus)) != "" {
		status = strings.TrimSpace(strings.ToLower(string(rs.LastStatus)))
	}
	if rs.Recording {
		status = "recording"
	}
	attrs := map[string]any{
		"status":           status,
		"recording":        rs.Recording,
		"recordingScope":   strings.TrimSpace(string(r.RecordingScope)),
		"servicePrincipal": strings.TrimSpace(awsToString(r.ServicePrincipal)),
	}
	if rs.LastStatusChangeTime != nil {
		attrs["lastStatusChangeAt"] = rs.LastStatusChangeTime.UTC().Format("2006-01-02 15:04")
	}
	if rs.LastErrorCode != nil {
		attrs["lastErrorCode"] = strings.TrimSpace(awsToString(rs.LastErrorCode))
	}
	if rs.LastErrorMessage != nil {
		attrs["lastErrorMessage"] = strings.TrimSpace(awsToString(rs.LastErrorMessage))
	}
	raw, _ := json.Marshal(map[string]any{"recorder": r, "status": rs})
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, arn),
		Service:     "config",
		Type:        "config:recorder",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "config",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if roleArn := strings.TrimSpace(awsToString(r.RoleARN)); roleArn != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleArn)
		stubs = append(stubs, stubNode(toKey, "iam", "iam:role", shortArn(roleArn), now, "config"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "config.role"}, CollectedAt: now})
	}
	return node, stubs, edges
}

func normalizeDeliveryChannel(partition, accountID, region string, d types.DeliveryChannel, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	name := strings.TrimSpace(awsToString(d.Name))
	key := graph.EncodeResourceKey(partition, accountID, region, "config:delivery-channel", name)
	attrs := map[string]any{
		"s3Bucket":    strings.TrimSpace(awsToString(d.S3BucketName)),
		"s3KeyPrefix": strings.TrimSpace(awsToString(d.S3KeyPrefix)),
		"snsTopicArn": strings.TrimSpace(awsToString(d.SnsTopicARN)),
		"s3KmsKeyArn": strings.TrimSpace(awsToString(d.S3KmsKeyArn)),
		"status":      "active",
	}
	if d.ConfigSnapshotDeliveryProperties != nil && d.ConfigSnapshotDeliveryProperties.DeliveryFrequency != "" {
		attrs["deliveryFrequency"] = string(d.ConfigSnapshotDeliveryProperties.DeliveryFrequency)
	}
	raw, _ := json.Marshal(d)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "config",
		Type:        "config:delivery-channel",
		PrimaryID:   name,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "config",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if b := strings.TrimSpace(awsToString(d.S3BucketName)); b != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "s3:bucket", b)
		stubs = append(stubs, stubNode(toKey, "s3", "s3:bucket", b, now, "config"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "config.s3"}, CollectedAt: now})
	}
	if s := strings.TrimSpace(awsToString(d.SnsTopicARN)); s != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(s, region), "sns:topic", s)
		stubs = append(stubs, stubNode(toKey, "sns", "sns:topic", shortArn(s), now, "config"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "config.sns"}, CollectedAt: now})
	}
	if kms := strings.TrimSpace(awsToString(d.S3KmsKeyArn)); kms != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
		stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "config"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"direct": true, "source": "config.kms"}, CollectedAt: now})
	}
	return node, stubs, edges
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
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
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{"stub": true},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
}

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}
