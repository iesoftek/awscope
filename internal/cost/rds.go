package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

type rdsInstanceEstimator struct{}

func (rdsInstanceEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}

	engine := strings.TrimSpace(anyString(t.Attributes["engine"]))
	class := strings.TrimSpace(anyString(t.Attributes["class"]))
	deployment := strings.TrimSpace(anyString(t.Attributes["deployment"]))

	deploymentOpt := "Single-AZ"
	if deployment == "multi-az" {
		deploymentOpt = "Multi-AZ"
	}
	if engine == "" || class == "" {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing engine/class"}}, nil
	}

	enginePricing := rdsEngineToPricingEngine(engine)
	if enginePricing == "" {
		enginePricing = engine
	}

	var price pricing.Price
	var err error
	candidates := []map[string]string{
		{"productFamily": "Database Instance", "databaseEngine": enginePricing, "instanceType": class, "deploymentOption": deploymentOpt},
		{"databaseEngine": enginePricing, "instanceType": class, "deploymentOption": deploymentOpt},
		{"productFamily": "Database Instance", "databaseEngine": enginePricing, "instanceClass": class, "deploymentOption": deploymentOpt},
		{"databaseEngine": enginePricing, "instanceClass": class, "deploymentOption": deploymentOpt},
		{"instanceType": class, "deploymentOption": deploymentOpt},
		{"instanceClass": class, "deploymentOption": deploymentOpt},
	}
	for _, f := range candidates {
		price, err = pc.Lookup(ctx, pricing.Query{
			Partition:   t.Partition,
			ServiceCode: "AmazonRDS",
			PriceKind:   "rds_ondemand_hourly",
			AWSRegion:   t.Region,
			Location:    loc,
			Filters:     f,
		})
		if err != nil {
			return Result{}, err
		}
		if price.USD != nil {
			break
		}
	}
	if price.USD == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing not found", "engine": engine, "enginePricing": enginePricing, "class": class, "deployment": deploymentOpt}}, nil
	}

	hourly := *price.USD
	computeMonthly := hourly * float64(DefaultHoursPerMonth)

	// Best-effort: add storage GB-month when we can resolve it.
	storageGb := anyNumber(t.Attributes["allocatedStorageGb"])
	storageType := strings.TrimSpace(anyString(t.Attributes["storageType"]))

	breakdown := map[string]any{
		"hourly_usd":  hourly,
		"hours":       DefaultHoursPerMonth,
		"engine":      engine,
		"enginePrice": enginePricing,
		"class":       class,
		"deployment":  deploymentOpt,
		"location":    loc,
		"unit_hourly": strings.TrimSpace(price.Unit),
	}

	// If we don't have storage size, return compute-only.
	if storageGb <= 0 {
		monthly := computeMonthly
		breakdown["note"] = "compute only; allocatedStorageGb missing/zero"
		return Result{USD: &monthly, Basis: "rds.on_demand_compute_only", Breakdown: breakdown}, nil
	}

	// Try a few filter shapes; Pricing attributes vary by engine/storage type.
	storageFilters := make([]map[string]string, 0, 6)
	if storageType != "" {
		storageFilters = append(storageFilters,
			map[string]string{
				"productFamily":    "Database Storage",
				"deploymentOption": deploymentOpt,
				"storageType":      storageType,
			},
			map[string]string{
				"productFamily":    "Database Storage",
				"deploymentOption": deploymentOpt,
				"volumeType":       storageType,
			},
		)
	}
	// Some RDS storage products expose engine as an attribute; include as a fallback.
	if engine != "" {
		storageFilters = append(storageFilters, map[string]string{
			"productFamily":    "Database Storage",
			"deploymentOption": deploymentOpt,
			"databaseEngine":   engine,
		})
	}
	storageFilters = append(storageFilters,
		map[string]string{
			"productFamily":    "Database Storage",
			"deploymentOption": deploymentOpt,
		},
		map[string]string{
			"productFamily":    "Storage",
			"deploymentOption": deploymentOpt,
		},
	)

	var storageUSDPerGBMo *float64
	var storageUnit string
	for _, f := range storageFilters {
		p, err := pc.Lookup(ctx, pricing.Query{
			Partition:   t.Partition,
			ServiceCode: "AmazonRDS",
			PriceKind:   "rds_storage_gb_month",
			AWSRegion:   t.Region,
			Location:    loc,
			Filters:     f,
		})
		if err != nil {
			return Result{}, err
		}
		if p.USD != nil {
			storageUSDPerGBMo = p.USD
			storageUnit = strings.TrimSpace(p.Unit)
			break
		}
	}

	if storageUSDPerGBMo == nil {
		monthly := computeMonthly
		breakdown["allocatedStorageGb"] = storageGb
		if storageType != "" {
			breakdown["storageType"] = storageType
		}
		breakdown["note"] = "compute only; storage pricing not found"
		return Result{USD: &monthly, Basis: "rds.on_demand_compute_only", Breakdown: breakdown}, nil
	}

	storageMonthly := (*storageUSDPerGBMo) * float64(storageGb)
	monthly := computeMonthly + storageMonthly
	breakdown["allocatedStorageGb"] = storageGb
	if storageType != "" {
		breakdown["storageType"] = storageType
	}
	breakdown["usd_per_gb_month"] = *storageUSDPerGBMo
	breakdown["unit_storage"] = storageUnit

	return Result{
		USD:       &monthly,
		Basis:     "rds.on_demand_plus_storage",
		Breakdown: breakdown,
	}, nil
}

func rdsEngineToPricingEngine(engine string) string {
	e := strings.ToLower(strings.TrimSpace(engine))
	switch e {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "mariadb":
		return "MariaDB"
	case "aurora-postgresql":
		return "Aurora PostgreSQL"
	case "aurora-mysql":
		return "Aurora MySQL"
	default:
		// Many engines already match pricing names; leave as-is.
		return strings.TrimSpace(engine)
	}
}
