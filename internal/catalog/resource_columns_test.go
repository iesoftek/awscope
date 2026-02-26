package catalog

import "testing"

func hasKind(cols []ColumnSpec, kind ColumnKind) bool {
	for _, c := range cols {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

func TestResourceTablePresetCoverageAllFallbackTypes(t *testing.T) {
	for _, svc := range All() {
		p := ResourceTablePreset(svc.ID, "")
		if len(p.Columns) == 0 {
			t.Fatalf("service %q: empty preset", svc.ID)
		}
		for _, typ := range svc.FallbackTypes {
			p := ResourceTablePreset(svc.ID, typ)
			if len(p.Columns) == 0 {
				t.Fatalf("service %q type %q: empty preset", svc.ID, typ)
			}
		}
	}
}

func TestResourceTablePresetOverridePrecedence(t *testing.T) {
	serviceDefault := ResourceTablePreset("ec2", "ec2:unknown-type")
	if !hasKind(serviceDefault.Columns, ColumnKindStatus) {
		t.Fatalf("expected ec2 service default to include status column")
	}

	override := ResourceTablePreset("ec2", "ec2:key-pair")
	if hasKind(override.Columns, ColumnKindStatus) {
		t.Fatalf("expected ec2:key-pair override to exclude status column")
	}
}

func TestResourceTablePresetFallbackForUnknownService(t *testing.T) {
	p := ResourceTablePreset("unknown-service", "unknown:type")
	if len(p.Columns) == 0 {
		t.Fatalf("expected fallback preset columns")
	}
	if !hasKind(p.Columns, ColumnKindStatus) {
		t.Fatalf("expected fallback preset to include status")
	}
	if !hasKind(p.Columns, ColumnKindCreated) {
		t.Fatalf("expected fallback preset to include created")
	}
}
