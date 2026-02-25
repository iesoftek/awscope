# ADR-0002: Central Service Catalog

- Status: Accepted
- Date: 2026-02-25

## Context

Service defaults/fallback types were duplicated across scan/TUI paths.

## Decision

Use `internal/catalog/services.go` as the single service metadata source.

## Consequences

- Consistent defaults in scan summaries and TUI
- One place to update when adding services
- Catalog completeness tests required for future providers
