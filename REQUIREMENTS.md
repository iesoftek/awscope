# Product specification: AWS Resource Browser CLI + TUI (Go)

This document defines a buildable, modular product spec for an interactive AWS resource browser with a polished TUI, background caching, SQLite persistence, profile switching, relationship-aware navigation, and safe operational actions.

Context: this spec is prepared for an engineering org that does end-to-end product engineering and delivery (Iesoftek). 

---

## 1) Product summary

### Working name

**awscope** (placeholder; rename anytime)

### One-line description

A fast, keyboard-driven terminal app to **browse, search, and act on AWS resources across profiles and regions**, with **relationship-aware drill-down** and an **offline SQLite inventory**.

### Primary value

* Stop hunting through AWS Console tabs.
* Get an inventory across regions quickly (and keep it).
* Navigate interconnected resources (EC2 ↔ ALB/NLB ↔ target groups, ECS services ↔ target groups, SQS ↔ DLQ, IAM principals ↔ policies, etc.).
* Perform safe operational actions (stop instance, stop ECS task, refresh, etc.) with confirmations and audit logs.

---

## 2) Goals and non-goals

### Goals

1. **Best-in-class TUI UX**: multi-pane layout, fast search/filter, drill-down details, relationship navigation, command palette, minimal friction.
2. **Multi-profile + interactive switching** using AWS shared config/credentials (including SSO where configured via AWS CLI). ([AWS Documentation][1])
3. **Multi-region browsing** with “Global” handling for global services (IAM).
4. **Resource graph model**: represent resources as nodes + edges; allow “jump to related”.
5. **Actions framework**: per-resource actions with safety gating + audit trail.
6. **Caching + persistence**:

   * in-memory cache for responsiveness
   * SQLite for durable inventory (audit / offline browsing)
   * background refresh updating cache while UI remains usable
7. **Modular extensibility**: add new AWS services and relationship rules without touching core TUI logic.

### Non-goals (initial releases)

* Full IaC drift detection (Terraform/CloudFormation diffing).
* Full-blown “change management” approvals.
* Editing resource configurations beyond a small set of safe actions.
* Multi-account aggregation via AWS Organizations (can be phased later).

---

## 3) Target users & key use cases

### Personas

* **SRE/DevOps**: “What’s running in prod right now? Which region? Stop it safely.”
* **Platform engineer**: “Find all ECS services tied to this target group; inspect desired/healthy counts.”
* **Security/Compliance**: “Inventory across regions; export evidence; track actions taken.”
* **Developer on-call**: “Find log groups for a service; jump from ECS service → logs → task details.”

### Top workflows (must be 1–2 keystrokes away)

1. Launch TUI → choose profile → choose region(s) → choose service → browse list.
2. Filter list by fuzzy search (name/ID/tag/arn).
3. Select resource → see details pane → see related resources list → jump to related.
4. Trigger action (stop instance / stop task / refresh) → confirm → see progress → auto-refresh.
5. Start “scan all regions” → keep working in UI while scanner fills DB.
6. Offline mode: open previous inventory without AWS access.

---

## 4) Tech stack choices (researched)

### 4.1 TUI framework: **Bubble Tea stack (recommended)**

* **Bubble Tea** for the application loop / state machine (Elm-like architecture) and full-window TUIs. ([GitHub][2])
* **Lip Gloss** for styling/layout composition in terminal (works tightly with Bubble Tea). ([GitHub][3])
* **Bubbles** (Charm’s component set) for ready-made primitives (lists, text inputs, viewports, etc.)—use selectively (not everything must be a “bubble”). (Mentioned as part of Charm ecosystem; Bubble Tea + Lip Gloss are the anchors.) ([Charm][4])

Why this choice:

* Great for **custom, modern multi-pane** layouts and “app-like” UX.
* Clean async model: background fetchers send messages to update UI.

### 4.2 Alternate option (not primary): **tview**

