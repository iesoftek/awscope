package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

// auroraClusterEstimator estimates Aurora Serverless monthly cost from configuration (best-effort).
//
// We only model the baseline ACU-hours using the configured minimum capacity; actual bills depend on
// utilization. This gives a conservative floor that is often "directionally right" for rough estimates.
type auroraClusterEstimator struct{}

func (auroraClusterEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	engine := strings.ToLower(strings.TrimSpace(anyString(t.Attributes["engine"])))
	if !strings.HasPrefix(engine, "aurora") {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "non-aurora cluster", "engine": engine}}, nil
	}

	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}
	prefix, _ := pricing.RegionToUsagePrefix(t.Region)

	// Identify serverless flavor from cluster attributes.
	engineMode := strings.ToLower(strings.TrimSpace(anyString(t.Attributes["engineMode"])))
	v2Min := anyFloat(t.Attributes["serverlessV2MinAcu"])
	v2Max := anyFloat(t.Attributes["serverlessV2MaxAcu"])
	v1Min := anyFloat(t.Attributes["serverlessV1MinAcu"])
	v1Max := anyFloat(t.Attributes["serverlessV1MaxAcu"])
	storageType := strings.ToLower(strings.TrimSpace(anyString(t.Attributes["storageType"])))

	isV2 := v2Min > 0 || v2Max > 0
	isV1 := engineMode == "serverless" || v1Min > 0 || v1Max > 0
	if !isV2 && !isV1 {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "not serverless", "engineMode": engineMode}}, nil
	}

	// Use configured minimum ACU as a conservative baseline.
	assumedACU := v2Min
	basis := "aurora.serverless_v2_min_acu"
	if isV1 {
		assumedACU = v1Min
		basis = "aurora.serverless_v1_min_acu"
	}
	if assumedACU <= 0 {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":  "missing min ACU",
				"isV2":    isV2,
				"isV1":    isV1,
				"v2Min":   v2Min,
				"v1Min":   v1Min,
				"engine":  engine,
				"region":  t.Region,
				"location": loc,
			},
		}, nil
	}

	// Pricing: per ACU-hour.
	//
	// We try Serverless v2 usage first when v2 is detected, but in some regions the standard
	// ServerlessUsage SKU applies to v2 as well. us-east-1 often omits the region prefix.
	usagetypeCandidates := func(s string) []string {
		out := []string{s}
		if prefix != "" {
			out = append([]string{prefix + "-" + s}, out...)
		}
		return out
	}

	usageTry := []string{}
	if isV2 {
		// If the cluster is I/O-Optimized (storageType is commonly "aurora-iopt1"), use that SKU.
		if strings.Contains(storageType, "iopt") {
			usageTry = append(usageTry, "Aurora:ServerlessV2IOOptimizedUsage")
		} else {
			usageTry = append(usageTry, "Aurora:ServerlessV2Usage")
		}
		// Fallbacks (some regions only expose this).
		usageTry = append(usageTry, "Aurora:ServerlessUsage")
	} else {
		usageTry = append(usageTry, "Aurora:ServerlessUsage")
	}

	var price pricing.Price
	for _, baseUT := range usageTry {
		for _, ut := range usagetypeCandidates(baseUT) {
			p, err := pc.Lookup(ctx, pricing.Query{
				Partition:   t.Partition,
				ServiceCode: "AmazonRDS",
				PriceKind:   "aurora_acu_hour",
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
		if price.USD != nil {
			break
		}
	}
	if price.USD == nil {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":     "pricing not found",
				"engine":     engine,
				"engineMode": engineMode,
				"location":   loc,
				"usagePref":  prefix,
				"unit":       price.Unit,
			},
		}, nil
	}

	usdPerACUHr := *price.USD
	hours := float64(DefaultHoursPerMonth)
	monthly := usdPerACUHr * assumedACU * hours

	return Result{
		USD:   &monthly,
		Basis: basis,
		Breakdown: map[string]any{
			"assumption":          "min_acu_baseline",
			"assumedAcu":          assumedACU,
			"hours":               DefaultHoursPerMonth,
			"usd_per_acu_hour":    usdPerACUHr,
			"unit":                strings.TrimSpace(price.Unit),
			"engine":              engine,
			"engineMode":          engineMode,
			"serverlessV2MinAcu":  v2Min,
			"serverlessV2MaxAcu":  v2Max,
			"serverlessV1MinAcu":  v1Min,
			"serverlessV1MaxAcu":  v1Max,
			"storageType":         storageType,
			"location":            loc,
			"usagePref":           prefix,
			"note":                "Aurora Serverless is usage-driven; estimate uses configured minimum ACU only (floor), excludes storage and I/O charges",
		},
	}, nil
}

