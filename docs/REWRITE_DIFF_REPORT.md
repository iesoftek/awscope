# Rewrite Diff Report (One-Shot Branch)

## File Move / Split Map

### CLI

- `cmd/awscope/main.go` -> bootstrap only.
- Added command modules:
  - `cmd/awscope/root.go`
  - `cmd/awscope/cmd_tui.go`
  - `cmd/awscope/cmd_scan.go`
  - `cmd/awscope/cmd_diagram.go`
  - `cmd/awscope/cmd_export.go`
  - `cmd/awscope/cmd_action.go`
  - `cmd/awscope/cmd_cache.go`
  - `cmd/awscope/cmd_version.go`
  - `cmd/awscope/cmd_helpers.go`

### Core

- `internal/core/app.go` -> thin façade only.
- Scan orchestration split into:
  - `internal/core/scan_types.go`
  - `internal/core/scan_orchestrator.go`
  - `internal/core/scan_resolver.go`
  - `internal/core/scan_errors.go`
  - `internal/core/scan_helpers.go`

### Service Catalog

- Added central catalog:
  - `internal/catalog/services.go`

### TUI

- `internal/tui/app/app.go` reduced from monolith and split into:
  - `internal/tui/app/messages.go`
  - `internal/tui/app/keymap.go`
  - `internal/tui/app/view_root.go`
  - `internal/tui/app/commands_resources.go`
  - `internal/tui/app/transitions.go`
  - `internal/tui/app/formatters.go`
  - `internal/tui/app/layout.go`
  - `internal/tui/app/service_metadata.go`
  - `internal/tui/app/run.go`

### Providers (EC2)

- `internal/providers/ec2/ec2.go` split into:
  - `internal/providers/ec2/provider.go`
  - `internal/providers/ec2/list.go`
  - `internal/providers/ec2/normalize_core.go`
  - `internal/providers/ec2/normalize_extra.go`
  - `internal/providers/ec2/helpers.go`

### Store

- `internal/store/query.go` split into:
  - `internal/store/query_resources.go`
  - `internal/store/query_regions.go`
- `internal/store/store_test.go` split into:
  - `internal/store/store_resources_test.go`
  - `internal/store/store_costs_test.go`
  - `internal/store/store_cloudtrail_test.go`
  - `internal/store/store_lookup_test.go`

## Duplicate Switch/Metadata Cleanup

- Service default/fallback/sample metadata consolidated into `internal/catalog/services.go`.
- Replaced scattered service/type switch logic in scan summary and TUI default type resolution with catalog lookups.

## Public Command Surface

- Command names unchanged:
  - `scan`, `tui`, `diagram`, `export`, `action`, `cache`, `version`
- Existing global flags and subcommand flags preserved.

## Validation Summary

- `go test ./...` passes.
- `go vet ./...` passes.
- `make build` and `make test` pass.
