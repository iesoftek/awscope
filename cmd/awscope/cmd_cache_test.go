package main

import "testing"

func TestNewCacheCmd_HasPruneStaleSubcommand(t *testing.T) {
	cmd := newCacheCmd()
	found := false
	for _, c := range cmd.Commands() {
		if c.Name() == "prune-stale" {
			found = true
			if f := c.Flags().Lookup("profile"); f == nil {
				t.Fatalf("expected --profile flag on prune-stale")
			}
			if f := c.Flags().Lookup("days"); f == nil {
				t.Fatalf("expected --days flag on prune-stale")
			}
		}
	}
	if !found {
		t.Fatalf("expected prune-stale subcommand")
	}
}
