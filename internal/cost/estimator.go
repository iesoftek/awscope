package cost

import (
	"context"
	"strings"
	"time"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

const DefaultHoursPerMonth = 730

type Result struct {
	USD       *float64
	Basis     string
	Breakdown map[string]any
}

type Estimator interface {
	Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error)
}

func Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (store.ResourceCostRow, Result, error) {
	now := time.Now().UTC()
	res := store.ResourceCostRow{
		ResourceKey: t.Key,
		AccountID:   t.AccountID,
		Partition:   t.Partition,
		Region:      t.Region,
		Service:     t.Service,
		Type:        t.Type,
		Currency:    "USD",
		Basis:       "unknown",
		Breakdown:   map[string]any{},
		ComputedAt:  now,
		Source:      "scan",
	}

	est := registryEstimator(strings.TrimSpace(t.Type))
	if est == nil {
		res.Basis = "unknown"
		res.Breakdown = map[string]any{"reason": "usage_based_or_not_supported"}
		return res, Result{USD: nil, Basis: res.Basis, Breakdown: res.Breakdown}, nil
	}

	r, err := est.Estimate(ctx, t, pc)
	if err != nil {
		// Best-effort: keep unknown row. Caller can decide whether to record failures.
		res.Basis = "unknown"
		res.Breakdown = map[string]any{"error": err.Error()}
		return res, Result{USD: nil, Basis: res.Basis, Breakdown: res.Breakdown}, err
	}
	res.EstMonthlyUSD = r.USD
	res.Basis = strings.TrimSpace(r.Basis)
	if res.Basis == "" {
		res.Basis = "unknown"
	}
	if r.Breakdown != nil {
		res.Breakdown = r.Breakdown
	}
	return res, r, nil
}
