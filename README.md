# awscope

AWS inventory scanner + interactive TUI for browsing resources and relationships.

## Highlights

- Scan AWS accounts into a local SQLite inventory (`awscope scan`)
- Browse inventory in a fast TUI (`awscope tui`)
- Service -> Type navigation, paging, filtering
- Graph exploration (neighbors, incoming/outgoing relationships)
- Best-effort estimated monthly cost mode (Pricing API cache)
- Offline mode for browsing cached inventories

## Install / Run

Go 1.25+:

```sh
go run ./cmd/awscope --help
```

Run the TUI (defaults to `tui` if you omit subcommand):

```sh
go run ./cmd/awscope tui --profile default
```

## Scan

Scan populates the local SQLite DB (default path is OS-specific).

Example:

```sh
go run ./cmd/awscope scan --profile default --regions all --services ec2,ecs,elbv2,iam,logs,rds,s3,kms,secretsmanager,sqs,sns,dynamodb,lambda
```

Common flags:

- `--profile <name>`: AWS profile (uses default chain if empty)
- `--regions <csv|all>`: required; supports `all`
- `--services <csv>`: defaults to `ec2`
- `--concurrency N`: max concurrent scan tasks (default 8)
- `--plain`: disable progress UI and print only the final summary
- `--offline`: never call AWS; browse cached inventory only
- `--db-path <path>`: override SQLite DB path

Supported services (as of this repo state):

- `dynamodb, ec2, ecs, elbv2, iam, kms, lambda, logs, rds, s3, secretsmanager, sns, sqs`

### Permissions

Scan is best-effort: if some APIs are `AccessDenied`, scan continues for other services and prints an error summary.

## TUI

Start:

```sh
go run ./cmd/awscope tui --profile default
```

### Keys

- `tab`: focus next pane
- `1/2/3`: focus Navigator / Resources / Details
- `/`: filter resources
- `R`: region picker
- `g`: Graph Lens (incoming/outgoing grouped neighbors)
- `p`: pricing mode (adds totals + column)
- `T`: cycle theme
- `?`: help overlay
- `q`: quit

### Icons

Default icon mode is **Nerd Font**.

- Env: `AWSCOPE_ICONS=ascii|nerd|none`
- Flag: `awscope tui --icons ascii|nerd|none`

If you don't have a Nerd Font configured in your terminal, set:

```sh
AWSCOPE_ICONS=ascii
```

### Pricing (estimates)

Pricing mode is a rough estimate intended for "directionally correct" exploration, not billing.

- Uses AWS Pricing API in `us-east-1`
- Caches lookups in SQLite (`pricing_cache`)
- Stores per-resource estimates in SQLite (`resource_costs`)

Notes:

- Many services are usage-based; those will show unknown or partial estimates.
- CloudWatch Logs estimate is storage-only (from `storedBytes`), excluding ingestion/insights/vended logs/etc.

## Export

Export the latest inventory snapshot from SQLite:

```sh
go run ./cmd/awscope export --format json --out awscope-export.json
```

Export resources as CSV:

```sh
go run ./cmd/awscope export --format csv
```

Profile-scoped export (filters by the profile's account in the DB):

```sh
go run ./cmd/awscope export --format csv --profile default --out default.csv
```

Notes:

- If `--out` is omitted, the file is written to the current directory as `awscope-export-<profile|all>-<timestamp>.<ext>`.
- CSV includes `tags_json`, `attributes_json`, and `raw_json` columns for detailed inspection.

## DB / Offline

The TUI can browse cached data with:

```sh
go run ./cmd/awscope tui --profile default --offline
```

## Development

Run tests:

```sh
go test ./...
```
