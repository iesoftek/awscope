package cost

import (
	"context"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

// elbv2LoadBalancerEstimator estimates the base hourly cost for an ELBv2 load balancer.
// It excludes usage-based components (ALB LCU, NLB data processing, etc).
type elbv2LoadBalancerEstimator struct{}

func (elbv2LoadBalancerEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}
	prefix, _ := pricing.RegionToUsagePrefix(t.Region)

	lbType := strings.ToLower(strings.TrimSpace(anyString(t.Attributes["type"]))) // application/network/gateway
	priceKind := "elb_lb_hour"
	if lbType == "application" {
		priceKind = "elb_alb_hour"
	} else if lbType == "network" {
		priceKind = "elb_nlb_hour"
	} else if lbType == "gateway" {
		priceKind = "elb_gwlb_hour"
	}

	// ServiceCode for Elastic Load Balancing in Pricing API can vary; try common codes.
	serviceCodes := []string{"AWSELB", "AmazonELB"}

	// Prefer exact LoadBalancerUsage to avoid accidentally selecting data processing dimensions.
	// us-east-1 often uses "LoadBalancerUsage" without the region prefix.
	usagetypeCandidates := []string{"LoadBalancerUsage"}
	if prefix != "" {
		usagetypeCandidates = append([]string{prefix + "-LoadBalancerUsage"}, usagetypeCandidates...)
	}

	filterCandidates := make([]map[string]string, 0, 4)
	for _, ut := range usagetypeCandidates {
		filterCandidates = append(filterCandidates, map[string]string{
			"productFamily": "Load Balancer",
			"usagetype":     ut,
		})
	}
	// Final fallback: minimal filters; parser should reject obvious mismatches.
	filterCandidates = append(filterCandidates, map[string]string{"productFamily": "Load Balancer"}, map[string]string{})

	var price pricing.Price
	var err error
	for _, sc := range serviceCodes {
		for _, f := range filterCandidates {
			price, err = pc.Lookup(ctx, pricing.Query{
				Partition:   t.Partition,
				ServiceCode: sc,
				PriceKind:   priceKind,
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
				"lbType":     lbType,
				"priceKind":  priceKind,
				"location":   loc,
				"usagePref":  prefix,
				"serviceTry": strings.Join(serviceCodes, ","),
			},
		}, nil
	}

	hourly := *price.USD
	monthly := hourly * float64(DefaultHoursPerMonth)
	return Result{
		USD:   &monthly,
		Basis: "elbv2.base_lb_hour",
		Breakdown: map[string]any{
			"hourly_usd": hourly,
			"hours":      DefaultHoursPerMonth,
			"lbType":     lbType,
			"location":   loc,
			"unit":       strings.TrimSpace(price.Unit),
			"note":       "base LB-hours only; excludes LCU/data processing/usage-based fees",
		},
	}, nil
}
