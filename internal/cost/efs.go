package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

// efsFileSystemEstimator estimates EFS storage-only monthly cost from current bytes.
// It intentionally excludes request/throughput and lifecycle-class split details.
type efsFileSystemEstimator struct{}

func (efsFileSystemEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}

	sizeBytes := anyFloat(t.Attributes["sizeBytes"])
	if sizeBytes <= 0 {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing sizeBytes"}}, nil
	}
	sizeGiB := sizeBytes / float64(1024*1024*1024)

	candidates := make([]map[string]string, 0, 4)
	if prefix, ok := pricing.RegionToUsagePrefix(t.Region); ok {
		candidates = append(candidates,
			map[string]string{"usagetype": prefix + "-TimedStorage-ByteHrs"},
			map[string]string{"usagetype": prefix + "-IATimedStorage-ByteHrs"},
		)
	}
	candidates = append(candidates,
		map[string]string{"productFamily": "Storage"},
		map[string]string{},
	)

	var price pricing.Price
	for _, filters := range candidates {
		p, err := pc.Lookup(ctx, pricing.Query{
			Partition:   t.Partition,
			ServiceCode: "AmazonEFS",
			PriceKind:   "efs_storage_gb_month",
			AWSRegion:   t.Region,
			Location:    loc,
			Filters:     filters,
		})
		if err != nil {
			return Result{}, err
		}
		price = p
		if p.USD != nil {
			break
		}
	}
	if price.USD == nil {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":    "pricing not found",
				"sizeBytes": sizeBytes,
				"sizeGiB":   sizeGiB,
				"location":  loc,
			},
		}, nil
	}

	usdPerGBMo := *price.USD
	monthly := sizeGiB * usdPerGBMo
	return Result{
		USD:   &monthly,
		Basis: "efs.storage_only",
		Breakdown: map[string]any{
			"sizeBytes":        sizeBytes,
			"sizeGiB":          sizeGiB,
			"usd_per_gb_month": usdPerGBMo,
			"unit":             strings.TrimSpace(price.Unit),
			"location":         loc,
			"note":             "storage only; excludes throughput, requests, lifecycle mix and backup features",
		},
	}, nil
}
