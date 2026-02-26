# awscope

AWS inventory scanner + interactive TUI for browsing resources and relationships.


<img width="2125" height="1195" alt="image" src="https://github.com/user-attachments/assets/44133479-62e6-4637-ba83-a391d9a6f51e" />



## Highlights

- Scan AWS accounts into a local SQLite inventory (`awscope scan`)
- Browse inventory in a fast TUI (`awscope tui`)
- Service -> Type navigation, paging, filtering
- Graph exploration (neighbors, incoming/outgoing relationships)
- Best-effort estimated monthly cost mode (Pricing API cache)
- Security posture findings from inventory (after scan and via `awscope security`)
- Offline mode for browsing cached inventories

## Install / Run

Go 1.25+:

```sh
go run ./cmd/awscope --help
```

Install from a GitHub release tarball (Linux/macOS):

```sh
VERSION="0.1.0"
OS="$(uname | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "unsupported arch: ${ARCH}" && exit 1 ;;
esac
curl -fsSL -o awscope.tar.gz "https://github.com/iesoftek/awscope/releases/download/v${VERSION}/awscope_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf awscope.tar.gz
./awscope version
```

Install via Homebrew cask (macOS):

```sh
brew tap iesoftek/tap
brew install --cask awscope
```

## Release Process

- Releases are automated with GitHub Actions + Release Please.
- `release-please` opens and updates a release PR on `main`, including `CHANGELOG.md`.
- Merging the release PR creates the `vX.Y.Z` tag and publishes a GitHub release.
- A release-published workflow builds and uploads cross-platform archives:
  - `linux/amd64`
  - `linux/arm64`
  - `darwin/amd64`
  - `darwin/arm64`
- The same workflow updates the Homebrew cask in `iesoftek/homebrew-tap`.

## PR Title Convention

PR titles must follow Conventional Commits and pass the `PR Title` workflow check.

Examples:

- `feat: add cloudtrail timeline filtering`
- `fix: handle empty region sets`
- `refactor!: simplify scan pipeline state`
- `docs: update release troubleshooting`

## Quickstart

Initial full scan:

```sh
go run ./cmd/awscope scan --profile default --regions all
```

Open inventory TUI (defaults to `tui` if subcommand is omitted):

```sh
go run ./cmd/awscope tui --profile default
```

Open interactive security findings viewer:

```sh
go run ./cmd/awscope security --profile default --tui
```

Print security findings in text mode:

```sh
go run ./cmd/awscope security --profile default
```

Export resources to CSV:

```sh
go run ./cmd/awscope export --format csv --profile default
```

Generate a region architecture diagram:

