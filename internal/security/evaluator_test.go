package security

import (
	"encoding/json"
	"testing"

	"awscope/internal/graph"
)

func TestEvaluate_FindsExpectedRules(t *testing.T) {
	services := []string{
		"cloudtrail", "config", "guardduty", "securityhub", "accessanalyzer",
		"s3", "rds", "iam", "secretsmanager", "eks", "ec2",
	}
	nodes := []graph.ResourceNode{
		testNode("aws", "111111111111", "us-east-1", "cloudtrail", "cloudtrail:trail", "trail-1", "trail-1", map[string]any{
			"status":                   "stopped",
			"isMultiRegionTrail":       false,
			"logFileValidationEnabled": false,
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "config", "config:recorder", "rec-1", "rec-1", map[string]any{
			"recording": false,
			"status":    "stopped",
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "guardduty", "guardduty:detector", "det-1", "det-1", map[string]any{
			"status": "DISABLED",
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "accessanalyzer", "accessanalyzer:analyzer", "aa-1", "aa-1", map[string]any{
			"status": "INACTIVE",
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "s3", "s3:bucket", "bucket-1", "bucket-1", map[string]any{
			"encryption": "",
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "rds", "rds:db-instance", "db-1", "db-1", map[string]any{
			"public":    true,
			"encrypted": false,
		}, nil),
		testNode("aws", "111111111111", "global", "iam", "iam:user", "user-1", "user-1", map[string]any{
			"password_enabled": true,
			"mfa_active":       false,
		}, nil),
		testNode("aws", "111111111111", "global", "iam", "iam:access-key", "key-1", "key-1", map[string]any{
			"status":   "Active",
			"age_days": 120,
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "secretsmanager", "secretsmanager:secret", "secret-1", "secret-1", map[string]any{
			"rotationEnabled": false,
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "eks", "eks:cluster", "eks-1", "eks-1", map[string]any{
			"publicApi": true,
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "ec2", "ec2:instance", "i-1", "i-1", map[string]any{
			"state":    "running",
			"publicIp": "1.2.3.4",
		}, nil),
		testNode("aws", "111111111111", "us-east-1", "ec2", "ec2:security-group", "sg-1", "sg-1", map[string]any{
			"description": "open",
		}, mustJSON(map[string]any{
			"IpPermissions": []any{
				map[string]any{
					"IpProtocol": "-1",
					"IpRanges": []any{
						map[string]any{"CidrIp": "0.0.0.0/0"},
					},
				},
			},
		})),
	}

	got := Evaluate(EvaluateInput{
		Nodes:           nodes,
		SelectedRegions: []string{"us-east-1"},
		ScannedServices: services,
		MaxKeyAgeDays:   90,
	})

	if got.Coverage.AssessedChecks != 17 {
		t.Fatalf("assessed checks: got %d want 17", got.Coverage.AssessedChecks)
	}
	if got.Coverage.SkippedChecks != 0 {
		t.Fatalf("skipped checks: got %d want 0", got.Coverage.SkippedChecks)
	}

	wantIDs := []string{
		"CT-001", "CT-002", "CT-003", "CFG-001", "GD-001", "SH-001", "AA-001",
		"S3-001", "S3-002", "RDS-001", "RDS-002", "IAM-001", "IAM-002",
		"SEC-001", "EKS-001", "EC2-001", "EC2-002",
	}
	for _, id := range wantIDs {
		if _, ok := findingByID(got.Findings, id); !ok {
			t.Fatalf("missing finding %s", id)
		}
	}
	if f, ok := findingByID(got.Findings, "EC2-002"); !ok || f.Severity != SeverityCritical {
		t.Fatalf("EC2-002 severity: got %+v", f)
	}
}

func TestEvaluate_CoverageSkipsUnscannedServices(t *testing.T) {
	got := Evaluate(EvaluateInput{
		Nodes:           nil,
		SelectedRegions: []string{"us-east-1"},
		ScannedServices: []string{"ec2"},
	})

	if got.Coverage.AssessedChecks != 2 {
		t.Fatalf("assessed checks: got %d want 2", got.Coverage.AssessedChecks)
	}
	if got.Coverage.SkippedChecks != 15 {
		t.Fatalf("skipped checks: got %d want 15", got.Coverage.SkippedChecks)
	}
	if len(got.Coverage.MissingServices) == 0 {
		t.Fatalf("expected missing services")
	}
}

func TestSecurityGroupWorldOpenExposure(t *testing.T) {
	cases := []struct {
		name     string
		raw      []byte
		critical bool
		high     bool
	}{
		{
			name: "critical all protocol",
			raw: mustJSON(map[string]any{
				"IpPermissions": []any{
					map[string]any{
						"IpProtocol": "-1",
						"IpRanges": []any{
							map[string]any{"CidrIp": "0.0.0.0/0"},
						},
					},
				},
			}),
			critical: true,
			high:     false,
		},
		{
			name: "high ssh",
			raw: mustJSON(map[string]any{
				"IpPermissions": []any{
					map[string]any{
						"IpProtocol": "tcp",
						"FromPort":   22,
						"ToPort":     22,
						"IpRanges": []any{
							map[string]any{"CidrIp": "0.0.0.0/0"},
						},
					},
				},
			}),
			critical: false,
			high:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, h := securityGroupWorldOpenExposure(tc.raw)
			if c != tc.critical || h != tc.high {
				t.Fatalf("got critical=%v high=%v", c, h)
			}
		})
	}
}

func TestEvaluate_IAMKeyAgeThreshold(t *testing.T) {
	nodes := []graph.ResourceNode{
		testNode("aws", "111111111111", "global", "iam", "iam:access-key", "k-1", "key-1", map[string]any{
			"status":   "active",
			"age_days": 95,
		}, nil),
	}

	got := Evaluate(EvaluateInput{
		Nodes:           nodes,
		SelectedRegions: []string{"us-east-1"},
		ScannedServices: []string{"iam"},
		MaxKeyAgeDays:   120,
	})
	if _, ok := findingByID(got.Findings, "IAM-002"); ok {
		t.Fatalf("unexpected IAM-002 finding at 120-day threshold")
	}

	got = Evaluate(EvaluateInput{
		Nodes:           nodes,
		SelectedRegions: []string{"us-east-1"},
		ScannedServices: []string{"iam"},
		MaxKeyAgeDays:   90,
	})
	if _, ok := findingByID(got.Findings, "IAM-002"); !ok {
		t.Fatalf("expected IAM-002 finding at 90-day threshold")
	}
}

func testNode(partition, accountID, region, service, typ, id, display string, attrs map[string]any, raw []byte) graph.ResourceNode {
	return graph.ResourceNode{
		Key:         graph.EncodeResourceKey(partition, accountID, region, typ, id),
		DisplayName: display,
		Service:     service,
		Type:        typ,
		PrimaryID:   id,
		Attributes:  attrs,
		Raw:         raw,
	}
}

func findingByID(findings []Finding, id string) (Finding, bool) {
	for _, f := range findings {
		if f.CheckID == id {
			return f, true
		}
	}
	return Finding{}, false
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
