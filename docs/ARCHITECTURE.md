# awscope Architecture

awscope is split into five runtime layers:

1. CLI (`cmd/awscope`)
2. Application core (`internal/core`)
3. Data access (`internal/store`)
4. Domain integrations (`internal/providers`, `internal/pricing`, `internal/cost`, `internal/audittrail`)
5. Presentation (`internal/tui`, `internal/diagram`, `internal/export` paths in store)

## Layer boundaries

- `cmd/awscope` only parses flags, constructs dependencies, invokes core/TUI/diagram/export entrypoints.
- `internal/core` orchestrates scan stages and progress reporting; it does not render UI.
- `internal/providers/*` only talk to AWS APIs and produce graph nodes/edges.
- `internal/store` is the only SQLite boundary.
- `internal/tui/app` consumes store/core abstractions and renders state machines.

## Scan pipeline

The scan path is stage-based:

1. Provider collection
2. Resolver enrichment (cross-service relations)
3. Audit indexing (CloudTrail activity)
4. Cost indexing (pricing + estimates)

Progress is emitted as stage events; storage writes are serialized for SQLite safety.

## Service metadata

`internal/catalog` is the single source for service defaults and fallback types used by scan summaries and TUI navigation.

## TUI state model

`internal/tui/app` is organized as:

- model + update state machine
- command loaders for async DB/AWS work
- view renderers (main panes + full-screen audit)
- transitions/selection/region helpers
- formatting/table helpers

This keeps key handling deterministic while allowing feature overlays (audit, regions, actions).
