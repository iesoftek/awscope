package audittrail

import "testing"

func TestNormalizeService(t *testing.T) {
	svc, ok := normalizeService("ec2.amazonaws.com")
	if !ok || svc != "ec2" {
		t.Fatalf("normalizeService ec2: svc=%q ok=%v", svc, ok)
	}
	if _, ok := normalizeService("cloudwatch.amazonaws.com"); ok {
		t.Fatalf("normalizeService should reject unsupported service")
	}
}

func TestClassifyAction(t *testing.T) {
	cases := map[string]string{
		"CreateRole":             "create",
		"DeleteRole":             "delete",
		"RunInstances":           "create",
		"TerminateInstances":     "delete",
		"RegisterTaskDefinition": "create",
	}
	for in, want := range cases {
		got, ok := classifyAction(in)
		if !ok || got != want {
			t.Fatalf("classifyAction(%q) got=%q ok=%v want=%q", in, got, ok, want)
		}
	}
	if _, ok := classifyAction("UpdateRole"); ok {
		t.Fatalf("classifyAction should reject non create/delete actions")
	}
}
