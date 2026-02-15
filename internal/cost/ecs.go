package cost

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

// ecsServiceEstimator estimates ECS service cost for Fargate (best-effort).
// For non-Fargate services, cost is usage/cluster dependent (EC2 capacity), so we report unknown.
type ecsServiceEstimator struct{}

func (ecsServiceEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	launchType := strings.ToUpper(strings.TrimSpace(anyString(t.Attributes["launchType"])))
	if launchType == "" {
		// Capacity providers can also drive launch type; we don't model that yet.
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing launchType"}}, nil
	}
	if launchType != "FARGATE" {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "non-fargate service (cost on underlying compute)", "launchType": launchType}}, nil
	}
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

	desired := anyNumber(t.Attributes["desiredCount"])
	vcpu := anyFloat(t.Attributes["vcpu"])
	memGB := anyFloat(t.Attributes["memoryGb"])
	if desired <= 0 || vcpu <= 0 || memGB <= 0 {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":       "missing desiredCount/vcpu/memoryGb (need task definition details)",
				"desiredCount": desired,
				"vcpu":         vcpu,
				"memoryGb":     memGB,
			},
		}, nil
	}

	// Fargate pricing is split by vCPU-hours and GB-hours.
	// Pricing API returns many ECS products; to avoid poisoning the cache (and wildly inflated costs),
	// we constrain to exact "usagetype" values for Fargate compute.
	//
	// Examples:
	// - USE2-Fargate-vCPU-Hours
	// - USE2-Fargate-GB-Hours
	//
	// In some regions/usagetype sets, the prefix may be omitted for historical reasons; we try both.
	usagetypeCandidates := func(suffix string) []string {
		out := []string{
			prefix + "-" + suffix,
			suffix,
		}
		// Avoid duplicates when prefix already empty (shouldn't happen with our mapping).
		if out[0] == out[1] {
			return out[:1]
		}
		return out
	}

	lookup := func(kind string, usagetypeSuffixes ...string) (pricing.Price, error) {
		var last pricing.Price
		for _, suffix := range usagetypeSuffixes {
			for _, ut := range usagetypeCandidates(suffix) {
				p, err := pc.Lookup(ctx, pricing.Query{
					Partition:   t.Partition,
					ServiceCode: "AmazonECS",
					PriceKind:   kind,
					AWSRegion:   t.Region,
					Location:    loc,
					Filters: map[string]string{
						"productFamily": "Compute",
						"usagetype":     ut,
					},
				})
				if err != nil {
					return pricing.Price{}, err
				}
				last = p
				if p.USD != nil {
					return p, nil
				}
			}
		}
		return last, nil
	}

	vcpuP, err := lookup("fargate_vcpu_hour", "Fargate-vCPU-Hours:perCPU", "Fargate-vCPU-Hours")
	if err != nil {
		return Result{}, err
	}
	memP, err := lookup("fargate_gb_hour", "Fargate-GB-Hours")
	if err != nil {
		return Result{}, err
	}
	if vcpuP.USD == nil || memP.USD == nil {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":     "pricing not found",
				"location":   loc,
				"usagePref":  prefix,
				"unitVCPU":   vcpuP.Unit,
				"unitMem":    memP.Unit,
			},
		}, nil
	}

	hours := float64(DefaultHoursPerMonth)
	usdPerVCPUHr := normalizePerHour(*vcpuP.USD, vcpuP.Unit)
	usdPerGBHr := normalizePerHour(*memP.USD, memP.Unit)
	if usdPerVCPUHr <= 0 || usdPerGBHr <= 0 || usdPerVCPUHr > 5 || usdPerGBHr > 5 {
		return Result{
			USD:   nil,
			Basis: "unknown",
			Breakdown: map[string]any{
				"reason":         "pricing sanity check failed",
				"usd_per_vcpu_h": usdPerVCPUHr,
				"usd_per_gb_h":   usdPerGBHr,
				"unitVCPU":       vcpuP.Unit,
				"unitMem":        memP.Unit,
				"usagePref":      prefix,
				"location":       loc,
			},
		}, nil
	}

	perTaskMonthly := hours*(vcpu*usdPerVCPUHr) + hours*(memGB*usdPerGBHr)
	monthly := perTaskMonthly * float64(desired)

	return Result{
		USD:   &monthly,
		Basis: "ecs.fargate_estimate",
		Breakdown: map[string]any{
			"desiredCount":      desired,
			"hours":             DefaultHoursPerMonth,
			"vcpu":              vcpu,
			"memoryGb":          memGB,
			"usd_per_vcpu_hour": usdPerVCPUHr,
			"usd_per_gb_hour":   usdPerGBHr,
			"location":          loc,
			"usagePref":         prefix,
			"note":              fmt.Sprintf("estimated Fargate compute only; desiredCount=%d, excludes data transfer, ECR, logs, etc", desired),
		},
	}, nil
}

func normalizePerHour(usd float64, unit string) float64 {
	u := strings.ToLower(strings.TrimSpace(unit))
	switch {
	case strings.Contains(u, "sec"):
		return usd * 3600.0
	default:
		return usd
	}
}

func anyFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
}
