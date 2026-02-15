package rds

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
	sdkrds "github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newRDS func(cfg awsSDK.Config) rdsAPI
}

func New() *Provider {
	return &Provider{newRDS: func(cfg awsSDK.Config) rdsAPI { return sdkrds.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "rds" }
func (p *Provider) DisplayName() string { return "RDS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type rdsAPI interface {
	DescribeDBInstances(ctx context.Context, params *sdkrds.DescribeDBInstancesInput, optFns ...func(*sdkrds.Options)) (*sdkrds.DescribeDBInstancesOutput, error)
	DescribeDBClusters(ctx context.Context, params *sdkrds.DescribeDBClustersInput, optFns ...func(*sdkrds.Options)) (*sdkrds.DescribeDBClustersOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("rds provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("rds provider requires account identity")
	}
	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newRDS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api rdsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// Instances.
	var marker *string
	for {
		resp, err := api.DescribeDBInstances(ctx, &sdkrds.DescribeDBInstancesInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, inst := range resp.DBInstances {
			n, stubs, es := normalizeDBInstance(partition, accountID, region, inst, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if resp.Marker == nil || *resp.Marker == "" {
			break
		}
		marker = resp.Marker
	}

	// Clusters (best-effort).
	marker = nil
	for {
		resp, err := api.DescribeDBClusters(ctx, &sdkrds.DescribeDBClustersInput{Marker: marker})
		if err != nil {
			// Some accounts may not have permissions; treat as optional.
			break
		}
		for _, c := range resp.DBClusters {
			n, stubs, es := normalizeDBCluster(partition, accountID, region, c, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if resp.Marker == nil || *resp.Marker == "" {
			break
		}
		marker = resp.Marker
	}

	return nodes, edges, nil
}

func normalizeDBInstance(partition, accountID, region string, inst types.DBInstance, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := awsToString(inst.DBInstanceIdentifier)
	arn := awsToString(inst.DBInstanceArn)
	display := id
	if display == "" {
		display = arn
	}
	primary := arn
	if primary == "" {
		primary = id
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "rds:db-instance", primary)

	attrs := map[string]any{
		"status":      awsToString(inst.DBInstanceStatus),
		"engine":      awsToString(inst.Engine),
		"class":       awsToString(inst.DBInstanceClass),
		"multiAz":     inst.MultiAZ != nil && *inst.MultiAZ,
		"public":      inst.PubliclyAccessible,
		"encrypted":   inst.StorageEncrypted != nil && *inst.StorageEncrypted,
		"endpoint":    "",
		"port":        int32(0),
		"subnetGroup": "",
		// Best-effort fields for cost estimation.
		"allocatedStorageGb": int32(0),
		"storageType":        awsToString(inst.StorageType),
		"deployment": func() string {
			if inst.MultiAZ != nil && *inst.MultiAZ {
				return "multi-az"
			}
			return "single-az"
		}(),
	}
	if inst.AllocatedStorage != nil {
		attrs["allocatedStorageGb"] = *inst.AllocatedStorage
	}
	if inst.Endpoint != nil {
		attrs["endpoint"] = awsToString(inst.Endpoint.Address)
		if inst.Endpoint.Port != nil {
			attrs["port"] = *inst.Endpoint.Port
		}
	}
	if inst.InstanceCreateTime != nil && !inst.InstanceCreateTime.IsZero() {
		attrs["created_at"] = inst.InstanceCreateTime.UTC().Format("2006-01-02 15:04")
	}
	if inst.DBSubnetGroup != nil {
		attrs["subnetGroup"] = awsToString(inst.DBSubnetGroup.DBSubnetGroupName)
	}
	if inst.DBClusterIdentifier != nil && strings.TrimSpace(*inst.DBClusterIdentifier) != "" {
		attrs["cluster"] = strings.TrimSpace(*inst.DBClusterIdentifier)
	}

	raw, _ := json.Marshal(inst)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "rds",
		Type:        "rds:db-instance",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "rds",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// Instance -> VPC/Subnet (via subnet group).
	if inst.DBSubnetGroup != nil {
		if vpcID := awsToString(inst.DBSubnetGroup.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			stubs = append(stubs, stubNode(vpcKey, "ec2", "ec2:vpc", vpcID, now, "rds"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
		for _, sn := range inst.DBSubnetGroup.Subnets {
			subnetID := awsToString(sn.SubnetIdentifier)
			if subnetID == "" {
				continue
			}
			subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
			stubs = append(stubs, stubNode(subnetKey, "ec2", "ec2:subnet", subnetID, now, "rds"))
			edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}

	// Instance -> Security groups.
	for _, sg := range inst.VpcSecurityGroups {
		sgID := awsToString(sg.VpcSecurityGroupId)
		if sgID == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)
		stubs = append(stubs, stubNode(sgKey, "ec2", "ec2:security-group", sgID, now, "rds"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// Instance -> KMS key (if encrypted).
	if inst.StorageEncrypted != nil && *inst.StorageEncrypted {
		if kms := strings.TrimSpace(awsToString(inst.KmsKeyId)); kms != "" {
			toKey, ok := kmsRefToKey(partition, accountID, region, kms)
			if ok {
				edges = append(edges, graph.RelationshipEdge{
					From:        key,
					To:          toKey,
					Kind:        "uses",
					Meta:        map[string]any{"direct": true, "source": "rds.kms"},
					CollectedAt: now,
				})
			}
		}
	}

	return node, stubs, edges
}

func normalizeDBCluster(partition, accountID, region string, c types.DBCluster, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := awsToString(c.DBClusterIdentifier)
	arn := awsToString(c.DBClusterArn)
	display := id
	if display == "" {
		display = arn
	}
	primary := arn
	if primary == "" {
		primary = id
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "rds:db-cluster", primary)
	raw, _ := json.Marshal(c)

	attrs := map[string]any{
		"status":     awsToString(c.Status),
		"engine":     awsToString(c.Engine),
		"engineMode": awsToString(c.EngineMode),
		"storageType": func() string {
			// Only present on some Aurora variants (e.g. I/O-Optimized).
			return awsToString(c.StorageType)
		}(),
		// Best-effort fields for Aurora Serverless pricing.
		"serverlessV2MinAcu": float64(0),
		"serverlessV2MaxAcu": float64(0),
		"serverlessV1MinAcu": float64(0),
		"serverlessV1MaxAcu": float64(0),
	}
	if c.ClusterCreateTime != nil && !c.ClusterCreateTime.IsZero() {
		attrs["created_at"] = c.ClusterCreateTime.UTC().Format("2006-01-02 15:04")
	}
	if c.ServerlessV2ScalingConfiguration != nil {
		if c.ServerlessV2ScalingConfiguration.MinCapacity != nil {
			attrs["serverlessV2MinAcu"] = float64(*c.ServerlessV2ScalingConfiguration.MinCapacity)
		}
		if c.ServerlessV2ScalingConfiguration.MaxCapacity != nil {
			attrs["serverlessV2MaxAcu"] = float64(*c.ServerlessV2ScalingConfiguration.MaxCapacity)
		}
	}
	if c.ScalingConfigurationInfo != nil {
		// Serverless v1 scaling config. Values are ACUs (ints).
		if c.ScalingConfigurationInfo.MinCapacity != nil {
			attrs["serverlessV1MinAcu"] = float64(*c.ScalingConfigurationInfo.MinCapacity)
		}
		if c.ScalingConfigurationInfo.MaxCapacity != nil {
			attrs["serverlessV1MaxAcu"] = float64(*c.ScalingConfigurationInfo.MaxCapacity)
		}
	}

	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "rds",
		Type:        "rds:db-cluster",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "rds",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// Cluster -> KMS key
	if kms := strings.TrimSpace(awsToString(c.KmsKeyId)); kms != "" {
		toKey, ok := kmsRefToKey(partition, accountID, region, kms)
		if ok {
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "uses",
				Meta:        map[string]any{"direct": true, "source": "rds.kms"},
				CollectedAt: now,
			})
		}
	}

	for _, sg := range c.VpcSecurityGroups {
		sgID := awsToString(sg.VpcSecurityGroupId)
		if sgID == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)
		stubs = append(stubs, stubNode(sgKey, "ec2", "ec2:security-group", sgID, now, "rds"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	return node, stubs, edges
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
