package ec2

import (
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

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