tview is a mature widget library with rich interactive components, built over tcell. ([GitHub][5])
We **won’t** use it in v1 to avoid mixing paradigms; keep it as a fallback if Bubble Tea custom layout cost becomes too high.

### 4.3 CLI framework

* **spf13/cobra** for commands/subcommands, flags, help UX. ([GitHub][6])

### 4.4 AWS SDK

* **aws-sdk-go-v2** (official). ([GitHub][7])
  Notes:

  * The v1 repo is archived / end-of-support; do not build on it. ([GitHub][8])
  * Shared config/credential chain loads from env + `~/.aws/config` + `~/.aws/credentials`. ([Go Packages][9])
  * SSO named profile support exists (when configured/signed-in via AWS CLI v2). ([Amazon Web Services, Inc.][10])

### 4.5 SQLite driver

* Prefer **modernc.org/sqlite** (pure Go; easier cross-compiles, no gcc/cgo dependency). ([Go Packages][11])
* Explicitly avoid `mattn/go-sqlite3` for default builds because it requires cgo/gcc. ([GitHub][12])

### 4.6 SQL access pattern

* Recommended: **sqlc** for type-safe queries + migrations folder. ([sqlc.dev][13])
  (ORMs are fine, but sqlc keeps runtime minimal and schema explicit.)

---

## 5) High-level architecture

### 5.1 Component diagram (logical)

