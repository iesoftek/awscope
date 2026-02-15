package ec2

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
	sdkec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newEC2 func(cfg awsSDK.Config) ec2API
}

func New() *Provider {
	return &Provider{
		newEC2: func(cfg awsSDK.Config) ec2API { return sdkec2.NewFromConfig(cfg) },
	}
}

func (p *Provider) ID() string          { return "ec2" }
func (p *Provider) DisplayName() string { return "EC2" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type ec2API interface {
	DescribeInstances(ctx context.Context, params *sdkec2.DescribeInstancesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInstancesOutput, error)
	DescribeVpcs(ctx context.Context, params *sdkec2.DescribeVpcsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVpcsOutput, error)
	DescribeSubnets(ctx context.Context, params *sdkec2.DescribeSubnetsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *sdkec2.DescribeSecurityGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSecurityGroupsOutput, error)
	DescribeVolumes(ctx context.Context, params *sdkec2.DescribeVolumesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeVolumesOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("ec2 provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("ec2 provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region

		nodes, edges, err := p.listRegion(ctx, p.newEC2(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api ec2API, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()

	var (
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
	)

	// Populate base network primitives first so relationship jumps have rich targets.
	vpcs, err := api.DescribeVpcs(ctx, &sdkec2.DescribeVpcsInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, v := range vpcs.Vpcs {
		nodes = append(nodes, normalizeVPC(partition, accountID, region, v, now))
	}
	subnets, err := api.DescribeSubnets(ctx, &sdkec2.DescribeSubnetsInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, s := range subnets.Subnets {
		n := normalizeSubnet(partition, accountID, region, s, now)
		nodes = append(nodes, n)
		if vpcID := awsToString(s.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			edges = append(edges, graph.RelationshipEdge{
				From:        n.Key,
				To:          vpcKey,
				Kind:        "member-of",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
	}
	sgs, err := api.DescribeSecurityGroups(ctx, &sdkec2.DescribeSecurityGroupsInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, sg := range sgs.SecurityGroups {
		n := normalizeSecurityGroup(partition, accountID, region, sg, now)
		nodes = append(nodes, n)
		if vpcID := awsToString(sg.VpcId); vpcID != "" {
			vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
			edges = append(edges, graph.RelationshipEdge{
				From:        n.Key,
				To:          vpcKey,
				Kind:        "member-of",
				Meta:        map[string]any{"direct": true},
				CollectedAt: now,
			})
		}
	}

	var nextToken *string
	for {
		out, err := api.DescribeInstances(ctx, &sdkec2.DescribeInstancesInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, nil, err
		}

		for _, r := range out.Reservations {
			for _, inst := range r.Instances {
				n, ns, es := normalizeInstance(partition, accountID, region, inst, now)
				nodes = append(nodes, n)
				// Keep stubs for referenced resources not found via list calls (rare, but cheap).
				nodes = append(nodes, ns...)
				edges = append(edges, es...)
			}
		}

		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Volumes (EBS). This is a major cost driver and provides important topology edges to instances and KMS keys.
	nextToken = nil
	for {
		out, err := api.DescribeVolumes(ctx, &sdkec2.DescribeVolumesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, v := range out.Volumes {
			n, stubs, es := normalizeVolume(partition, accountID, region, v, now)
			nodes = append(nodes, n)
			nodes = append(nodes, stubs...)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	return nodes, edges, nil
}

func normalizeInstance(partition, accountID, region string, inst types.Instance, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	instanceID := awsToString(inst.InstanceId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", instanceID)

	name := instanceID
	for _, t := range inst.Tags {
		if awsToString(t.Key) == "Name" && awsToString(t.Value) != "" {
			name = awsToString(t.Value)
			break
		}
	}

	tags := map[string]string{}
	for _, t := range inst.Tags {
		k := awsToString(t.Key)
		v := awsToString(t.Value)
		if k != "" {
			tags[k] = v
		}
	}

	attrs := map[string]any{
		"state":        instanceState(inst),
		"instanceType": string(inst.InstanceType),
		"az":           instanceAZ(inst),
		"privateIp":    awsToString(inst.PrivateIpAddress),
		"publicIp":     awsToString(inst.PublicIpAddress),
	}
	// Best-effort fields for cost estimation.
	// Platform is usually nil for Linux; set a stable "os" value for estimators.
	os := "linux"
	if strings.EqualFold(string(inst.Platform), "windows") {
		os = "windows"
	}
	attrs["os"] = os
	tenancy := strings.ToLower(strings.TrimSpace(string(inst.Placement.Tenancy)))
	switch tenancy {
	case "", "default":
		tenancy = "shared"
	}
	attrs["tenancy"] = tenancy
	if inst.LaunchTime != nil {
		attrs["created_at"] = inst.LaunchTime.UTC().Format("2006-01-02 15:04")
	}

	raw, _ := json.Marshal(inst)
	arn := ""
	// EC2 instance ARN format:
	// arn:{partition}:ec2:{region}:{account}:instance/{instance-id}
	if instanceID != "" {
		arn = fmt.Sprintf("arn:%s:ec2:%s:%s:instance/%s", partition, region, accountID, instanceID)
	}

	node := graph.ResourceNode{
		Key:         key,
		DisplayName: name,
		Service:     "ec2",
		Type:        "ec2:instance",
		Arn:         arn,
		PrimaryID:   instanceID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}

	var stubNodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// instance -> vpc/subnet
	if vpcID := awsToString(inst.VpcId); vpcID != "" {
		vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if subnetID := awsToString(inst.SubnetId); subnetID != "" {
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	// instance -> security groups
	for _, sg := range inst.SecurityGroups {
		sgID := awsToString(sg.GroupId)
		if sgID == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}

	return node, stubNodes, edges
}

func normalizeVolume(partition, accountID, region string, v types.Volume, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	volID := awsToString(v.VolumeId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:volume", volID)

	tags := map[string]string{}
	for _, t := range v.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}

	display := volID
	if n := tags["Name"]; n != "" {
		display = n
	}

	attrs := map[string]any{
		"state":      string(v.State),
		"az":         awsToString(v.AvailabilityZone),
		"volumeType": string(v.VolumeType),
		"sizeGb":     v.Size,
		"iops":       v.Iops,
		"throughput": v.Throughput,
		"encrypted":  v.Encrypted != nil && *v.Encrypted,
	}
	if v.CreateTime != nil && !v.CreateTime.IsZero() {
		attrs["created_at"] = v.CreateTime.UTC().Format("2006-01-02 15:04")
	}
	if snap := awsToString(v.SnapshotId); snap != "" {
		attrs["snapshotId"] = snap
	}
	if v.MultiAttachEnabled != nil {
		attrs["multiAttachEnabled"] = *v.MultiAttachEnabled
	}
	if kms := strings.TrimSpace(awsToString(v.KmsKeyId)); kms != "" {
		attrs["kmsKeyId"] = kms
	}

	raw, _ := json.Marshal(v)
	arn := ""
	if volID != "" {
		arn = fmt.Sprintf("arn:%s:ec2:%s:%s:volume/%s", partition, region, accountID, volID)
	}
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:volume",
		Arn:         arn,
		PrimaryID:   volID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge

	// volume -> instance (attachments)
	for _, a := range v.Attachments {
		instID := awsToString(a.InstanceId)
		if instID == "" {
			continue
		}
		instKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", instID)
		meta := map[string]any{"direct": true}
		if d := awsToString(a.Device); d != "" {
			meta["device"] = d
		}
		if a.State != "" {
			meta["state"] = string(a.State)
		}
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          instKey,
			Kind:        "attached-to",
			Meta:        meta,
			CollectedAt: now,
		})
	}

	// volume -> kms (if encrypted)
	if v.Encrypted != nil && *v.Encrypted {
		if kms := strings.TrimSpace(awsToString(v.KmsKeyId)); kms != "" {
			toKey, ok := kmsRefToKey(partition, accountID, region, kms)
			if ok {
				edges = append(edges, graph.RelationshipEdge{
					From:        key,
					To:          toKey,
					Kind:        "uses",
					Meta:        map[string]any{"direct": true, "source": "ec2.ebs.kms"},
					CollectedAt: now,
				})
			}
		}
	}

	return node, stubs, edges
}

func normalizeVPC(partition, accountID, region string, v types.Vpc, now time.Time) graph.ResourceNode {
	vpcID := awsToString(v.VpcId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)

	tags := map[string]string{}
	for _, t := range v.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := vpcID
	if n := tags["Name"]; n != "" {
		display = n
	}
	raw, _ := json.Marshal(v)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:vpc",
		Arn:         "",
		PrimaryID:   vpcID,
		Tags:        tags,
		Attributes: map[string]any{
			"cidr":         awsToString(v.CidrBlock),
			"isDefaultVpc": v.IsDefault,
			"state":        string(v.State),
		},
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
}

func normalizeSubnet(partition, accountID, region string, s types.Subnet, now time.Time) graph.ResourceNode {
	subnetID := awsToString(s.SubnetId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)

	tags := map[string]string{}
	for _, t := range s.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := subnetID
	if n := tags["Name"]; n != "" {
		display = n
	}
	raw, _ := json.Marshal(s)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:subnet",
		Arn:         "",
		PrimaryID:   subnetID,
		Tags:        tags,
		Attributes: map[string]any{
			"cidr": awsToString(s.CidrBlock),
			"az":   awsToString(s.AvailabilityZone),
			"vpc":  awsToString(s.VpcId),
			"state": func() string {
				if s.State == "" {
					return ""
				}
				return string(s.State)
			}(),
		},
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
}

func normalizeSecurityGroup(partition, accountID, region string, sg types.SecurityGroup, now time.Time) graph.ResourceNode {
	sgID := awsToString(sg.GroupId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)

	tags := map[string]string{}
	for _, t := range sg.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := awsToString(sg.GroupName)
	if display == "" {
		display = sgID
	}
	raw, _ := json.Marshal(sg)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:security-group",
		Arn:         "",
		PrimaryID:   sgID,
		Tags:        tags,
		Attributes: map[string]any{
			"vpc":         awsToString(sg.VpcId),
			"description": awsToString(sg.Description),
		},
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
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

func instanceState(inst types.Instance) string {
	if inst.State == nil {
		return ""
	}
	return string(inst.State.Name)
}

func instanceAZ(inst types.Instance) string {
	if inst.Placement == nil {
		return ""
	}
	return awsToString(inst.Placement.AvailabilityZone)
}
