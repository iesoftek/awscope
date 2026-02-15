# awscope Architecture + Component Plan (Go TUI AWS Resource Browser)

This plan is derived from `REQUIREMENTS.md` and is intended to be implementation-ready.

## Summary

Build `awscope` as a Go CLI + Bubble Tea TUI that inventories AWS resources into a local SQLite DB, maintains a hot in-memory index for responsiveness, supports multi-profile and multi-region browsing, models resources as a node/edge graph for relationship navigation, and executes a small set of guarded operational actions with an auditable trail.

### Locked v1 decisions

* SQLite persistence is **latest-only** (no per-scan resource history in v1).
* EC2 ↔ Target Group ↔ Load Balancer mapping is **full mapping in v1** (resolver calls; higher API cost).
* “Open in AWS Console” in v1 will **auto-open browser** with a fallback to displaying/copying the URL.

## Architecture

### Layering (strict boundaries)

1. UI layer (`internal/tui`)
   * Bubble Tea model(s), views, keybindings, modals, command palette.
   * No direct AWS SDK calls.
2. App/core layer (`internal/core`)
   * Application state, reducers, message types, intents and queries.
3. Scheduler layer (`internal/scheduler`)
   * Job queue, cancellation, concurrency limits, per-service/region rate limiting.
4. Provider layer (`internal/providers/*`)
   * Service-specific AWS list/describe normalization into nodes + edges.
5. Graph layer (`internal/graph`)
   * ResourceKey encoding, node/edge types, relationship resolution.
6. Store layer (`internal/store`)
   * SQLite schema, migrations, queries, redaction; hot in-memory indexes.
7. Actions layer (`internal/actions`)
   * Action registry, safety gates, executor, audit persistence.

### Data flow (runtime)

1. TUI emits intent: show resources for (profile, regions, service, type, filter).
2. Core loads from hot index; if missing/stale reads SQLite; UI renders immediately.
3. Scheduler starts refresh jobs; providers fetch AWS data; graph resolver enriches edges.
4. Store upserts nodes/edges; core updates hot index; UI receives data-updated events.
5. Actions run via executor; results persisted to SQLite; UI shows progress and results.

## Components

### CLI (`cmd/awscope`)

Commands:

* `awscope tui` (default)
* `awscope scan` (inventory job)
* `awscope export` (json/csv)
* `awscope cache` (stats/gc/compact)
* `awscope version`

### TUI (`internal/tui`)

* Multi-pane layout: navigator, list, details, status.
* Modals: profile switch (`P`), region selector (`R`), confirmations for actions.
* Command palette (`:`) for core actions including console URL open.

### Providers (`internal/providers/*`) (v1 scope)

Implement in this order:

1. EC2
2. ECS
3. IAM (global)
4. ELBv2 (for relationship resolver)
5. RDS
6. CloudWatch Logs (log groups only)
7. Secrets Manager (metadata only)
8. SQS (DLQ edges)
9. SNS (topic/subscription edges)

### Store (`internal/store`)

SQLite schema (v1, latest-only):

* `accounts`, `profiles`, `regions`
* `resources` (latest)
* `edges`
* `scan_runs`
* `action_runs`

Use `modernc.org/sqlite` by default.

### Scheduler (`internal/scheduler`)

* Job identity: `(profile, account_id, region, provider_id, query_kind)`
* Cancellation when switching profile/regions/service
* Provider-level concurrency + per service/region rate limiting

### Actions (`internal/actions`)

v1 actions:

* EC2: Stop/Start instance
* ECS: Stop task

## Libraries (pinned choices)