* **cmd/** (cobra commands)
* **internal/tui/** (Bubble Tea app: layout, keybindings, panes, view models)
* **internal/core/** (app state, reducers, message bus, selection model)
* **internal/aws/** (session/config loader, per-service clients, throttling, pagination helpers)
* **internal/providers/** (service modules: ECS, EC2, RDS, Logs, IAM, Secrets, SNS, SQS)
* **internal/graph/** (resource node/edge model + relationship resolvers)
* **internal/store/** (SQLite schema, queries, resource cache, snapshot history)
* **internal/scheduler/** (background refresh workers, scan orchestration)
* **internal/actions/** (action registry, safety gates, execution engine, audit logging)

### 5.2 Data flow

1. TUI requests “list resources” for (profile, regions, service, filter).
2. Core checks **in-memory index** first; reads from SQLite if needed.
3. If stale/missing → scheduler queues provider fetch jobs.
4. Provider calls AWS APIs; normalizes into ResourceNodes + Edges.
5. Store persists nodes/edges + updates indexes.
6. Provider emits UI messages (“data updated”), list pane refreshes.

---

## 6) Domain model: resources + relationships

### 6.1 Resource identity rules

Every resource is uniquely addressed by a composite key:

* `account_id`
* `partition` (aws / aws-us-gov / aws-cn)
* `region` (`global` for global services like IAM)
* `resource_type` (e.g., `ec2:instance`, `ecs:service`)
* `primary_id` (prefer ARN; else stable ID like instance-id or URL-safe ID)

### 6.2 ResourceNode schema (in memory)

```go
type ResourceNode struct {
  Key          ResourceKey
  DisplayName  string            // best human label
  Service      string            // ec2, ecs, rds, iam...
  Type         string            // instance, service, db-instance...
  Arn          string            // may be empty for some types
  Tags         map[string]string
  Attributes   map[string]any    // normalized summary fields (for list columns)
  Raw          json.RawMessage   // full Describe* payload (redacted as needed)
  CollectedAt  time.Time
  Source       string            // provider name/version
}
```

### 6.3 RelationshipEdge schema

```go
type RelationshipEdge struct {
  From   ResourceKey
  To     ResourceKey
  Kind   string // "attached-to" | "member-of" | "targets" | "uses" | "logs-to" | ...
  Meta   map[string]any
}
```

### 6.4 Relationship principles

* Relationships are **explicit edges**, not inferred in UI.
* Each provider is responsible for:

  1. emitting its own nodes
  2. emitting edges to known related nodes (where IDs/ARNs exist)
* A separate **Relationship Resolver layer** can add cross-service edges:

  * Example: EC2 instance → target group membership, and target group → load balancer.
  * Some edges come “for free” from describe payloads; some require additional calls.

### 6.5 Interconnection rules (initial set)

Focus on what users asked for:

#### EC2 (region-scoped)

Nodes:

* instance, vpc, subnet, eni, security-group, ebs-volume (optional v1), ami (optional)
  Edges:
* instance → subnet/vpc
* instance → security-group(s)
* instance → eni(s)
* instance → iam-role (instance profile role) if present
* instance → target-group(s) (resolver)
* target-group → load-balancer(s) (resolver)
* load-balancer → listener(s) (optional)

#### ECS (region-scoped)

Nodes:

* cluster, service, task, task-definition
  Edges:
* service → cluster
* service → task-definition
* service → target-group(s) (from service load balancer config)
* task → service (when listing tasks by service)
* task → task-definition

#### RDS (region-scoped)

Nodes:

* db-instance, db-cluster (optional), subnet-group, parameter-group (optional)
  Edges:
* db-instance → subnet-group
* db-instance → vpc-security-group(s)
* db-instance → kms-key (if known; optional v1)

#### CloudWatch Logs (region-scoped)

Nodes:

* log-group (and optionally log-stream on-demand)
  Edges:
* ecs:service → logs:log-group (heuristic via naming or task definition log config; mark as `Kind=heuristic`)
* Provide “Open log group” navigation from ECS details

#### IAM (global)

Nodes:

* role, user, group, policy, access-key (metadata only)
  Edges:
* user → group(s)
* user → attached-policy(s)
* role → attached-policy(s)
* role → instance-profile(s) (optional)
* role → trust-principal summary (not edges)

#### Secrets Manager (region-scoped)

Nodes:

* secret (metadata)
  Edges:
* secret → kms-key (if present)
  **Never store secret values by default.**

#### SNS (region-scoped)

Nodes:

* topic, subscription
  Edges:
* topic → subscription(s)

#### SQS (region-scoped)

Nodes:

* queue
  Edges:
* queue → dead-letter-queue (from RedrivePolicy if enabled)

---

## 7) Service discovery strategy (important for “audit everything”)

We will use a **hybrid discovery** approach:

### 7.1 First-class providers (high quality)

For the initial services (EC2/ECS/RDS/Logs/IAM/Secrets/SNS/SQS), use service-native APIs for correctness and better relationship data.

### 7.2 Optional “broad inventory helpers” (phase 2)

To help discover additional services quickly:

* **Resource Groups Tagging API** `GetResources` can return tagged (or previously tagged) resources in a region, filtered by resource types/tags. ([AWS Documentation][14])
* **Cloud Control API** `ListResources` can list resources of a given CloudFormation type (per region) regardless of provisioning mechanism. ([AWS Documentation][15])
  These can seed inventory even before a dedicated provider exists.

### 7.3 Why not just use AWS Resource Explorer?

AWS Resource Explorer provides resource discovery/search across regions/accounts when configured with an aggregator index. ([Amazon Web Services, Inc.][16])
However, it requires service setup and doesn’t directly provide the **relationship-aware** drill-down we want. We can integrate later as an optional backend.

---

## 8) Caching & SQLite persistence

### 8.1 Cache layers

1. **Hot in-memory index** (for current session)

   * keyed by `ResourceKey`
   * plus secondary indexes for list queries (by type/region/name/tag)
2. **SQLite store** (durable inventory)

   * used for startup load, offline browsing, audit exports
3. **Background refresh** updates both layers

### 8.2 SQLite schema (v1)

Use migrations (sql files) and sqlc-generated code.

**Tables**

* `accounts`

  * `account_id TEXT PK`, `partition TEXT`, `last_seen_at`
* `profiles`

  * `profile_name TEXT PK`, `account_id TEXT`, `role_arn TEXT NULL`, `last_used_at`
* `regions`

  * `region TEXT PK`
* `resources` (latest view)

  * `resource_key TEXT PK` (stable string encoding)
  * `account_id, partition, region, service, type, arn, primary_id`
  * `display_name`
  * `tags_json`
  * `attributes_json`
  * `raw_json` (redacted)
  * `collected_at`, `updated_at`
  * indexes: `(account_id, region, service, type)`, `(arn)`, `(display_name)`
* `edges`

  * `from_key`, `to_key`, `kind`, `meta_json`, `collected_at`
  * composite index `(from_key, kind)`
* `scan_runs` (optional but recommended)

  * `scan_id TEXT PK`, `started_at`, `ended_at`, `profile_name`, `scope_json`, `status`
* `resource_history` (optional in v1; can be v2)

  * `scan_id`, `resource_key`, `raw_json`, `collected_at`

**Action audit**

* `action_runs`

  * `action_run_id TEXT PK`, `started_at`, `ended_at`
  * `profile_name`, `account_id`, `region`
  * `resource_key`, `action_id`, `input_json`, `result_json`, `status`

### 8.3 Sensitive data rules

* Never store:

  * Secret values (Secrets Manager)
  * CloudWatch log event contents
* Redact in `raw_json`:

  * access key secrets (shouldn’t be returned anyway)
  * any field matching patterns like `*Secret*`, `*Token*` (configurable)

### 8.4 SQLite driver decision

* Default: `modernc.org/sqlite` to avoid CGO and simplify releases. ([Go Packages][11])
* Documented alternative build tag `cgo_sqlite` enabling `mattn/go-sqlite3` for maximum performance on systems with gcc. ([GitHub][12])

---

## 9) Background refresh & concurrency

### 9.1 Worker model

* A central scheduler manages jobs:

  * `(profile, region, provider, queryKind)`
* Each provider declares:

  * `DefaultTTL`
  * `MaxConcurrency`
  * `CostHint` (cheap/moderate/expensive)

### 9.2 Rate limiting & retries

* Rely on AWS SDK v2 standard retry behavior as baseline. ([AWS Documentation][17])
* Add an application-level token bucket per region/service to avoid API storms.
* Use `context.Context` everywhere; cancel jobs when user switches profile/region.

### 9.3 “Refresh in background” behavior

* UI always reads from cache/SQLite first.
* When stale:

  * UI shows stale data immediately + a subtle “refreshing…” indicator
  * job runs async; when finished, list pane updates in place
* Provide manual refresh:

  * refresh selected resource
  * refresh current list
  * refresh all visible services in current region
  * start/stop background auto-refresh

---

## 10) Profiles, regions, credentials UX

### 10.1 Profile loading

* Enumerate profiles from AWS shared config/credentials.
* Load using `config.LoadDefaultConfig` with explicit profile selection. The SDK supports shared config files and the default chain. ([Go Packages][9])
* Support SSO profiles (assumes user already logged in with AWS CLI). ([Amazon Web Services, Inc.][10])

### 10.2 Interactive profile switch (must-have)

Keybinding: `P`

* Pops a modal list of profiles (fuzzy-search)
* On select:

  * cancel in-flight jobs
  * set new AWS config
  * load account identity via STS GetCallerIdentity (store account_id)
  * reload regions/services view

### 10.3 Region selection

Keybinding: `R`

* Region picker supports:

  * single region
  * multi-select regions
  * “All enabled regions” preset
* Maintain a special pseudo-region `global` for IAM (and other global services later).

---

## 11) TUI UX specification (multi-pane, drill-down, details)

### 11.1 Layout (default)

**Top bar**: profile | account | selected regions | global status | search hint
**Left pane (Navigator)**:

* Services list (EC2, ECS, RDS, Logs, IAM, Secrets, SNS, SQS)
* Region quick switch / pinned regions
* “Scans” (history) (optional v1, easy v2)

**Middle pane (Resource list)**:

* Table/List of resources for current (service, region scope)
* Columns configurable per resource type (e.g., EC2: State, Type, AZ, Name, PrivateIP, LaunchTime)
* Fuzzy filter input (inline)

**Right pane (Details)**:
Tabbed:

* **Summary** (key fields, tags)
* **Relationships** (related resources grouped by edge kind; Enter to jump)
* **Raw** (pretty JSON with redaction)
* **Actions** (available actions + status)

**Bottom status bar**:

* Keybindings help
* background scan progress
* errors/warnings (throttling, auth expired, permission denied)

### 11.2 Navigation & keybindings (v1)

Global:

* `q` quit
* `?` help overlay (all shortcuts)
* `tab` cycle panes
* `/` focus search/filter box
* `esc` clear modal/back
* `P` profile switch
* `R` region select
* `Ctrl+R` refresh current view
* `Ctrl+F` global search (across services/regions in SQLite)

List pane:

* `j/k` or arrows move
* `enter` open drill-down (resource “page”)
* `space` multi-select toggle
* `A` open action palette for selected
* `D` open details tab focus

Details pane:

* `1..4` switch tabs
* `enter` on relationship item → jump to that resource list + selection

### 11.3 Command palette (strongly recommended)

Keybinding: `:`

* actions:

  * “Switch profile…”
  * “Select regions…”
  * “Start full scan (all regions)”
  * “Export inventory…”
  * “Toggle offline mode”
  * “Copy ARN”
  * “Open in AWS Console (print URL / copy)”

### 11.4 UX guardrails for actions

* Every destructive action requires:

  * a confirmation modal with resource identifiers + region + profile
  * optional “type the resource name/id to confirm” for high-risk actions
* Always show action progress + final API result in the status bar and in `action_runs`.

---

## 12) Actions framework

### 12.1 Action interface

```go
type Action interface {
  ID() string
  Title() string
  Description() string
  Risk() RiskLevel // Low, Medium, High
  Applicable(node ResourceNode) bool
  ParamsSchema() JSONSchema // for prompts
  Execute(ctx context.Context, exec ActionExecContext, node ResourceNode, params map[string]any) (ActionResult, error)
}
```

### 12.2 Execution rules

* Actions run in a dedicated executor with concurrency limits.
* Actions are cancellable (best effort).
* Results are persisted to SQLite (`action_runs`).

### 12.3 Initial action catalog (v1)

EC2 instance:

* Stop instance (primary ask)
* Start instance
* Reboot (optional)
  ECS:
* Stop task (primary ask)
* Update service desired count (optional; Medium risk)
  RDS:
* Stop DB instance (if supported; handle AWS errors gracefully)
* Start DB instance
  CloudWatch Logs:
* Set retention policy (optional)
  IAM:
* Deactivate access key (High risk; probably v2)
  Secrets Manager:
* Rotate secret (v2)
  SQS:
* Purge queue (High risk; likely v2)
  SNS:
* No destructive actions in v1 (list-only)

(Implementation note: action availability should be driven by provider capabilities + permissions—if denied, show error but don’t crash.)

---

## 13) Provider module specification (modular approach)

### 13.1 Provider interface

```go
type Provider interface {
  ID() string // "ec2", "ecs", ...
  DisplayName() string
  Scope() ScopeKind // Regional | Global
  ResourceTypes() []ResourceTypeDef
  List(ctx context.Context, req ListRequest) ([]ResourceNode, []RelationshipEdge, error)
  Describe(ctx context.Context, req DescribeRequest) (ResourceNode, []RelationshipEdge, error)
}
```

### 13.2 Resource type definition

```go
type ResourceTypeDef struct {
  Type string // "ec2:instance"
  DefaultColumns []ColumnDef
  DefaultSort ColumnSort
  SearchKeys []string // attributes/tags for quick filtering
}
```

### 13.3 Registration mechanism (no dynamic plugins in v1)

Use compile-time registration:

* `internal/providers/ec2` has `func init(){ registry.Register(ec2Provider) }`
* Core provider registry is in `internal/providers/registry`

This is:

* simple
* testable
* cross-platform

Later: optionally support external plugins via separate build or WASM modules (phase 3).

---

## 14) “Scan / Audit mode” (inventory across all regions)

### 14.1 CLI commands

* `awscope tui` (default)
* `awscope scan --profile prod --regions all --services ec2,ecs,rds,logs,iam,secrets,sns,sqs`
* `awscope export --format json --out inventory.json`
* `awscope export --format csv --out inventory.csv` (per-resource-type tables)
* `awscope cache stats|gc|compact`

### 14.2 Scan behavior

* Produces a `scan_run`
* For each service/region:

  * provider List + optionally Describe (depending on resource type)
* Stores results in SQLite (latest + optionally history)
* Provides progress output in CLI and in TUI

---

## 15) Safety, permissions, and error handling

### 15.1 IAM permissions

* Tool should ship with **example least-privilege policies** per service:

  * list/describe permissions
  * action permissions (stop/start/etc.)
* If a permission is missing:

  * show “AccessDenied” in status pane with which call failed
  * still allow browsing other services

### 15.2 Handling throttling

* Display throttle warnings
* Backoff via SDK retry + local rate limiting
* Allow user to reduce concurrency in config

(AWS throttling and retry guidance exists broadly; the SDK has a standard retryer with defaults and backoff. ([AWS Documentation][17]))

---

## 16) Configuration

### 16.1 Config file

`~/.config/awscope/config.yaml` (or OS-appropriate default)

Key settings:

* default profile
* pinned regions
* enabled services
* refresh TTLs per provider
* max concurrency per provider
* sqlite path
* redaction rules
* offline mode default

### 16.2 Environment variables

* `AWS_PROFILE`, `AWS_REGION` (respected but UI can override)
* `AWS_SDK_LOAD_CONFIG=1` (documented for users coming from legacy setups)

---

## 17) Testing strategy (Codex-friendly)

### 17.1 Unit tests

* provider normalization: feed canned AWS responses → assert nodes/edges
* keybinding reducer tests: message in → model out
* store tests: migrations + insert/update + indexes

### 17.2 Integration tests

* “Mock AWS” via stubbed SDK clients (interfaces per service client)
* Optional LocalStack-based tests (best-effort; not all services behave identically)

### 17.3 Golden tests for TUI views

* Render list/detail views for fixed model states and compare to golden text snapshots.

---

## 18) Deliverables & repository structure

### 18.1 Repo skeleton

```
/cmd/awscope
  main.go
/internal
  /tui
  /core
  /providers
    /ec2
    /ecs
    /rds
    /logs
    /iam
    /secrets
    /sns
    /sqs
  /graph
  /aws
  /store
  /actions
  /scheduler
/migrations
/sql
  queries.sql
  schema.sql
```

### 18.2 Definition of Done (v1)

* Launches `awscope tui` into a stable multi-pane interface.
* Profile switch works and cancels inflight tasks.
* Region selector works, including multi-select.
* Providers implemented: EC2, ECS, RDS, Logs, IAM, Secrets, SNS, SQS.
* Relationship navigation:

  * ECS service → target group(s)
  * SQS queue → DLQ
  * IAM user/role → attached policies
  * EC2 instance → SG/VPC/subnet
  * (Optional v1.1) EC2 instance → target groups → load balancers
* SQLite caching works; offline mode can browse existing inventory.
* Actions implemented safely:

  * EC2 stop/start
  * ECS stop task
* Action audit log written to SQLite.

---

## 19) Suggested milestones (implementation order)

### Milestone 0 — Foundations (1–2 weeks)

* cobra CLI skeleton
* AWS config loader (profiles/regions) using SDK v2 ([Go Packages][9])
* SQLite store + migrations + sqlc
* Bubble Tea app shell + panes + keybindings ([GitHub][2])

### Milestone 1 — Read-only browsing (2–4 weeks)

* Providers: EC2/ECS/IAM first (highest value)
* Basic relationships from payloads
* Background refresh scheduler
* Search/filter and global search from SQLite

### Milestone 2 — Actions + audit (1–2 weeks)

* Action framework + confirmations
* EC2 stop/start, ECS stop task
* Persist `action_runs` and show in UI

### Milestone 3 — Inventory scan + export (1–2 weeks)

* `scan` command (all regions)
* export JSON/CSV
* scan progress in TUI

### Milestone 4 — Relationship depth & polish (ongoing)

* EC2 ↔ target groups ↔ load balancers resolver
* ECS ↔ logs mapping heuristics
* Better diff/history

---

## 20) Open decisions (make these explicit in tickets, not blockers)

1. **History vs latest-only**: keep `resource_history` in v1 or defer to v2.
2. **How deep to go on ALB/TG mapping** for EC2 (can be expensive at scale).
3. **Console URL integration**: generate and copy URLs vs open browser (opening is OS-specific).
4. **Bulk actions**: allow multi-select stop (dangerous; likely v2 with strong guardrails).

---


[1]: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html?utm_source=chatgpt.com "Configure the SDK - AWS SDK for Go v2"
[2]: https://github.com/charmbracelet/bubbletea?utm_source=chatgpt.com "charmbracelet/bubbletea: A powerful little TUI framework"
[3]: https://github.com/charmbracelet/lipgloss?utm_source=chatgpt.com "charmbracelet/lipgloss: Style definitions for nice terminal ..."
[4]: https://charm.land/?utm_source=chatgpt.com "Charm"
[5]: https://github.com/rivo/tview?utm_source=chatgpt.com "rivo/tview: Terminal UI library with rich, interactive widgets"
[6]: https://github.com/spf13/cobra?utm_source=chatgpt.com "spf13/cobra: A Commander for modern Go CLI interactions"
[7]: https://github.com/aws/aws-sdk-go-v2?utm_source=chatgpt.com "aws/aws-sdk-go-v2"
[8]: https://github.com/aws/aws-sdk-go?utm_source=chatgpt.com "GitHub - aws/aws-sdk-go: This SDK has reached end-of- ..."
[9]: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/config?utm_source=chatgpt.com "config package - github.com/aws/aws-sdk-go-v2/config"
[10]: https://aws.amazon.com/blogs/developer/aws-sso-support-in-the-aws-sdk-for-go/?utm_source=chatgpt.com "AWS SSO Support in the AWS SDK for Go"
[11]: https://pkg.go.dev/modernc.org/sqlite?utm_source=chatgpt.com "sqlite package - modernc.org/sqlite"
[12]: https://github.com/mattn/go-sqlite3?utm_source=chatgpt.com "sqlite3 driver for go using database/sql"
[13]: https://sqlc.dev/?utm_source=chatgpt.com "Compile SQL to type-safe code | sqlc.dev"
[14]: https://docs.aws.amazon.com/resourcegroupstagging/latest/APIReference/API_GetResources.html?utm_source=chatgpt.com "GetResources - Resource Groups Tagging API"
[15]: https://docs.aws.amazon.com/cloudcontrolapi/latest/APIReference/API_ListResources.html?utm_source=chatgpt.com "ListResources - AWS Cloud Control API"
[16]: https://aws.amazon.com/resourceexplorer/?utm_source=chatgpt.com "AWS Resource Explorer – Amazon Web Services"
[17]: https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-retries-timeouts.html?utm_source=chatgpt.com "Retries and Timeouts - AWS SDK for Go v2"

