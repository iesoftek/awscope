package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

type logsLogGroupEstimator struct{}

func (logsLogGroupEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}
	prefix, ok := pricing.RegionToUsagePrefix(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing usage prefix", "region": t.Region}}, nil
	}

	// storedBytes comes from DescribeLogGroups (approximate current storage).
	storedBytes := anyFloat(t.Attributes["storedBytes"])
	if storedBytes <= 0 {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing storedBytes"}}, nil
	}
	gb := storedBytes / float64(1024*1024*1024)

	// CloudWatch Logs storage is exposed as TimedStorage-ByteHrs and priced per GB-Mo.
	// Try IA first if the log group class indicates it; fall back to standard.
	lgClass := strings.ToLower(strings.TrimSpace(anyString(t.Attributes["class"])))
	usagetypeTry := []string{prefix + "-TimedStorage-ByteHrs"}
	if strings.Contains(lgClass, "infrequent") || strings.Contains(lgClass, "ia") {
		usagetypeTry = append([]string{prefix + "-TimedStorageIA-ByteHrs"}, usagetypeTry...)
	}

	var price pricing.Price
	for _, ut := range usagetypeTry {
		p, err := pc.Lookup(ctx, pricing.Query{
			Partition:   t.Partition,
			ServiceCode: "AmazonCloudWatch",
			PriceKind:   "cwlogs_storage_gb_month",
			AWSRegion:   t.Region,
			Location:    loc,
			Filters: map[string]string{
				"usagetype": ut,
			},
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
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing not found", "location": loc, "usagePref": prefix, "unit": price.Unit}}, nil
	}

	usdPerGBMo := *price.USD
	monthly := gb * usdPerGBMo

	return Result{
		USD:   &monthly,
		Basis: "cwlogs.storage_only",
		Breakdown: map[string]any{
			"storedBytes":       storedBytes,
			"storedGiB":         gb,
			"usd_per_gb_month":  usdPerGBMo,
			"unit":              strings.TrimSpace(price.Unit),
			"location":          loc,
			"usagePref":         prefix,
			"class":             lgClass,
			"note":              "storage only; excludes ingestion, insights queries, vended logs, data protection, and other usage-based charges",
		},
	}, nil
}

