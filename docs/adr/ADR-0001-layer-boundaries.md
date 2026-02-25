# ADR-0001: Layer Boundaries

- Status: Accepted
- Date: 2026-02-25

## Context

Large files and cross-layer imports made behavioral changes risky.

## Decision

Enforce strict boundaries:

- CLI bootstraps only
- Core orchestrates only
- Providers collect AWS data only
- Store owns SQL only
- TUI/diagram render/read only

## Consequences

- Easier testing and review per layer
- Less accidental coupling
- More files, but lower cognitive load per file
