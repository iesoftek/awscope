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
	DescribeImages(ctx context.Context, params *sdkec2.DescribeImagesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeImagesOutput, error)
	DescribeSnapshots(ctx context.Context, params *sdkec2.DescribeSnapshotsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeSnapshotsOutput, error)
	DescribeInternetGateways(ctx context.Context, params *sdkec2.DescribeInternetGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeInternetGatewaysOutput, error)
	DescribePlacementGroups(ctx context.Context, params *sdkec2.DescribePlacementGroupsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribePlacementGroupsOutput, error)
	DescribeLaunchTemplates(ctx context.Context, params *sdkec2.DescribeLaunchTemplatesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplatesOutput, error)
	DescribeLaunchTemplateVersions(ctx context.Context, params *sdkec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeLaunchTemplateVersionsOutput, error)
	DescribeNatGateways(ctx context.Context, params *sdkec2.DescribeNatGatewaysInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNatGatewaysOutput, error)
	DescribeAddresses(ctx context.Context, params *sdkec2.DescribeAddressesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeAddressesOutput, error)
	DescribeRouteTables(ctx context.Context, params *sdkec2.DescribeRouteTablesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeRouteTablesOutput, error)
	DescribeNetworkAcls(ctx context.Context, params *sdkec2.DescribeNetworkAclsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkAclsOutput, error)
	DescribeNetworkInterfaces(ctx context.Context, params *sdkec2.DescribeNetworkInterfacesInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeNetworkInterfacesOutput, error)
	DescribeKeyPairs(ctx context.Context, params *sdkec2.DescribeKeyPairsInput, optFns ...func(*sdkec2.Options)) (*sdkec2.DescribeKeyPairsOutput, error)
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

	// AMIs (owned by this account).
	nextToken = nil
	for {
		out, err := api.DescribeImages(ctx, &sdkec2.DescribeImagesInput{
			Owners:    []string{"self"},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, img := range out.Images {
			n, es := normalizeImage(partition, accountID, region, img, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// EBS snapshots (owned by this account).
	nextToken = nil
	for {
		out, err := api.DescribeSnapshots(ctx, &sdkec2.DescribeSnapshotsInput{
			OwnerIds:  []string{"self"},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, snap := range out.Snapshots {
			n, es := normalizeSnapshot(partition, accountID, region, snap, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Internet gateways.
	nextToken = nil
	for {
		out, err := api.DescribeInternetGateways(ctx, &sdkec2.DescribeInternetGatewaysInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, igw := range out.InternetGateways {
			n, es := normalizeInternetGateway(partition, accountID, region, igw, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Placement groups.
	pgOut, err := api.DescribePlacementGroups(ctx, &sdkec2.DescribePlacementGroupsInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, pg := range pgOut.PlacementGroups {
		n := normalizePlacementGroup(partition, accountID, region, pg, now)
		nodes = append(nodes, n)
	}

	// Launch templates + latest version details for relationship extraction.
	nextToken = nil
	for {
		out, err := api.DescribeLaunchTemplates(ctx, &sdkec2.DescribeLaunchTemplatesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, lt := range out.LaunchTemplates {
			n, es := normalizeLaunchTemplateMeta(partition, accountID, region, lt, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)

			ltID := awsToString(lt.LaunchTemplateId)
			if ltID == "" {
				continue
			}
			verOut, err := api.DescribeLaunchTemplateVersions(ctx, &sdkec2.DescribeLaunchTemplateVersionsInput{
				LaunchTemplateId: &ltID,
				Versions:         []string{"$Latest"},
			})
			if err != nil {
				continue
			}
			for _, v := range verOut.LaunchTemplateVersions {
				ves := normalizeLaunchTemplateVersionEdges(partition, accountID, region, ltID, v, now)
				edges = append(edges, ves...)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// NAT gateways.
	nextToken = nil
	for {
		out, err := api.DescribeNatGateways(ctx, &sdkec2.DescribeNatGatewaysInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, ngw := range out.NatGateways {
			n, es := normalizeNatGateway(partition, accountID, region, ngw, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Elastic IP addresses.
	addrOut, err := api.DescribeAddresses(ctx, &sdkec2.DescribeAddressesInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, a := range addrOut.Addresses {
		n, es := normalizeAddress(partition, accountID, region, a, now)
		nodes = append(nodes, n)
		edges = append(edges, es...)
	}

	// Route tables.
	nextToken = nil
	for {
		out, err := api.DescribeRouteTables(ctx, &sdkec2.DescribeRouteTablesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, rt := range out.RouteTables {
			n, es := normalizeRouteTable(partition, accountID, region, rt, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Network ACLs.
	nextToken = nil
	for {
		out, err := api.DescribeNetworkAcls(ctx, &sdkec2.DescribeNetworkAclsInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, nacl := range out.NetworkAcls {
			n, es := normalizeNetworkACL(partition, accountID, region, nacl, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// ENIs.
	nextToken = nil
	for {
		out, err := api.DescribeNetworkInterfaces(ctx, &sdkec2.DescribeNetworkInterfacesInput{NextToken: nextToken})
		if err != nil {
			return nil, nil, err
		}
		for _, eni := range out.NetworkInterfaces {
			n, es := normalizeNetworkInterface(partition, accountID, region, eni, now)
			nodes = append(nodes, n)
			edges = append(edges, es...)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	// Key pairs.
	keyOut, err := api.DescribeKeyPairs(ctx, &sdkec2.DescribeKeyPairsInput{})
	if err != nil {
		return nil, nil, err
	}
	for _, kp := range keyOut.KeyPairs {
		nodes = append(nodes, normalizeKeyPair(partition, accountID, region, kp, now))
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
	if keyName := strings.TrimSpace(awsToString(inst.KeyName)); keyName != "" {
		kpKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:key-pair", keyName)
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          kpKey,
			Kind:        "uses",
			Meta:        map[string]any{"direct": true},
			CollectedAt: now,
		})
		attrs["keyName"] = keyName
	}
	if pgName := strings.TrimSpace(awsToString(inst.Placement.GroupName)); pgName != "" {
		pgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:placement-group", pgName)
		edges = append(edges, graph.RelationshipEdge{
			From:        pgKey,
			To:          key,
			Kind:        "contains",
			Meta:        map[string]any{"direct": true},
			CollectedAt: now,
		})
		attrs["placementGroup"] = pgName
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

func normalizeImage(partition, accountID, region string, img types.Image, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	imageID := awsToString(img.ImageId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:ami", imageID)

	tags := map[string]string{}
	for _, t := range img.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := strings.TrimSpace(awsToString(img.Name))
	if display == "" {
		display = imageID
	}

	attrs := map[string]any{
		"state":            string(img.State),
		"owner":            awsToString(img.OwnerId),
		"platformDetails":  awsToString(img.PlatformDetails),
		"rootDeviceType":   string(img.RootDeviceType),
		"virtualization":   string(img.VirtualizationType),
		"architecture":     string(img.Architecture),
		"isPublic":         boolFromPtr(img.Public),
		"imageOwnerAlias":  awsToString(img.ImageOwnerAlias),
		"deprecationTime":  awsToString(img.DeprecationTime),
		"deregistrationAt": awsToString(img.DeregistrationProtection),
	}
	if cd := strings.TrimSpace(awsToString(img.CreationDate)); cd != "" {
		attrs["created_at"] = cd
	}
	raw, _ := json.Marshal(img)
	arn := ""
	if imageID != "" {
		arn = fmt.Sprintf("arn:%s:ec2:%s:%s:image/%s", partition, region, accountID, imageID)
	}
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:ami",
		Arn:         arn,
		PrimaryID:   imageID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}

	var edges []graph.RelationshipEdge
	for _, bdm := range img.BlockDeviceMappings {
		if bdm.Ebs == nil {
			continue
		}
		snapID := strings.TrimSpace(awsToString(bdm.Ebs.SnapshotId))
		if snapID == "" {
			continue
		}
		sKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:snapshot", snapID)
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          sKey,
			Kind:        "uses",
			Meta:        map[string]any{"direct": true, "source": "ec2.ami.bdm"},
			CollectedAt: now,
		})
	}
	return node, edges
}

func normalizeSnapshot(partition, accountID, region string, snap types.Snapshot, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	snapID := awsToString(snap.SnapshotId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:snapshot", snapID)
	tags := map[string]string{}
	for _, t := range snap.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := snapID
	if n := tags["Name"]; n != "" {
		display = n
	}
	attrs := map[string]any{
		"state":      string(snap.State),
		"volumeId":   awsToString(snap.VolumeId),
		"volumeSize": snap.VolumeSize,
		"encrypted":  boolFromPtr(snap.Encrypted),
		"owner":      awsToString(snap.OwnerId),
		"progress":   awsToString(snap.Progress),
	}
	if snap.StartTime != nil && !snap.StartTime.IsZero() {
		attrs["created_at"] = snap.StartTime.UTC().Format("2006-01-02 15:04")
	}
	if kms := strings.TrimSpace(awsToString(snap.KmsKeyId)); kms != "" {
		attrs["kmsKeyId"] = kms
	}
	raw, _ := json.Marshal(snap)
	arn := ""
	if snapID != "" {
		arn = fmt.Sprintf("arn:%s:ec2:%s:%s:snapshot/%s", partition, region, accountID, snapID)
	}
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:snapshot",
		Arn:         arn,
		PrimaryID:   snapID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if volID := strings.TrimSpace(awsToString(snap.VolumeId)); volID != "" {
		volKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:volume", volID)
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          volKey,
			Kind:        "uses",
			Meta:        map[string]any{"direct": true},
			CollectedAt: now,
		})
	}
	if kms := strings.TrimSpace(awsToString(snap.KmsKeyId)); kms != "" {
		if toKey, ok := kmsRefToKey(partition, accountID, region, kms); ok {
			edges = append(edges, graph.RelationshipEdge{
				From:        key,
				To:          toKey,
				Kind:        "uses",
				Meta:        map[string]any{"direct": true, "source": "ec2.snapshot.kms"},
				CollectedAt: now,
			})
		}
	}
	return node, edges
}

func normalizeInternetGateway(partition, accountID, region string, igw types.InternetGateway, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	igwID := awsToString(igw.InternetGatewayId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:internet-gateway", igwID)
	tags := map[string]string{}
	for _, t := range igw.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := igwID
	if n := tags["Name"]; n != "" {
		display = n
	}
	attrs := map[string]any{
		"attachments": len(igw.Attachments),
	}
	raw, _ := json.Marshal(igw)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:internet-gateway",
		PrimaryID:   igwID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	for _, a := range igw.Attachments {
		vpcID := strings.TrimSpace(awsToString(a.VpcId))
		if vpcID == "" {
			continue
		}
		vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          vpcKey,
			Kind:        "attached-to",
			Meta:        map[string]any{"direct": true, "state": string(a.State)},
			CollectedAt: now,
		})
	}
	return node, edges
}

func normalizePlacementGroup(partition, accountID, region string, pg types.PlacementGroup, now time.Time) graph.ResourceNode {
	pgName := strings.TrimSpace(awsToString(pg.GroupName))
	primary := pgName
	if primary == "" {
		primary = strings.TrimSpace(awsToString(pg.GroupId))
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:placement-group", primary)
	attrs := map[string]any{
		"state":          string(pg.State),
		"strategy":       string(pg.Strategy),
		"partitionCount": pg.PartitionCount,
		"groupId":        awsToString(pg.GroupId),
		"spreadLevel":    string(pg.SpreadLevel),
	}
	raw, _ := json.Marshal(pg)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: primary,
		Service:     "ec2",
		Type:        "ec2:placement-group",
		PrimaryID:   primary,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
}

func normalizeLaunchTemplateMeta(partition, accountID, region string, lt types.LaunchTemplate, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	ltID := strings.TrimSpace(awsToString(lt.LaunchTemplateId))
	name := strings.TrimSpace(awsToString(lt.LaunchTemplateName))
	primary := ltID
	if primary == "" {
		primary = name
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:launch-template", primary)
	attrs := map[string]any{
		"launchTemplateId":   ltID,
		"defaultVersion":     lt.DefaultVersionNumber,
		"latestVersion":      lt.LatestVersionNumber,
		"createdBy":          awsToString(lt.CreatedBy),
		"operatorManaged":    boolFromPtr(lt.Operator.Managed),
		"operatorPrincipal":  awsToString(lt.Operator.Principal),
		"launchTemplateName": name,
	}
	if lt.CreateTime != nil && !lt.CreateTime.IsZero() {
		attrs["created_at"] = lt.CreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(lt)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(name, ltID),
		Service:     "ec2",
		Type:        "ec2:launch-template",
		PrimaryID:   primary,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	return node, nil
}

func normalizeLaunchTemplateVersionEdges(partition, accountID, region, ltID string, v types.LaunchTemplateVersion, now time.Time) []graph.RelationshipEdge {
	if ltID == "" || v.LaunchTemplateData == nil {
		return nil
	}
	ltKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:launch-template", ltID)
	d := v.LaunchTemplateData
	var edges []graph.RelationshipEdge
	if ami := strings.TrimSpace(awsToString(d.ImageId)); ami != "" {
		amiKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:ami", ami)
		edges = append(edges, graph.RelationshipEdge{From: ltKey, To: amiKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if kp := strings.TrimSpace(awsToString(d.KeyName)); kp != "" {
		kpKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:key-pair", kp)
		edges = append(edges, graph.RelationshipEdge{From: ltKey, To: kpKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, sgID := range d.SecurityGroupIds {
		sg := strings.TrimSpace(sgID)
		if sg == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sg)
		edges = append(edges, graph.RelationshipEdge{From: ltKey, To: sgKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if d.IamInstanceProfile != nil {
		roleRef := strings.TrimSpace(awsToString(d.IamInstanceProfile.Arn))
		if roleRef != "" {
			roleKey := graph.EncodeResourceKey(partition, accountID, "global", "iam:role", roleRef)
			edges = append(edges, graph.RelationshipEdge{From: ltKey, To: roleKey, Kind: "uses", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}
	return edges
}

func normalizeNatGateway(partition, accountID, region string, nat types.NatGateway, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	natID := awsToString(nat.NatGatewayId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:nat-gateway", natID)
	attrs := map[string]any{
		"state":            string(nat.State),
		"connectivityType": string(nat.ConnectivityType),
		"subnetId":         awsToString(nat.SubnetId),
		"vpcId":            awsToString(nat.VpcId),
	}
	if nat.CreateTime != nil && !nat.CreateTime.IsZero() {
		attrs["created_at"] = nat.CreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(nat)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: natID,
		Service:     "ec2",
		Type:        "ec2:nat-gateway",
		PrimaryID:   natID,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if subnetID := strings.TrimSpace(awsToString(nat.SubnetId)); subnetID != "" {
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if vpcID := strings.TrimSpace(awsToString(nat.VpcId)); vpcID != "" {
		vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, a := range nat.NatGatewayAddresses {
		allocID := strings.TrimSpace(awsToString(a.AllocationId))
		if allocID == "" {
			continue
		}
		eipKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:eip", allocID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: eipKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	return node, edges
}

func normalizeAddress(partition, accountID, region string, a types.Address, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	primary := strings.TrimSpace(awsToString(a.AllocationId))
	if primary == "" {
		primary = strings.TrimSpace(awsToString(a.PublicIp))
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:eip", primary)
	tags := map[string]string{}
	for _, t := range a.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := primary
	if name := tags["Name"]; name != "" {
		display = name
	}
	attrs := map[string]any{
		"allocationId":       awsToString(a.AllocationId),
		"associationId":      awsToString(a.AssociationId),
		"publicIp":           awsToString(a.PublicIp),
		"privateIp":          awsToString(a.PrivateIpAddress),
		"networkInterfaceId": awsToString(a.NetworkInterfaceId),
		"instanceId":         awsToString(a.InstanceId),
		"domain":             string(a.Domain),
	}
	raw, _ := json.Marshal(a)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:eip",
		PrimaryID:   primary,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if eniID := strings.TrimSpace(awsToString(a.NetworkInterfaceId)); eniID != "" {
		eniKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:network-interface", eniID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: eniKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if instID := strings.TrimSpace(awsToString(a.InstanceId)); instID != "" {
		instKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", instID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: instKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	return node, edges
}

func normalizeRouteTable(partition, accountID, region string, rt types.RouteTable, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	rtID := awsToString(rt.RouteTableId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:route-table", rtID)
	tags := map[string]string{}
	for _, t := range rt.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := rtID
	if n := tags["Name"]; n != "" {
		display = n
	}
	attrs := map[string]any{
		"vpc":          awsToString(rt.VpcId),
		"associations": len(rt.Associations),
		"routes":       len(rt.Routes),
	}
	raw, _ := json.Marshal(rt)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:route-table",
		PrimaryID:   rtID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if vpcID := strings.TrimSpace(awsToString(rt.VpcId)); vpcID != "" {
		vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, assoc := range rt.Associations {
		subnetID := strings.TrimSpace(awsToString(assoc.SubnetId))
		if subnetID == "" {
			continue
		}
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, r := range rt.Routes {
		toKey, ok := routeTargetKey(partition, accountID, region, r)
		if !ok {
			continue
		}
		edges = append(edges, graph.RelationshipEdge{
			From:        key,
			To:          toKey,
			Kind:        "targets",
			Meta:        map[string]any{"direct": true, "destinationCidr": awsToString(r.DestinationCidrBlock)},
			CollectedAt: now,
		})
	}
	return node, edges
}

func normalizeNetworkACL(partition, accountID, region string, acl types.NetworkAcl, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	aclID := awsToString(acl.NetworkAclId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:nacl", aclID)
	tags := map[string]string{}
	for _, t := range acl.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := aclID
	if n := tags["Name"]; n != "" {
		display = n
	}
	attrs := map[string]any{
		"vpc":       awsToString(acl.VpcId),
		"isDefault": boolFromPtr(acl.IsDefault),
		"entries":   len(acl.Entries),
	}
	raw, _ := json.Marshal(acl)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:nacl",
		PrimaryID:   aclID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if vpcID := strings.TrimSpace(awsToString(acl.VpcId)); vpcID != "" {
		vpcKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:vpc", vpcID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: vpcKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, a := range acl.Associations {
		subnetID := strings.TrimSpace(awsToString(a.SubnetId))
		if subnetID == "" {
			continue
		}
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "contains", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	return node, edges
}

func normalizeNetworkInterface(partition, accountID, region string, eni types.NetworkInterface, now time.Time) (graph.ResourceNode, []graph.RelationshipEdge) {
	eniID := awsToString(eni.NetworkInterfaceId)
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:network-interface", eniID)
	tags := map[string]string{}
	for _, t := range eni.TagSet {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	display := eniID
	if n := tags["Name"]; n != "" {
		display = n
	}
	attrs := map[string]any{
		"status":        string(eni.Status),
		"interfaceType": string(eni.InterfaceType),
		"privateIp":     awsToString(eni.PrivateIpAddress),
		"subnet":        awsToString(eni.SubnetId),
		"vpc":           awsToString(eni.VpcId),
	}
	if eni.Attachment != nil {
		attrs["attachmentStatus"] = string(eni.Attachment.Status)
	}
	raw, _ := json.Marshal(eni)
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     "ec2",
		Type:        "ec2:network-interface",
		PrimaryID:   eniID,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
	var edges []graph.RelationshipEdge
	if subnetID := strings.TrimSpace(awsToString(eni.SubnetId)); subnetID != "" {
		subnetKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:subnet", subnetID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: subnetKey, Kind: "member-of", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	for _, sg := range eni.Groups {
		sgID := strings.TrimSpace(awsToString(sg.GroupId))
		if sgID == "" {
			continue
		}
		sgKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:security-group", sgID)
		edges = append(edges, graph.RelationshipEdge{From: key, To: sgKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
	}
	if eni.Attachment != nil {
		instID := strings.TrimSpace(awsToString(eni.Attachment.InstanceId))
		if instID != "" {
			instKey := graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", instID)
			edges = append(edges, graph.RelationshipEdge{From: key, To: instKey, Kind: "attached-to", Meta: map[string]any{"direct": true}, CollectedAt: now})
		}
	}
	return node, edges
}

func normalizeKeyPair(partition, accountID, region string, kp types.KeyPairInfo, now time.Time) graph.ResourceNode {
	keyName := strings.TrimSpace(awsToString(kp.KeyName))
	primary := keyName
	if primary == "" {
		primary = strings.TrimSpace(awsToString(kp.KeyPairId))
	}
	key := graph.EncodeResourceKey(partition, accountID, region, "ec2:key-pair", primary)
	tags := map[string]string{}
	for _, t := range kp.Tags {
		k := awsToString(t.Key)
		if k == "" {
			continue
		}
		tags[k] = awsToString(t.Value)
	}
	attrs := map[string]any{
		"keyPairId":      awsToString(kp.KeyPairId),
		"keyFingerprint": awsToString(kp.KeyFingerprint),
		"keyType":        string(kp.KeyType),
	}
	if kp.CreateTime != nil && !kp.CreateTime.IsZero() {
		attrs["created_at"] = kp.CreateTime.UTC().Format("2006-01-02 15:04")
	}
	raw, _ := json.Marshal(kp)
	return graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(keyName, primary),
		Service:     "ec2",
		Type:        "ec2:key-pair",
		PrimaryID:   primary,
		Tags:        tags,
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "ec2",
	}
}

func routeTargetKey(partition, accountID, region string, r types.Route) (graph.ResourceKey, bool) {
	if v := strings.TrimSpace(awsToString(r.GatewayId)); strings.HasPrefix(v, "igw-") {
		return graph.EncodeResourceKey(partition, accountID, region, "ec2:internet-gateway", v), true
	}
	if v := strings.TrimSpace(awsToString(r.NatGatewayId)); v != "" {
		return graph.EncodeResourceKey(partition, accountID, region, "ec2:nat-gateway", v), true
	}
	if v := strings.TrimSpace(awsToString(r.NetworkInterfaceId)); v != "" {
		return graph.EncodeResourceKey(partition, accountID, region, "ec2:network-interface", v), true
	}
	if v := strings.TrimSpace(awsToString(r.InstanceId)); v != "" {
		return graph.EncodeResourceKey(partition, accountID, region, "ec2:instance", v), true
	}
	return "", false
}

func boolFromPtr[T ~bool](v *T) bool {
	if v == nil {
		return false
	}
	return bool(*v)
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
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