```sh
go run ./cmd/awscope diagram --profile default --region us-east-1 --format both
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
- `--services <csv>`: defaults to all supported services
- `--concurrency N`: max concurrent scan tasks (default 8)
- `--plain`: disable progress UI and print only the final summary
- `--offline`: never call AWS; browse cached inventory only
- `--db-path <path>`: override SQLite DB path
- `--security-view summary|detailed`: security findings section verbosity (default `summary`)
- `--security-color auto|always|never`: security findings color mode (default `auto`)

Audit indexing behavior:

- If `cloudtrail` is included in `--services`, scan also indexes create/delete management events into SQLite.
- Default indexed window is last `7` days; retention is rolling `30` days.
- Audit indexing uses CloudTrail `LookupEvents` with service-side filtering (`EventSource`) and a per-region cap/time budget.
- Optional tuning via environment variables:
  - `AWSCOPE_AUDIT_WINDOW_DAYS` (default `7`)
  - `AWSCOPE_AUDIT_MAX_EVENTS_PER_REGION` (default `1200`)
  - `AWSCOPE_AUDIT_MAX_REGION_DURATION_SEC` (default `120`)

Scan summary behavior:

- At the end of scan, awscope prints:
  - inventory summary (counts/regions/cost),
  - `security findings` (potential issues based on AWS best-practice controls),
  - performance summary (phase timings + slow steps).

Supported services (as of this repo state):

- `accessanalyzer, acm, apigateway, autoscaling, cloudfront, cloudtrail, config, dynamodb, ec2, ecr, ecs, efs, eks, elasticache, elbv2, guardduty, iam, identitycenter, kms, lambda, logs, msk, opensearch, rds, redshift, s3, sagemaker, secretsmanager, securityhub, sns, sqs, wafv2`


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
- `E`: Audit Events (CloudTrail create/delete explorer)
- `p`: pricing mode (adds totals + column)
- `T`: cycle theme
- `?`: help overlay
- `q`: quit

### Audit Events

- Open with `E`.
- Shows create/delete lifecycle management events (indexed during scan when `cloudtrail` is scanned).
- Uses current region selection for filtering.
- `enter` jumps to the target resource when the event can be resolved to inventory.
- If a target cannot be resolved, the event is still shown with raw identifiers.

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

## Security

Run security posture analysis from cached DB inventory (no live AWS calls):

```sh
go run ./cmd/awscope security --profile default
```

Interactive viewer:

```sh
go run ./cmd/awscope security --profile default --tui
```

Scoped examples:

```sh
go run ./cmd/awscope security --profile default --regions us-east-1,us-west-2 --services ec2,iam,s3
```

Flags:

- `--profile <name>`: required; scopes analysis to the profile-mapped account in DB
- `--regions <csv|all>`: optional; defaults to all account regions in DB
- `--services <csv>`: optional; restrict checks to selected service set
- `--max-key-age-days <n>`: optional IAM key-age threshold (default `90`)
- `--view summary|detailed`: collapse/expand finding details (default `detailed`)
- `--color auto|always|never`: output color mode (default `auto`)
- `--tui`: open interactive fullscreen security viewer

Security TUI keys:

- `j/k`, `up/down`: move selection
- `g/G`: top/bottom
- `/`: filter findings
- `e` or `space`: expand/collapse details
- `q`: quit

Environment:

- `AWSCOPE_SECURITY_MAX_KEY_AGE_DAYS` (default `90`)

Output semantics:

- Findings are potential posture issues, not compliance attestation.
- Results include severity, affected counts, sample resources, and coverage gaps.
- Coverage gaps are shown when required service inventory is missing in scope.

AWS guidance baseline used for checks:

- Well-Architected Security Pillar: https://docs.aws.amazon.com/wellarchitected/latest/security-pillar/welcome.html
- IAM Best Practices: https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html
- Security Hub controls reference: https://docs.aws.amazon.com/securityhub/latest/userguide/securityhub-controls-reference.html

Implemented v1.1 checks (mapped to AWS guidance families):

- CloudTrail: `CT-001/002/003` (logging, multi-region trail, log-file validation)
- AWS Config: `CFG-001` (recorder active coverage)
- GuardDuty: `GD-001` (detector enabled coverage)
- Security Hub: `SH-001` (hub enabled coverage)
- Access Analyzer: `AA-001` (analyzer active coverage)
- S3: `S3-001/002` (public-access-block posture, default encryption)
- RDS: `RDS-001/002` (public accessibility, storage encryption)
- IAM: `IAM-001/002` (console users without MFA, old active access keys)
- Secrets Manager: `SEC-001` (rotation disabled)
- EKS: `EKS-001` (public API exposure)
- EC2: `EC2-001/002` (public IP on running instance, world-open SG ingress)

## Actions

List available actions:

```sh
go run ./cmd/awscope action list
```

Run SSM shell action from CLI:

```sh
go run ./cmd/awscope action run --id ec2.ssm-shell --key <resource_key> --profile default --confirm
```

Run SSH via EC2 Instance Connect from CLI:

```sh
go run ./cmd/awscope action run --id ec2.ssh --key <resource_key> --profile default --confirm
```

Run SQS mirror stream action from CLI (never reads the primary queue directly):

```sh
go run ./cmd/awscope action run --id sqs.mirror-stream --key <resource_key> --profile default --confirm
```

Run SNS stream action from CLI (live capture only via ephemeral SQS subscriber):

```sh
go run ./cmd/awscope action run --id sns.stream --key <resource_key> --profile default --confirm
```

Run SSM shell action from TUI:

- select a running EC2 instance
- press `A` (actions)
- choose `Open SSM shell`
- confirm by typing the resource ID and pressing enter

When the interactive session exits, awscope regains terminal control automatically.

SQS mirror stream behavior:

- Target resource must be an `sqs:queue` subscribed to at least one SNS topic.
- awscope discovers SNS->SQS fanout, creates/updates a mirror queue path, streams from that mirror queue, and prompts at each mutating step.
- Stream reads mirrored messages and deletes them from the mirror queue after printing.
- Primary queue is never read by this action.
- On exit, awscope prompts to unsubscribe session-created subscriptions and delete the session-created mirror queue.

SNS stream behavior:

- Target resource must be an `sns:topic`.
- awscope creates or reuses an ephemeral SQS stream queue, patches queue policy for the topic, subscribes if needed, then streams from that queue.
- This is live capture only; SNS does not provide historical replay.
- Stream consumes/deletes messages from the ephemeral stream queue, not from your existing consumers.
- On exit, awscope prompts to unsubscribe session-created subscriptions and delete the session-created stream queue.

Prerequisites for terminal EC2 actions:

- AWS CLI v2 installed and available in `PATH`
- `ec2.ssm-shell`: `session-manager-plugin` installed and available in `PATH`
- `ec2.ssh`: OpenSSH client available in `PATH` and a public key at `~/.ssh/id_rsa.pub` (or `~/.ssh/id_ed25519.pub`)
- IAM permissions to start SSM sessions (for example `ssm:StartSession` and related session channel permissions)
- IAM permissions for EC2 Instance Connect (`ec2-instance-connect:SendSSHPublicKey`) and EC2 Instance Connect Endpoint/open-tunnel access as applicable
- `sns.stream`: `sns:GetTopicAttributes`, `sns:ListSubscriptionsByTopic`, `sns:Subscribe`, `sns:Unsubscribe`, `sqs:GetQueueUrl`, `sqs:GetQueueAttributes`, `sqs:CreateQueue`, `sqs:SetQueueAttributes`, `sqs:ReceiveMessage`, `sqs:DeleteMessageBatch`, `sqs:DeleteQueue`
- `sqs.mirror-stream`: `sqs:GetQueueAttributes`, `sqs:GetQueueUrl`, `sqs:CreateQueue`, `sqs:SetQueueAttributes`, `sqs:DeleteQueue`, `sqs:ReceiveMessage`, `sqs:DeleteMessageBatch`, `sns:ListSubscriptions`, `sns:ListSubscriptionsByTopic`, `sns:Subscribe`, `sns:Unsubscribe`

Tip:

- If `ec2.ssm-shell` returns `TargetNotConnected`, prefer `ec2.ssh` for instances reachable via EC2 Instance Connect.

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

## Diagram

Generate architecture diagrams for a single region from scanned graph data:

```sh
go run ./cmd/awscope diagram --profile default --region us-east-1 --format both
```

Key flags:

- `--region <region>`: required region scope
- `--profile <name>`: optional account scope; required when DB contains multiple accounts
- `--format graphviz|mermaid|both`: output source format(s)
- `--view overview|network|eventing|security|full`: projection profile (default `overview`)
- `--full`: alias for `--view full`
- `--include-isolated summary|full|none`: how isolated nodes are handled (default `summary`)
- `--component-limit <n>`: number of disconnected components to keep before summarizing (default `3`)
- `--no-fold`: disable leaf/parallel-edge folding
- `--layout dot|sfdp`: layout engine (`sfdp` is supported for `view=full`)
- `--max-nodes`, `--max-edges`: caps (`0` uses view defaults; unlimited in full unless explicitly set)
- `--include-global-linked`: include global resources directly linked to region resources (default true)
- `--render`: render SVG when renderer binaries are available (default true)

Rendering dependencies (optional):

- Graphviz SVG rendering uses `dot`
- Mermaid SVG rendering uses `mmdc`

If render binaries are unavailable, `awscope` still writes source files (`.dot`/`.mmd`) and prints warnings.

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

### Permissions

Scan is best-effort: if some APIs are `AccessDenied`, scan continues for other services and prints an error summary.

IAM (for users/groups/access keys) uses these APIs:

- `iam:ListUsers`
- `iam:ListAccessKeys`
- `iam:GetAccessKeyLastUsed`
- `iam:ListGroups`
- `iam:ListGroupsForUser`
- `iam:GenerateCredentialReport`
- `iam:GetCredentialReport`

Auto Scaling uses:

- `autoscaling:DescribeAutoScalingGroups`
- `autoscaling:DescribeAutoScalingInstances`
- `autoscaling:DescribeLaunchConfigurations`

SageMaker uses list/describe APIs for:

- notebook instances, models, endpoint configs, endpoints
- training jobs, processing jobs, transform jobs
- domains and user profiles

Identity Center uses:

- `sso:ListInstances`
- `sso:ListPermissionSets`
- `sso:DescribePermissionSet`
- `sso:ListAccountsForProvisionedPermissionSet`
- `sso:ListAccountAssignments`
- `identitystore:ListUsers`
- `identitystore:ListGroups`
- `identitystore:ListGroupMemberships`

Note: Identity Center has a home region. Use `--regions all` (or include the home region explicitly) to discover Identity Center resources.

CloudTrail uses:

- `cloudtrail:DescribeTrails`
- `cloudtrail:GetTrailStatus`
- `cloudtrail:LookupEvents` (for Audit Events indexing)

AWS Config uses:

- `config:DescribeConfigurationRecorders`
- `config:DescribeConfigurationRecorderStatus`
- `config:DescribeDeliveryChannels`

GuardDuty uses:

- `guardduty:ListDetectors`
- `guardduty:GetDetector`

Security Hub uses:

- `securityhub:DescribeHub`
- `securityhub:GetEnabledStandards`

IAM Access Analyzer uses:

- `access-analyzer:ListAnalyzers`

WAFv2 uses:

- `wafv2:ListWebACLs`

ACM uses:

- `acm:ListCertificates`
- `acm:DescribeCertificate`

CloudFront uses:

- `cloudfront:ListDistributions`

API Gateway (REST) uses:

- `apigateway:GET` (for `GetRestApis`)

ECR uses:

- `ecr:DescribeRepositories`

EKS uses:

- `eks:ListClusters`
- `eks:DescribeCluster`

ElastiCache uses:

- `elasticache:DescribeReplicationGroups`
- `elasticache:DescribeCacheClusters`

OpenSearch uses:

- `es:ListDomainNames`
- `es:DescribeDomain`

Redshift uses:

- `redshift:DescribeClusters`

MSK uses:

- `kafka:ListClustersV2`

EFS uses:

- `elasticfilesystem:DescribeFileSystems`
- `elasticfilesystem:DescribeMountTargets`
- `elasticfilesystem:DescribeMountTargetSecurityGroups`

Architecture and contributor docs:

- `docs/ARCHITECTURE.md`
- `docs/CODE_STRUCTURE.md`
- `docs/DEVELOPMENT.md`
- `docs/adr/ADR-0001-layer-boundaries.md`
- `docs/adr/ADR-0002-service-catalog.md`
- `docs/adr/ADR-0003-scan-stage-pipeline.md`
