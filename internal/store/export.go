package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"time"
)

type ExportSnapshot struct {
	ExportedAt time.Time        `json:"exported_at"`
	Resources  []ExportResource `json:"resources"`
	Edges      []ExportEdge     `json:"edges"`
}

type ExportResource struct {
	ResourceKey    string            `json:"resource_key"`
	AccountID      string            `json:"account_id"`
	Partition      string            `json:"partition"`
	Region         string            `json:"region"`
	Service        string            `json:"service"`
	Type           string            `json:"type"`
	Arn            string            `json:"arn,omitempty"`
	PrimaryID      string            `json:"primary_id"`
	DisplayName    string            `json:"display_name"`
	Tags           map[string]string `json:"tags"`
	Attributes     map[string]any    `json:"attributes"`
	Raw            json.RawMessage   `json:"raw"`
	CollectedAtRFC string            `json:"collected_at"`
	UpdatedAtRFC   string            `json:"updated_at"`
}

type ExportEdge struct {
	FromKey        string         `json:"from_key"`
	ToKey          string         `json:"to_key"`
	Kind           string         `json:"kind"`
	Meta           map[string]any `json:"meta"`
	CollectedAtRFC string         `json:"collected_at"`
}

func (s *Store) ExportLatest(ctx context.Context) (ExportSnapshot, error) {
	resources, err := s.exportResources(ctx)
	if err != nil {
		return ExportSnapshot{}, err
	}
	edges, err := s.exportEdges(ctx)
	if err != nil {
		return ExportSnapshot{}, err
	}
	return ExportSnapshot{
		ExportedAt: time.Now().UTC(),
		Resources:  resources,
		Edges:      edges,
	}, nil
}

func (s *Store) exportResources(ctx context.Context) ([]ExportResource, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  resource_key, account_id, partition, region, service, type,
  arn, primary_id, display_name,
  tags_json, attributes_json, raw_json,
  collected_at, updated_at
FROM resources
ORDER BY resource_key
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExportResource
	for rows.Next() {
		var (
			keyStr      string
			accountID   string
			partition   string
			region      string
			service     string
			typ         string
			arn         sql.NullString
			primaryID   string
			displayName string
			tagsJSON    string
			attrsJSON   string
			rawJSON     string
			collectedAt string
			updatedAt   string
		)
		if err := rows.Scan(
			&keyStr, &accountID, &partition, &region, &service, &typ,
			&arn, &primaryID, &displayName,
			&tagsJSON, &attrsJSON, &rawJSON,
			&collectedAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		var tags map[string]string
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
		var attrs map[string]any
		_ = json.Unmarshal([]byte(attrsJSON), &attrs)

		out = append(out, ExportResource{
			ResourceKey:    keyStr,
			AccountID:      accountID,
			Partition:      partition,
			Region:         region,
			Service:        service,
			Type:           typ,
			Arn:            arn.String,
			PrimaryID:      primaryID,
			DisplayName:    displayName,
			Tags:           tags,
			Attributes:     attrs,
			Raw:            json.RawMessage(rawJSON),
			CollectedAtRFC: collectedAt,
			UpdatedAtRFC:   updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) exportEdges(ctx context.Context) ([]ExportEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT from_key, to_key, kind, meta_json, collected_at
FROM edges
ORDER BY from_key, kind, to_key
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExportEdge
	for rows.Next() {
		var fromKey, toKey, kind, metaJSON, collectedAt string
		if err := rows.Scan(&fromKey, &toKey, &kind, &metaJSON, &collectedAt); err != nil {
			return nil, err
		}
		meta := map[string]any{}
		_ = json.Unmarshal([]byte(metaJSON), &meta)
		out = append(out, ExportEdge{
			FromKey:        fromKey,
			ToKey:          toKey,
			Kind:           kind,
			Meta:           meta,
			CollectedAtRFC: collectedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func WriteJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
