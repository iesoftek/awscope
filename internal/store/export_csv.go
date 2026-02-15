package store

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type ExportResourcesCSVOptions struct {
	// AccountID scopes the export to a single AWS account. When empty, all accounts are exported.
	AccountID string
	// IncludeProfileColumn adds a "profile" column (best-effort derived from the profiles table).
	// Intended for the "export all" case.
	IncludeProfileColumn bool
}

func (s *Store) ExportResourcesCSV(ctx context.Context, w io.Writer, opts ExportResourcesCSVOptions) error {
	accountID := strings.TrimSpace(opts.AccountID)
	includeProfileCol := opts.IncludeProfileColumn && accountID == ""

	profilesByAccount := map[string][]string{}
	if includeProfileCol {
		// Best-effort mapping; resources are account-scoped, not profile-scoped.
		rows, err := s.ListProfiles(ctx)
		if err == nil {
			for _, r := range rows {
				if strings.TrimSpace(r.AccountID) == "" || strings.TrimSpace(r.ProfileName) == "" {
					continue
				}
				profilesByAccount[r.AccountID] = append(profilesByAccount[r.AccountID], r.ProfileName)
			}
			for k := range profilesByAccount {
				sort.Strings(profilesByAccount[k])
			}
		}
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	header := []string{
		"account_id",
		"partition",
		"region",
		"service",
		"type",
		"display_name",
		"primary_id",
		"arn",
		"status",
		"created_at",
		"collected_at",
		"updated_at",
		"est_monthly_usd",
		"cost_basis",
		"tags_json",
		"attributes_json",
		"raw_json",
		"resource_key",
	}
	if includeProfileCol {
		header = append([]string{"profile"}, header...)
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	q := `
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.arn, r.primary_id, r.display_name,
  r.tags_json, r.attributes_json, r.raw_json,
  r.collected_at, r.updated_at,
  c.est_monthly_usd, c.basis
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key AND c.account_id = r.account_id
`
	args := []any{}
	if accountID != "" {
		q += "WHERE r.account_id = ?\n"
		args = append(args, accountID)
	}
	q += "ORDER BY r.account_id, r.service, r.type, r.region, r.display_name\n"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			resourceKey string
			accID       string
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
			estUSD      sql.NullFloat64
			costBasis   sql.NullString
		)
		if err := rows.Scan(
			&resourceKey, &accID, &partition, &region, &service, &typ,
			&arn, &primaryID, &displayName,
			&tagsJSON, &attrsJSON, &rawJSON,
			&collectedAt, &updatedAt,
			&estUSD, &costBasis,
		); err != nil {
			return err
		}

		createdAt := ""
		status := ""
		var attrs map[string]any
		_ = json.Unmarshal([]byte(attrsJSON), &attrs)
		if v, ok := attrs["created_at"].(string); ok {
			createdAt = strings.TrimSpace(v)
		}
		status = statusFromAttrs(attrs)

		estStr := ""
		if estUSD.Valid {
			estStr = fmt.Sprintf("%.6f", estUSD.Float64)
		}

		outRow := []string{
			accID,
			partition,
			region,
			service,
			typ,
			displayName,
			primaryID,
			arn.String,
			status,
			createdAt,
			collectedAt,
			updatedAt,
			estStr,
			costBasis.String,
			tagsJSON,
			attrsJSON,
			rawJSON,
			resourceKey,
		}
		if includeProfileCol {
			p := strings.Join(profilesByAccount[accID], ",")
			outRow = append([]string{p}, outRow...)
		}
		if err := cw.Write(outRow); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	cw.Flush()
	return cw.Error()
}

func statusFromAttrs(attrs map[string]any) string {
	if attrs == nil {
		return ""
	}
	// Common across our providers:
	// - EC2 instances use "state"
	// - ECS/Lambda/RDS and others use "status"
	// Keep this conservative; callers can still inspect attributes_json for exact fields.
	for _, k := range []string{"state", "status"} {
		if v, ok := attrs[k]; ok {
			switch vv := v.(type) {
			case string:
				return strings.TrimSpace(vv)
			}
		}
	}
	return ""
}
