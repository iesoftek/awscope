package ec2

import (
	"awscope/internal/graph"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

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