* TUI: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/bubbles`
* CLI: `github.com/spf13/cobra`
* AWS: `github.com/aws/aws-sdk-go-v2/...` (per-service clients)
* SQLite: `modernc.org/sqlite`
* Migrations: `github.com/pressly/goose/v3` (embedded migrations)
* Browser open: `github.com/pkg/browser`
* Concurrency: `golang.org/x/sync/errgroup`, `golang.org/x/time/rate`
* IDs: `github.com/google/uuid`
* Config: `gopkg.in/yaml.v3`

## Milestones (tasks)

### Milestone 0: Foundations

1. Go module + repo skeleton
2. Cobra CLI skeleton
3. Bubble Tea app shell + panes + keybindings
4. SQLite store + embedded migrations
5. Hot in-memory index baseline

### Milestone 1: Read-only browsing

1. Providers: EC2/ECS/IAM
2. Background refresh scheduler
3. Search/filter + global search from SQLite

### Milestone 2: Actions + audit

1. Action framework + confirmations
2. EC2 stop/start, ECS stop task
3. Persist and display `action_runs`

### Milestone 3: Scan + export

1. `scan` command (all regions)
2. Export JSON/CSV
3. Scan progress in TUI

### Milestone 4: Relationship depth & polish

1. Full EC2 ↔ TG ↔ LB resolver
2. ECS ↔ logs mapping heuristics
3. UX polish and performance tuning

---

# Best-In-Class TUI Plan (Navigation, Readability, Themes)

This section extends the implementation plan for the UI specifically. It is intended to drive the next large block of work.

## UI goals / success criteria

1. Fast browsing on large inventories (paging + debounced search, no UI blocking).
2. Human-readable by default: name, status/state, counts, created/launch time, region.
3. Relationship navigation is reversible and intuitive (navigation stack + Back).
4. IDs/ARNs and raw JSON are available in details but not front-and-center in lists.
5. Actions are discoverable but safe (risk-based confirmation, audit trail visibility).
6. Styling is consistent and themeable (semantic tokens, no hardcoded colors in views).

## Layout (default)

Target default: 3 panes + collapsible context sidebar.

* Left: Navigator (services + views)
* Center: Browser (hybrid table/list)
* Right: Details (tabbed)
* Collapsible sidebar: profile/account/regions, scan/refresh status, warnings/errors, key hints, quick actions

## Interaction model (keybindings)

* `tab`: cycle focus across panes
* `/`: focus filter (debounced)
* `?`: help overlay (full keymap)
* `:`: command palette
* `P`: profile picker
* `R`: region picker
* `Ctrl+R`: refresh view
* Relationship navigation:
  * `enter` on related target pushes a navigation frame
  * `backspace` (or `esc` outside modals) pops back
* Actions:
  * `A` opens action palette
  * typed confirmation based on risk level

## Data presentation rules

Human-readable first:

* Browser shows status/counts/dates as columns where available.
* Details Summary has structured sections: Status, Counts, Times, Networking, Tags, Identifiers.

Identifiers separate:

* IDs/ARNs shown in a dedicated Identifiers section + Raw tab, not as the main list view.

## TUI components we will use (Charm stack)

From `github.com/charmbracelet/bubbles`:

* `list`: navigator, relationship list, palettes, pickers
* `table`: resource browsing tables (type-specific columns)
* `viewport`: scrollable Summary/Raw/Help content
* `help` + `key`: keybinding rendering + `?` overlay
* `textinput` / `textarea`: search boxes + confirmation + command palette
* `paginator`: paging through SQLite-backed result sets
* `spinner` + `progress`: scan/refresh progress and loading states
* `filepicker`: export destination selection

### Bubbles component inventory (pinned)

Pinned module: `github.com/charmbracelet/bubbles@v1.0.0` (current `go.sum`).

Available components we can leverage for “best in class” UX:

* `cursor`: inline cursor helpers (useful for custom inputs/editors)
* `filepicker`: choose export path interactively
* `help`: contextual help footer + full overlay
* `key`: keybinding definitions for consistent help rendering
* `list`: fast selectable lists (services, palettes, relationship browsing)
* `paginator`: page calculations + page indicator rendering
* `progress`: progress bars (scan, refresh, action execution)
* `spinner`: spinners for loading states
* `stopwatch`: elapsed timers (scan timing, action timing)
* `table`: columnar browsing (hybrid mode: table for wide terminals)
* `textarea`: multi-line inputs (command palette, notes, future “query builder”)
* `textinput`: single-line inputs (filter, confirmation, palette)
* `timer`: countdown timers (optional for refresh intervals)
* `viewport`: scrollable text panes (details, raw, help, errors)

From `github.com/charmbracelet/bubbletea`:

* single state machine + async cmds + alt-screen

From `github.com/charmbracelet/lipgloss`:

* styling, borders, layout

## Theme support (add now, extend later)

Add a first-class theme system so we can add themes later cheaply.

* New package: `internal/tui/theme`
* Themes defined by semantic palette tokens; styles are derived centrally.
* Built-in themes (v1): `auto`, `classic`, `high-contrast`
* Respect `NO_COLOR=1`
* Future: load theme files from `~/.config/awscope/themes/*.yaml` without changing view code.

## Implementation phases (UI)

### Phase UI-1: TUI refactor + theming foundation

1. Split `internal/tui/tui.go` into an `app` model + `components` + `theme`.
2. Implement pane layout manager (3 panes + collapsible sidebar).
3. Implement modal stack + help overlay (`help.Model` + `key.Binding`).
4. Replace all direct styling in views with theme-derived styles.

### Phase UI-2: Browser (hybrid) + paging + debounced filter

1. Table-first browser using `bubbles/table`.
2. List fallback for narrow widths / uncurated types.
3. Paging with `paginator.Model` and store `Count/List` queries.
4. Debounced filtering (150-250ms).

### Phase UI-3: Details tabs (readable Summary + relationships + raw)

1. Summary renderer with wrapped text and structured sections.
2. Relationships: group edges by kind; resolve targets to names/types/regions via bulk DB lookup.
3. Raw: redacted JSON, optional syntax highlight.

### Phase UI-4: Context sidebar + safe actions

1. Sidebar shows profile/account/regions, scan/refresh progress, warnings/errors, key hints.
2. Actions show risk and require confirmation; show progress and final status.

### Phase UI-5: Command palette + global search + breadcrumbs

1. Command palette (`:`) for core operations and jump-to.
2. Global search across SQLite.
3. Breadcrumb from nav stack.
