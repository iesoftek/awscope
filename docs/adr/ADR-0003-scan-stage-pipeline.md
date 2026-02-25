# ADR-0003: Scan Stage Pipeline

- Status: Accepted
- Date: 2026-02-25

## Context

Scan flow grew to include provider collection, resolver enrichment, audit indexing, and cost indexing.

## Decision

Treat scan as a stage pipeline with explicit progress phases and failure handling rules.

Stages:

1. Provider
2. Resolver
3. Audit
4. Cost

## Consequences

- Predictable progress semantics
- Cleaner future extraction to per-stage files
- Better testability for stage-level behavior
