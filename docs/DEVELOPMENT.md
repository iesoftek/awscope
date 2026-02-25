# Development Guide

## Setup

- Go 1.26+
- SQLite available locally
- AWS credentials/profile for live scan

## Common commands

- `go test ./...`
- `go vet ./...`
- `make build`
- `make test`

## Adding a provider

1. Create `internal/providers/<service>/`.
2. Implement `providers.Provider` with correct scope.
3. Emit normalized `graph.ResourceNode` and `graph.RelationshipEdge` only.
4. Register provider in package `init()`.
5. Add service metadata in `internal/catalog/services.go`.
6. Add command registration import in `cmd/awscope/root.go`.
7. Add provider tests with stubbed SDK APIs.

## Adding TUI behavior

1. Add message type in `internal/tui/app/messages.go`.
2. Add loader command in `internal/tui/app/commands_resources.go`.
3. Route keys/state in `internal/tui/app/app.go` update logic.
4. Render in `internal/tui/app/view_root.go`.
5. Keep focus rules explicit for each modal/overlay.

## Adding service metadata

Use `internal/catalog/services.go` only.

Required fields:

- `ID`
- `DisplayName`
- `DefaultType`
- `FallbackTypes`
- `SampleType`
- `SampleLabel`

Do not duplicate fallback/default type switch statements elsewhere.

## Quality gates

Before merge:

1. `go test ./...`
2. `go vet ./...`
3. manual smoke: scan + tui + diagram + export + action
4. no public CLI flag/key regression unless explicitly planned
