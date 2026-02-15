package graph

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ResourceKey is a canonical, stable identifier for a resource node.
// Encoding is url-escaped components joined by '|':
//
//	partition|account_id|region|resource_type|primary_id
type ResourceKey string

func EncodeResourceKey(partition, accountID, region, resourceType, primaryID string) ResourceKey {
	parts := []string{
		url.PathEscape(partition),
		url.PathEscape(accountID),
		url.PathEscape(region),
		url.PathEscape(resourceType),
		url.PathEscape(primaryID),
	}
	return ResourceKey(strings.Join(parts, "|"))
}

func ParseResourceKey(k ResourceKey) (partition, accountID, region, resourceType, primaryID string, err error) {
	parts := strings.Split(string(k), "|")
	if len(parts) != 5 {
		return "", "", "", "", "", fmt.Errorf("invalid resource key (expected 5 parts): %q", string(k))
	}
	unesc := func(s string) (string, error) { return url.PathUnescape(s) }
	if partition, err = unesc(parts[0]); err != nil {
		return
	}
	if accountID, err = unesc(parts[1]); err != nil {
		return
	}
	if region, err = unesc(parts[2]); err != nil {
		return
	}
	if resourceType, err = unesc(parts[3]); err != nil {
		return
	}
	if primaryID, err = unesc(parts[4]); err != nil {
		return
	}
	return
}

type ResourceNode struct {
	Key         ResourceKey
	DisplayName string
	Service     string
	Type        string
	Arn         string
	PrimaryID   string
	Tags        map[string]string
	Attributes  map[string]any
	Raw         json.RawMessage
	CollectedAt time.Time
	Source      string
}

type RelationshipEdge struct {
	From        ResourceKey
	To          ResourceKey
	Kind        string
	Meta        map[string]any
	CollectedAt time.Time
}
