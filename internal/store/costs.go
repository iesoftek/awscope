package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"awscope/internal/graph"
)

type PricingCacheRow struct {
	CacheKey    string
	Partition   string
	ServiceCode string
	PriceKind   string
	AWSRegion   string
	Location    string
	FiltersJSON string
	Unit        string
	USD         *float64
	RawJSON     string
	RetrievedAt time.Time
}

type ResourceCostRow struct {
	ResourceKey   graph.ResourceKey
	AccountID     string
	Partition     string
	Region        string
	Service       string
	Type          string
	EstMonthlyUSD *float64
	Currency      string
	Basis         string
	Breakdown     map[string]any
	ComputedAt    time.Time
	Source        string
}

type CostAgg struct {
	Key          string
	Count        int
	KnownUSD     float64
	UnknownCount int
}

func (s *Store) UpsertPricingCache(ctx context.Context, rows []PricingCacheRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO pricing_cache(
  cache_key, partition, service_code, price_kind, aws_region, location,
  filters_json, unit, usd, raw_json, retrieved_at
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?
)
ON CONFLICT(cache_key) DO UPDATE SET
  partition=excluded.partition,
  service_code=excluded.service_code,
  price_kind=excluded.price_kind,
  aws_region=excluded.aws_region,
  location=excluded.location,
  filters_json=excluded.filters_json,
  unit=excluded.unit,
  usd=excluded.usd,
  raw_json=excluded.raw_json,
  retrieved_at=excluded.retrieved_at
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		if strings.TrimSpace(r.CacheKey) == "" {
			continue
		}
		raw := r.RawJSON
		if strings.TrimSpace(raw) == "" {
			raw = "{}"
		}
		filters := r.FiltersJSON
		if strings.TrimSpace(filters) == "" {
			filters = "{}"
		}
		unit := r.Unit
		if unit == "" {
			unit = "-"
		}
		currencyUSD := any(nil)
		if r.USD != nil {
			currencyUSD = *r.USD
		}
		t := r.RetrievedAt
		if t.IsZero() {
			t = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(
			ctx,
			r.CacheKey,
			strings.TrimSpace(r.Partition),
			strings.TrimSpace(r.ServiceCode),
			strings.TrimSpace(r.PriceKind),
			strings.TrimSpace(r.AWSRegion),
			strings.TrimSpace(r.Location),
			filters,
			unit,
			currencyUSD,
			raw,
			t.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetPricingCache(ctx context.Context, cacheKey string) (PricingCacheRow, bool, error) {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return PricingCacheRow{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
  cache_key, partition, service_code, price_kind, aws_region, location,
  filters_json, unit, usd, raw_json, retrieved_at
FROM pricing_cache
WHERE cache_key = ?
`, cacheKey)

	var (
		r         PricingCacheRow
		usd       sql.NullFloat64
		retrieved string
	)
	if err := row.Scan(
		&r.CacheKey, &r.Partition, &r.ServiceCode, &r.PriceKind, &r.AWSRegion, &r.Location,
		&r.FiltersJSON, &r.Unit, &usd, &r.RawJSON, &retrieved,
	); err != nil {
		if err == sql.ErrNoRows {
			return PricingCacheRow{}, false, nil
		}
		return PricingCacheRow{}, false, err
	}
	if usd.Valid {
		v := usd.Float64
		r.USD = &v
	}
	t, err := time.Parse(time.RFC3339Nano, retrieved)
	if err == nil {
		r.RetrievedAt = t
	}
	return r, true, nil
}

func (s *Store) UpsertResourceCosts(ctx context.Context, rows []ResourceCostRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO resource_costs(
  resource_key, account_id, partition, region, service, type,
  est_monthly_usd, currency, basis, breakdown_json,
  computed_at, source
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  ?, ?
)
ON CONFLICT(resource_key) DO UPDATE SET
  account_id=excluded.account_id,
  partition=excluded.partition,
  region=excluded.region,
  service=excluded.service,
  type=excluded.type,
  est_monthly_usd=excluded.est_monthly_usd,
  currency=excluded.currency,
  basis=excluded.basis,
  breakdown_json=excluded.breakdown_json,
  computed_at=excluded.computed_at,
  source=excluded.source
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, r := range rows {
		if strings.TrimSpace(string(r.ResourceKey)) == "" {
			continue
		}
		breakdownJSON, err := json.Marshal(orEmptyAnyMap(r.Breakdown))
		if err != nil {
			return err
		}
		usd := any(nil)
		if r.EstMonthlyUSD != nil {
			usd = *r.EstMonthlyUSD
		}
		currency := strings.TrimSpace(r.Currency)
		if currency == "" {
			currency = "USD"
		}
		basis := strings.TrimSpace(r.Basis)
		if basis == "" {
			basis = "unknown"
		}
		computedAt := r.ComputedAt
		if computedAt.IsZero() {
			computedAt = now
		}
		source := strings.TrimSpace(r.Source)
		if source == "" {
			source = "scan"
		}

		if _, err := stmt.ExecContext(
			ctx,
			string(r.ResourceKey),
			strings.TrimSpace(r.AccountID),
			strings.TrimSpace(r.Partition),
			strings.TrimSpace(r.Region),
			strings.TrimSpace(r.Service),
			strings.TrimSpace(r.Type),
			usd,
			currency,
			basis,
			string(breakdownJSON),
			computedAt.UTC().Format(time.RFC3339Nano),
			source,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetResourceCost(ctx context.Context, key graph.ResourceKey) (ResourceCostRow, bool, error) {
	if strings.TrimSpace(string(key)) == "" {
		return ResourceCostRow{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
  resource_key, account_id, partition, region, service, type,
  est_monthly_usd, currency, basis, breakdown_json,
  computed_at, source
FROM resource_costs
WHERE resource_key = ?
`, string(key))

	var (
		out           ResourceCostRow
		usd           sql.NullFloat64
		breakdownJSON string
		computedAt    string
	)
	var keyStr string
	if err := row.Scan(
		&keyStr, &out.AccountID, &out.Partition, &out.Region, &out.Service, &out.Type,
		&usd, &out.Currency, &out.Basis, &breakdownJSON,
		&computedAt, &out.Source,
	); err != nil {
		if err == sql.ErrNoRows {
			return ResourceCostRow{}, false, nil
		}
		return ResourceCostRow{}, false, err
	}
	out.ResourceKey = graph.ResourceKey(keyStr)
	if usd.Valid {
		v := usd.Float64
		out.EstMonthlyUSD = &v
	}
	_ = json.Unmarshal([]byte(breakdownJSON), &out.Breakdown)
	if t, err := time.Parse(time.RFC3339Nano, computedAt); err == nil {
		out.ComputedAt = t
	}
	return out, true, nil
}

func (s *Store) ListServiceCostAggByRegions(ctx context.Context, accountID string, regions []string) ([]CostAgg, error) {
	return s.listCostAgg(ctx, accountID, regions, true, "")
}

func (s *Store) ListTypeCostAggByServiceAndRegions(ctx context.Context, accountID, service string, regions []string) ([]CostAgg, error) {
	return s.listCostAgg(ctx, accountID, regions, false, service)
}

func (s *Store) listCostAgg(ctx context.Context, accountID string, regions []string, groupByService bool, service string) ([]CostAgg, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}

	args := []any{accountID}
	clauses := []string{"r.account_id = ?"}

	if strings.TrimSpace(service) != "" {
		clauses = append(clauses, "r.service = ?")
		args = append(args, strings.TrimSpace(service))
	}

	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, r := range regions {
			holders = append(holders, "?")
			args = append(args, r)
		}
		clauses = append(clauses, fmt.Sprintf("r.region IN (%s)", strings.Join(holders, ",")))
	}

	keyExpr := "r.service"
	orderExpr := "r.service"
	if !groupByService {
		keyExpr = "r.type"
		orderExpr = "r.type"
	}

	q := fmt.Sprintf(`
SELECT
  %s AS k,
  COUNT(1) AS cnt,
  SUM(COALESCE(c.est_monthly_usd, 0)) AS known_usd,
  SUM(CASE WHEN c.est_monthly_usd IS NULL THEN 1 ELSE 0 END) AS unknown_cnt
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE %s
GROUP BY k
ORDER BY %s ASC
`, keyExpr, strings.Join(clauses, " AND "), orderExpr)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CostAgg
	for rows.Next() {
		var ca CostAgg
		if err := rows.Scan(&ca.Key, &ca.Count, &ca.KnownUSD, &ca.UnknownCount); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ca.Key) != "" {
			out = append(out, ca)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type CostIndexTarget struct {
	Key        graph.ResourceKey
	AccountID  string
	Partition  string
	Region     string
	Service    string
	Type       string
	Attributes map[string]any
	UpdatedAt  time.Time
}

func (s *Store) ListCostIndexTargets(ctx context.Context, accountID string, service string, regions []string) ([]CostIndexTarget, error) {
	accountID = strings.TrimSpace(accountID)
	service = strings.TrimSpace(service)
	if accountID == "" || service == "" {
		return nil, nil
	}

	args := []any{accountID, service}
	clauses := []string{"r.account_id = ?", "r.service = ?"}
	if len(regions) > 0 {
		holders := make([]string, 0, len(regions))
		for _, rr := range regions {
			holders = append(holders, "?")
			args = append(args, rr)
		}
		clauses = append(clauses, fmt.Sprintf("r.region IN (%s)", strings.Join(holders, ",")))
	}

	q := fmt.Sprintf(`
SELECT
  r.resource_key, r.account_id, r.partition, r.region, r.service, r.type,
  r.attributes_json, r.updated_at
FROM resources r
LEFT JOIN resource_costs c ON c.resource_key = r.resource_key
WHERE %s
  AND (c.resource_key IS NULL OR c.computed_at < r.updated_at)
ORDER BY r.updated_at ASC
`, strings.Join(clauses, " AND "))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CostIndexTarget
	for rows.Next() {
		var (
			keyStr       string
			attrsJSON    string
			updatedAtStr string
			t            CostIndexTarget
		)
		if err := rows.Scan(&keyStr, &t.AccountID, &t.Partition, &t.Region, &t.Service, &t.Type, &attrsJSON, &updatedAtStr); err != nil {
			return nil, err
		}
		t.Key = graph.ResourceKey(keyStr)
		_ = json.Unmarshal([]byte(attrsJSON), &t.Attributes)
		if tt, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
			t.UpdatedAt = tt
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
