package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

type secretsManagerSecretEstimator struct{}

func (secretsManagerSecretEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}

	price, err := pc.Lookup(ctx, pricing.Query{
		Partition:   t.Partition,
		ServiceCode: "AWSSecretsManager",
		PriceKind:   "secretsmanager_secret_month",
		AWSRegion:   t.Region,
		Location:    loc,
		Filters: map[string]string{
			"productFamily": "Secrets",
		},
	})
	if err != nil {
		return Result{}, err
	}
	if price.USD == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing not found"}}, nil
	}

	monthly := *price.USD
	return Result{
		USD:   &monthly,
		Basis: "secretsmanager.secret_month",
		Breakdown: map[string]any{
			"monthly_usd": monthly,
			"location":    loc,
			"unit":        strings.TrimSpace(price.Unit),
			"note":        "secret storage only; API calls excluded",
		},
	}, nil
}
