package ec2

import (
	"awscope/internal/graph"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

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
