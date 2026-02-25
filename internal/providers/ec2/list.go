package ec2

import (
	"awscope/internal/graph"
	"context"
	"time"

	sdkec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
)

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
