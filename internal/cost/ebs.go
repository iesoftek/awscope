package cost

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"awscope/internal/pricing"
	"awscope/internal/store"
)

// ebsVolumeEstimator estimates EBS volume storage cost only (GB-month).
// It intentionally excludes provisioned IOPS/throughput charges (gp3/io1/io2),
// and excludes snapshot costs. This is still a large accuracy win vs compute-only.
type ebsVolumeEstimator struct{}

func (ebsVolumeEstimator) Estimate(ctx context.Context, t store.CostIndexTarget, pc *pricing.Client) (Result, error) {
	if pc == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing client unavailable"}}, nil
	}
	loc, ok := pricing.RegionToLocation(t.Region)
	if !ok {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "unknown pricing location", "region": t.Region}}, nil
	}

	volType := strings.TrimSpace(anyString(t.Attributes["volumeType"]))
	sizeGb := anyNumber(t.Attributes["sizeGb"])
	if volType == "" || sizeGb <= 0 {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "missing volumeType/sizeGb", "volumeType": volType, "sizeGb": sizeGb}}, nil
	}

	// Pricing API for EBS is exposed via AmazonEC2 "Storage" products.
	price, err := pc.Lookup(ctx, pricing.Query{
		Partition:   t.Partition,
		ServiceCode: "AmazonEC2",
		PriceKind:   "ebs_storage_gb_month",
		AWSRegion:   t.Region,
		Location:    loc,
		Filters: map[string]string{
			"productFamily": "Storage",
			"volumeApiName": volType, // e.g. gp2/gp3/io1/io2/st1/sc1/standard
		},
	})
	if err != nil {
		return Result{}, err
	}
	if price.USD == nil {
		return Result{USD: nil, Basis: "unknown", Breakdown: map[string]any{"reason": "pricing not found", "volumeType": volType, "location": loc}}, nil
	}

	usdPerGBMo := *price.USD
	monthly := usdPerGBMo * float64(sizeGb)

	breakdown := map[string]any{
		"usd_per_gb_month": usdPerGBMo,
		"size_gb":          sizeGb,
		"volumeType":       volType,
		"location":         loc,
		"unit":             strings.TrimSpace(price.Unit),
		"note":             "storage only; excludes iops/throughput, snapshots",
	}
	if iops := anyNumber(t.Attributes["iops"]); iops > 0 {
		breakdown["iops"] = iops
	}
	if th := anyNumber(t.Attributes["throughput"]); th > 0 {
		breakdown["throughput"] = th
	}

	return Result{
		USD:       &monthly,
		Basis:     "ec2.ebs_storage",
		Breakdown: breakdown,
	}, nil
}

func anyNumber(v any) int64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	case jsonNumber:
		i, _ := x.Int64()
		return i
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", v))
		if s == "" {
			return 0
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f)
		}
		return 0
	}
}

// jsonNumber is the subset of encoding/json.Number we need, without importing it everywhere.
type jsonNumber interface {
	Int64() (int64, error)
}
