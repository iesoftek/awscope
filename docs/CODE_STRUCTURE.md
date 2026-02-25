# Code Structure

## CLI

- `cmd/awscope/main.go`: bootstrap
- `cmd/awscope/root.go`: root command + wiring
- `cmd/awscope/cmd_*.go`: per-command files (`scan`, `tui`, `diagram`, `export`, `action`, `cache`, `version`)

## Core

- `internal/core/scan_types.go`: public scan types + app constructor
- `internal/core/scan_orchestrator.go`: scan execution orchestration
- `internal/core/scan_resolver.go`: ELBv2 target-group membership resolver
- `internal/core/scan_errors.go`: skippable/unsupported region error classification
- `internal/core/scan_helpers.go`: shared helper utilities
- `internal/core/scan_progress.go`: progress event types

## Catalog

- `internal/catalog/services.go`: service metadata registry (defaults, fallback types, sample labels)

## Providers

Example split (`internal/providers/ec2`):

- `provider.go`: provider shell + AWS API contract
- `list.go`: region collection routine
- `normalize_core.go`: core EC2 resource normalizers
- `normalize_extra.go`: artifact/network normalizers
- `helpers.go`: key/ARN/helper functions

## Store

- `internal/store/store.go`: DB open/lifecycle
- `internal/store/query_resources.go`: resource summary and list/count queries
- `internal/store/query_regions.go`: region-scoped discovery queries
- `internal/store/cloudtrail_events.go`: audit query/filter/cursor methods
- `internal/store/costs.go`: pricing/resource cost persistence + aggregates
- `internal/store/*_test.go`: domain-split store tests

## TUI

`internal/tui/app`:

- `app.go`: model, shared state, root update loop
- `messages.go`: message types + list item models
- `keymap.go`: key bindings/help groups
- `view_root.go`: main and audit views
- `commands_resources.go`: async loaders/commands
- `transitions.go`: context + navigation + region scope transitions
- `formatters.go`: table/detail formatting + `Run` entrypoint

## Docs

- `docs/ARCHITECTURE.md`: high-level architecture
- `docs/CODE_STRUCTURE.md`: package/file responsibilities
- `docs/DEVELOPMENT.md`: contributor workflows
- `docs/adr/*.md`: design decisions
