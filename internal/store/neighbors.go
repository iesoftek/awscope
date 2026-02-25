package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"awscope/internal/graph"
)

type Neighbor struct {
	Kind        string
	OtherKey    graph.ResourceKey
	Dir         string // "out" or "in"
	Meta        map[string]any
	DisplayName string
	Service     string
	Type        string
	Region      string
	PrimaryID   string
	Arn         string
}

func (s *Store) ListNeighbors(ctx context.Context, key graph.ResourceKey) ([]Neighbor, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  kind,
  other_key,
  dir,
  meta_json,
  display_name,
  service,
  type,
  region,
  primary_id,
  arn
FROM (
  SELECT
    e.kind AS kind,
    e.to_key AS other_key,
    'out' AS dir,
    e.meta_json AS meta_json,
    r.display_name AS display_name,
    r.service AS service,
    r.type AS type,
    r.region AS region,
    r.primary_id AS primary_id,
    r.arn AS arn
  FROM edges e
  LEFT JOIN resources r ON r.resource_key = e.to_key AND r.lifecycle_state = 'active'
  WHERE e.from_key = ?

  UNION ALL

  SELECT
    e.kind AS kind,
    e.from_key AS other_key,
    'in' AS dir,
    e.meta_json AS meta_json,
    r.display_name AS display_name,
    r.service AS service,
    r.type AS type,
    r.region AS region,
    r.primary_id AS primary_id,
    r.arn AS arn
  FROM edges e
  LEFT JOIN resources r ON r.resource_key = e.from_key AND r.lifecycle_state = 'active'
  WHERE e.to_key = ?
)
`, string(key), string(key))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Neighbor
	for rows.Next() {
		var (
			kind        string
			otherKeyStr string
			dir         string
			metaJSON    string

			displayName sql.NullString
			service     sql.NullString
			typ         sql.NullString
			region      sql.NullString
			primaryID   sql.NullString
			arn         sql.NullString
		)

		if err := rows.Scan(
			&kind,
			&otherKeyStr,
			&dir,
			&metaJSON,
			&displayName,
			&service,
			&typ,
			&region,
			&primaryID,
			&arn,
		); err != nil {
			return nil, err
		}

		meta := map[string]any{}
		_ = json.Unmarshal([]byte(metaJSON), &meta)

		n := Neighbor{
			Kind:        kind,
			OtherKey:    graph.ResourceKey(otherKeyStr),
			Dir:         dir,
			Meta:        meta,
			DisplayName: displayName.String,
			Service:     service.String,
			Type:        typ.String,
			Region:      region.String,
			PrimaryID:   primaryID.String,
			Arn:         arn.String,
		}
		fillNeighborFromKey(&n)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].DisplayName < out[j].DisplayName
	})

	return out, nil
}

func fillNeighborFromKey(n *Neighbor) {
	if n == nil || n.OtherKey == "" {
		return
	}
	_, _, region, resourceType, primaryID, err := graph.ParseResourceKey(n.OtherKey)
	if err != nil {
		// Best effort: if we can't parse, at least show something.
		if n.DisplayName == "" {
			n.DisplayName = string(n.OtherKey)
		}
		return
	}

	if n.Region == "" {
		n.Region = region
	}
	if n.Type == "" {
		n.Type = resourceType
	}
	if n.Service == "" {
		if parts := strings.SplitN(resourceType, ":", 2); len(parts) > 0 {
			n.Service = parts[0]
		}
	}
	if n.PrimaryID == "" {
		n.PrimaryID = primaryID
	}
	if n.DisplayName == "" {
		if primaryID != "" {
			n.DisplayName = primaryID
		} else {
			n.DisplayName = string(n.OtherKey)
		}
	}
}
