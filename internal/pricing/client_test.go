package pricing

import "testing"

func TestParseUSDAndUnitForKind_PrefersALBLBHourOverLCU(t *testing.T) {
	raw := `{
  "terms": {
    "OnDemand": {
      "X": {
        "priceDimensions": {
          "A": {
            "unit": "Hrs",
            "description": "Application Load Balancer LoadBalancerUsage",
            "pricePerUnit": {"USD": "0.025"}
          },
          "B": {
            "unit": "Hrs",
            "description": "Application Load Balancer LCUUsage",
            "pricePerUnit": {"USD": "0.008"}
          }
        }
      }
    }
  }
}`
	usd, unit := parseUSDAndUnitForKind("elb_alb_hour", raw)
	if usd == nil {
		t.Fatalf("expected price")
	}
	if unit != "Hrs" {
		t.Fatalf("unit: got %q", unit)
	}
	if *usd != 0.025 {
		t.Fatalf("usd: got %v want 0.025", *usd)
	}
}

func TestParseUSDAndUnitForKind_PrefersFargateVCPU(t *testing.T) {
	raw := `{
  "terms": {
    "OnDemand": {
      "X": {
        "priceDimensions": {
          "A": {"unit": "GB-Hours", "description": "Fargate GB-Hours", "pricePerUnit": {"USD": "0.004"}},
          "B": {"unit": "vCPU-Hours", "description": "Fargate vCPU-Hours", "pricePerUnit": {"USD": "0.040"}}
        }
      }
    }
  }
}`
	usd, _ := parseUSDAndUnitForKind("fargate_vcpu_hour", raw)
	if usd == nil || *usd != 0.040 {
		t.Fatalf("usd: got %#v", usd)
	}
}

func TestParseUSDAndUnitForKind_PrefersFargateGB(t *testing.T) {
	raw := `{
  "terms": {
    "OnDemand": {
      "X": {
        "priceDimensions": {
          "A": {"unit": "GB-Hours", "description": "Fargate GB-Hours", "pricePerUnit": {"USD": "0.004"}},
          "B": {"unit": "vCPU-Hours", "description": "Fargate vCPU-Hours", "pricePerUnit": {"USD": "0.040"}}
        }
      }
    }
  }
}`
	usd, _ := parseUSDAndUnitForKind("fargate_gb_hour", raw)
	if usd == nil || *usd != 0.004 {
		t.Fatalf("usd: got %#v", usd)
	}
}

func TestParseUSDAndUnitForKind_RejectsNonFargateForFargateKinds(t *testing.T) {
	raw := `{
  "terms": {
    "OnDemand": {
      "X": {
        "priceDimensions": {
          "A": {"unit": "Hrs", "description": "ECS-Managed-Instances:g6e.48xlarge-management-hours", "pricePerUnit": {"USD": "3.615744"}}
        }
      }
    }
  }
}`
	usd, _ := parseUSDAndUnitForKind("fargate_vcpu_hour", raw)
	if usd != nil {
		t.Fatalf("expected nil price, got %v", *usd)
	}
}

func TestParseUSDAndUnitForKind_RejectsELBDataProcessingForHourKinds(t *testing.T) {
	raw := `{
  "terms": {
    "OnDemand": {
      "X": {
        "priceDimensions": {
          "A": {"unit": "GB", "description": "Data processed by Classic Load Balancer", "pricePerUnit": {"USD": "0.008"}}
        }
      }
    }
  }
}`
	usd, _ := parseUSDAndUnitForKind("elb_alb_hour", raw)
	if usd != nil {
		t.Fatalf("expected nil price, got %v", *usd)
	}
}
