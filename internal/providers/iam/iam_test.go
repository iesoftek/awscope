package iam

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestNormalizeRole(t *testing.T) {
	now := time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC)
	arn := "arn:aws:iam::123456789012:role/MyRole"
	name := "MyRole"
	roleID := "AROAXXXXX"
	path := "/"

	r := types.Role{
		Arn:      &arn,
		RoleName: &name,
		RoleId:   &roleID,
		Path:     &path,
	}

	n := normalizeRole("aws", "123456789012", r, now)
	if n.DisplayName != "MyRole" {
		t.Fatalf("display: got %q", n.DisplayName)
	}
	if n.Type != "iam:role" || n.Service != "iam" {
		t.Fatalf("type/service: %#v", n)
	}
}
