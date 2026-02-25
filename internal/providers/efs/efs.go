package efs

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
	sdkefs "github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/efs/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newEFS func(cfg awsSDK.Config) efsAPI
}

func New() *Provider {
	return &Provider{newEFS: func(cfg awsSDK.Config) efsAPI { return sdkefs.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "efs" }
func (p *Provider) DisplayName() string { return "EFS" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type efsAPI interface {
	DescribeFileSystems(ctx context.Context, params *sdkefs.DescribeFileSystemsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeFileSystemsOutput, error)
	DescribeMountTargets(ctx context.Context, params *sdkefs.DescribeMountTargetsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeMountTargetsOutput, error)
	DescribeMountTargetSecurityGroups(ctx context.Context, params *sdkefs.DescribeMountTargetSecurityGroupsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeMountTargetSecurityGroupsOutput, error)
	DescribeAccessPoints(ctx context.Context, params *sdkefs.DescribeAccessPointsInput, optFns ...func(*sdkefs.Options)) (*sdkefs.DescribeAccessPointsOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("efs provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("efs provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newEFS(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api efsAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge
	var marker *string
	for {
		out, err := api.DescribeFileSystems(ctx, &sdkefs.DescribeFileSystemsInput{Marker: marker})
		if err != nil {
			return nil, nil, err
		}
		for _, fs := range out.FileSystems {
			fsNode, stubs, es := normalizeFileSystem(partition, accountID, region, fs, now)
			nodes = append(nodes, fsNode)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)

			mtNodes, mtStubs, mtEdges, err := p.collectMountTargets(ctx, api, partition, accountID, region, fsNode.Key, strings.TrimSpace(awsToString(fs.FileSystemId)), now)
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, mtNodes...)
			nodes = append(nodes, mtStubs...)
			edges = append(edges, mtEdges...)

			apNodes, apStubs, apEdges, err := p.collectAccessPoints(ctx, api, partition, accountID, region, fsNode.Key, strings.TrimSpace(awsToString(fs.FileSystemId)), now)
			if err != nil {
				return nil, nil, err
			}
			nodes = append(nodes, apNodes...)
			nodes = append(nodes, apStubs...)
			edges = append(edges, apEdges...)
		}
		if out.NextMarker == nil || strings.TrimSpace(*out.NextMarker) == "" {
			break
		}
		marker = out.NextMarker
	}

	return nodes, edges, nil
}

func (p *Provider) collectMountTargets(ctx context.Context, api efsAPI, partition, accountID, region string, fsKey graph.ResourceKey, fsID string, now time.Time) ([]graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge, error) {
	if fsID == "" {
		return nil, nil, nil, nil
	}
	var nodes []graph.ResourceNode
	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var marker *string
	for {
		out, err := api.DescribeMountTargets(ctx, &sdkefs.DescribeMountTargetsInput{FileSystemId: awsSDK.String(fsID), Marker: marker})
		if err != nil {
			return nil, nil, nil, err
		}
		for _, mt := range out.MountTargets {
			mtNode, mtStubs, mtEdges := normalizeMountTarget(partition, accountID, region, mt, now)
			nodes = append(nodes, mtNode)
			stubs = append(stubs, mtStubs...)
			edges = append(edges, mtEdges...)
			edges = append(edges, graph.RelationshipEdge{From: fsKey, To: mtNode.Key, Kind: "contains", Meta: map[string]any{"source": "efs.mount-target", "direct": true}, CollectedAt: now})

			mtID := strings.TrimSpace(awsToString(mt.MountTargetId))
			if mtID != "" {
				if sgOut, err := api.DescribeMountTargetSecurityGroups(ctx, &sdkefs.DescribeMountTargetSecurityGroupsInput{MountTargetId: awsSDK.String(mtID)}); err == nil {
					for _, sg := range sgOut.SecurityGroups {
						sg = strings.TrimSpace(sg)
						if sg == "" {
							continue
						}
						toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
						stubs = append(stubs, stubNode(toKey, "ec2", "ec2:security-group", sg, now, "efs"))
						edges = append(edges, graph.RelationshipEdge{From: mtNode.Key, To: toKey, Kind: "attached-to", Meta: map[string]any{"source": "efs.mount-target.sg", "direct": true}, CollectedAt: now})
					}
				}
			}
		}
		if out.NextMarker == nil || strings.TrimSpace(*out.NextMarker) == "" {
			break
		}
		marker = out.NextMarker
	}

	return nodes, stubs, edges, nil
}

func (p *Provider) collectAccessPoints(ctx context.Context, api efsAPI, partition, accountID, region string, fsKey graph.ResourceKey, fsID string, now time.Time) ([]graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge, error) {
	if fsID == "" {
		return nil, nil, nil, nil
	}
	var nodes []graph.ResourceNode
	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	var nextToken *string
	for {
		out, err := api.DescribeAccessPoints(ctx, &sdkefs.DescribeAccessPointsInput{
			FileSystemId: awsSDK.String(fsID),
			NextToken:    nextToken,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		for _, ap := range out.AccessPoints {
			apNode, apStubs, apEdges := normalizeAccessPoint(partition, accountID, region, ap, now)
			nodes = append(nodes, apNode)
			stubs = append(stubs, apStubs...)
			edges = append(edges, apEdges...)
			edges = append(edges, graph.RelationshipEdge{
				From:        fsKey,
				To:          apNode.Key,
				Kind:        "contains",
				Meta:        map[string]any{"source": "efs.access-point", "direct": true},
				CollectedAt: now,
			})
		}
		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, stubs, edges, nil
}

func normalizeFileSystem(partition, accountID, region string, fs types.FileSystemDescription, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(fs.FileSystemId))
	arn := strings.TrimSpace(awsToString(fs.FileSystemArn))
	name := strings.TrimSpace(awsToString(fs.Name))
	key := graph.EncodeResourceKey(partition, accountID, region, "efs:file-system", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":             strings.TrimSpace(string(fs.LifeCycleState)),
		"performanceMode":    strings.TrimSpace(string(fs.PerformanceMode)),
		"throughputMode":     strings.TrimSpace(string(fs.ThroughputMode)),
		"mountTargets":       fs.NumberOfMountTargets,
		"encrypted":          awsToBool(fs.Encrypted),
		"availabilityZone":   strings.TrimSpace(awsToString(fs.AvailabilityZoneName)),
		"availabilityZoneId": strings.TrimSpace(awsToString(fs.AvailabilityZoneId)),
	}
	if fs.CreationTime != nil {
		attrs["created_at"] = fs.CreationTime.UTC().Format("2006-01-02 15:04")
	}
	if fs.SizeInBytes != nil {
		attrs["sizeBytes"] = fs.SizeInBytes.Value
	}
	raw, _ := json.Marshal(fs)
	node := graph.ResourceNode{Key: key, DisplayName: firstNonEmpty(name, id, arn), Service: "efs", Type: "efs:file-system", Arn: arn, PrimaryID: firstNonEmpty(arn, id), Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "efs"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if kms := strings.TrimSpace(awsToString(fs.KmsKeyId)); kms != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, arnRegion(kms, region), "kms:key", kms)
		stubs = append(stubs, stubNode(toKey, "kms", "kms:key", shortArn(kms), now, "efs"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "uses", Meta: map[string]any{"source": "efs.kms", "direct": true}, CollectedAt: now})
	}
	return node, stubs, edges
}

func normalizeMountTarget(partition, accountID, region string, mt types.MountTargetDescription, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(mt.MountTargetId))
	key := graph.EncodeResourceKey(partition, accountID, region, "efs:mount-target", id)
	attrs := map[string]any{
		"status":             strings.TrimSpace(string(mt.LifeCycleState)),
		"subnet":             strings.TrimSpace(awsToString(mt.SubnetId)),
		"vpc":                strings.TrimSpace(awsToString(mt.VpcId)),
		"ip":                 strings.TrimSpace(awsToString(mt.IpAddress)),
		"availabilityZone":   strings.TrimSpace(awsToString(mt.AvailabilityZoneName)),
		"availabilityZoneId": strings.TrimSpace(awsToString(mt.AvailabilityZoneId)),
	}
	raw, _ := json.Marshal(mt)
	node := graph.ResourceNode{Key: key, DisplayName: id, Service: "efs", Type: "efs:mount-target", PrimaryID: id, Tags: map[string]string{}, Attributes: attrs, Raw: raw, CollectedAt: now, Source: "efs"}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	if subnet := strings.TrimSpace(awsToString(mt.SubnetId)); subnet != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnet)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:subnet", subnet, now, "efs"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "efs.mount-target.subnet", "direct": true}, CollectedAt: now})
	}
	if vpc := strings.TrimSpace(awsToString(mt.VpcId)); vpc != "" {
		toKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpc)
		stubs = append(stubs, stubNode(toKey, "ec2", "ec2:vpc", vpc, now, "efs"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "member-of", Meta: map[string]any{"source": "efs.mount-target.vpc", "direct": true}, CollectedAt: now})
	}

	return node, stubs, edges
}

func normalizeAccessPoint(partition, accountID, region string, ap types.AccessPointDescription, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	id := strings.TrimSpace(awsToString(ap.AccessPointId))
	arn := strings.TrimSpace(awsToString(ap.AccessPointArn))
	name := strings.TrimSpace(awsToString(ap.Name))
	key := graph.EncodeResourceKey(partition, accountID, region, "efs:access-point", firstNonEmpty(arn, id))
	attrs := map[string]any{
		"status":       strings.TrimSpace(string(ap.LifeCycleState)),
		"fileSystemId": strings.TrimSpace(awsToString(ap.FileSystemId)),
		"ownerId":      strings.TrimSpace(awsToString(ap.OwnerId)),
	}
	if ap.RootDirectory != nil {
		attrs["rootPath"] = strings.TrimSpace(awsToString(ap.RootDirectory.Path))
		if ap.RootDirectory.CreationInfo != nil {
			if ap.RootDirectory.CreationInfo.OwnerUid != nil {
				attrs["rootOwnerUid"] = *ap.RootDirectory.CreationInfo.OwnerUid
			}
			if ap.RootDirectory.CreationInfo.OwnerGid != nil {
				attrs["rootOwnerGid"] = *ap.RootDirectory.CreationInfo.OwnerGid
			}
			attrs["rootPermissions"] = strings.TrimSpace(awsToString(ap.RootDirectory.CreationInfo.Permissions))
		}
	}
	if ap.PosixUser != nil {
		if ap.PosixUser.Uid != nil {
			attrs["posixUid"] = *ap.PosixUser.Uid
		}
		if ap.PosixUser.Gid != nil {
			attrs["posixGid"] = *ap.PosixUser.Gid
		}
		if len(ap.PosixUser.SecondaryGids) > 0 {
			attrs["posixSecondaryGids"] = ap.PosixUser.SecondaryGids
		}
	}
	raw, _ := json.Marshal(ap)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, id, arn),
		Service:     "efs",
		Type:        "efs:access-point",
		Arn:         arn,
		PrimaryID:   firstNonEmpty(arn, id),
		Tags:        efsTagsToMap(ap.Tags),
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "efs",
	}
	return node, nil, nil
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{Key: key, DisplayName: display, Service: service, Type: typ, PrimaryID: primaryID, Tags: map[string]string{}, Attributes: map[string]any{"stub": true}, Raw: []byte(`{}`), CollectedAt: now, Source: source}
}

func arnRegion(arn, fallback string) string {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 || strings.TrimSpace(parts[3]) == "" {
		return fallback
	}
	return strings.TrimSpace(parts[3])
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func awsToBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
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

func efsTagsToMap(in []types.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		k := strings.TrimSpace(awsToString(t.Key))
		if k == "" {
			continue
		}
		out[k] = strings.TrimSpace(awsToString(t.Value))
	}
	return out
}
