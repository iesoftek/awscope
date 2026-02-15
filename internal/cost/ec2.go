package cost

import (
	"context"
	"fmt"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

type ec2InstanceEstimator struct{}

func (ec2InstanceEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	attrs := t.Attributes

	state := strings.ToLower(strings.TrimSpace(anyString(attrs["state"])))
	instanceType := strings.TrimSpace(anyString(attrs["instanceType"]))
	if instanceType == "" {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing instanceType"}}, nil
	}

	// OS and tenancy are best-effort. Defaults align with the most common case.
	os := strings.ToLower(strings.TrimSpace(anyString(attrs["os"])))
	if os == "" {
		os = "linux"
	}
	ten := strings.ToLower(strings.TrimSpace(anyString(attrs["tenancy"])))
	if ten == "" {
		ten = "shared"
	}

	// If not running, we treat compute cost as 0 (EBS not accounted for in v1).
	if state != "running" {
		z := 0.0
		return Result{
			USD:   &z,
			Basis: "ec2.on_demand_compute",
			Breakdown: map[string]any{
				"hourly_usd": 0,
				"hours":      DefaultHoursPerMonth,
				"state":      state,
				"os":         os,
				"tenancy":    ten,
				"note":       "compute only; stopped instances assumed $0 (EBS excluded)",
			},
		}, nil
	}

	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}

	operatingSystem := "Linux"
	if os == "windows" {
		operatingSystem = "Windows"
	}

	tenancy := "Shared"
	switch ten {
	case "dedicated":
		tenancy = "Dedicated"
	case "host":
		tenancy = "Host"
	}

	baseFilters := map[string]string{
		"instanceType":    instanceType,
		"operatingSystem": operatingSystem,
		"tenancy":         tenancy,
		"preInstalledSw":  "NA",
		"capacitystatus":  "Used",
	}

	// Try a stricter query first (licenseModel), then fall back if it returns no price.
	filters := copyFilters(baseFilters)
	if operatingSystem == "Linux" {
		filters["licenseModel"] = "No License required"
	} else {
		filters["licenseModel"] = "License included"
	}

	price, err := pc.Lookup(ctx, pricing.Query{
		Partition:   t.Partition,
		ServiceCode: "AmazonEC2",
		PriceKind:   "ec2_ondemand_hourly",
		AWSRegion:   t.Region,
		Location:    loc,
		Filters:     filters,
	})
	if err != nil {
		return Result{}, err
	}
	if price.USD == nil {
		// Retry without licenseModel (it can vary by product family).
		price, err = pc.Lookup(ctx, pricing.Query{
			Partition:   t.Partition,
			ServiceCode: "AmazonEC2",
			PriceKind:   "ec2_ondemand_hourly",
			AWSRegion:   t.Region,
			Location:    loc,
			Filters:     baseFilters,
		})
		if err != nil {
			return Result{}, err
		}
	}
	if price.USD == nil {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":       "pricing not found",
				"instanceType": instanceType,
				"os":           operatingSystem,
				"tenancy":      tenancy,
			},
		}, nil
	}

	hourly := *price.USD
	monthly := hourly * float64(DefaultHoursPerMonth)
	return Result{
		USD:   &monthly,
		Basis: "ec2.on_demand_compute",
		Breakdown: map[string]any{
			"hourly_usd":   hourly,
			"hours":        DefaultHoursPerMonth,
			"state":        state,
			"instanceType": instanceType,
			"os":           operatingSystem,
			"tenancy":      tenancy,
			"location":     loc,
			"unit":         price.Unit,
		},
	}, nil
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}

func copyFilters(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
