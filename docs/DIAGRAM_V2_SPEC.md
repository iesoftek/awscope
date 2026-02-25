# awscope Diagram v2 Spec

## 1. Purpose

Make `awscope diagram` produce cleaner, more organized architecture diagrams by default, while still supporting full/raw exports.

Primary output remains Graphviz DOT/SVG. Mermaid stays a companion output for docs.

## 2. Baseline Findings (from current implementation)

Observed on recent `us-west-2` sample:

- ~`401` rendered nodes and `324` rendered edges in condensed output source.
- `196` connected components, with `180` isolated single-node components.
- Largest edge kinds:
  - `member-of` ~`41%`
  - `forwards-to` ~`13.6%`
  - `attached-to` ~`12.5%`
  - `targets` ~`11%`
- High-count node types with low topology signal in region view:
  - `logs:log-group`, `sns:subscription`, `elbv2:rule`, `s3:bucket`, `secretsmanager:secret`

Conclusion: readability is primarily blocked by noisy leaves and fragmented components, not by renderer bugs.

## 3. Design Goals

1. Produce a readable diagram by default without requiring manual flags.
2. Preserve topology intent (entrypoints, workloads, data stores, security boundaries).
3. Keep deterministic output (same DB + flags -> same diagram).
4. Support multiple curated views instead of one universal projection.

## 4. Canonical Pipeline (single model, multiple renderers)

Render pipeline:

1. Load scoped graph (`account + region (+linked global)`).
2. Apply view profile (`overview|network|eventing|security|full`).
3. Prune/filter.
4. Fold noisy leaves and parallel edges.
5. Select components.
6. Cap by rank score when needed.
7. Render (Graphviz and/or Mermaid) from the same processed model.

This pipeline is deterministic and testable at each stage.

## 5. View Definitions (locked)

### 5.1 `overview` (default)

For fast architecture understanding.

- Include types:
  - `ec2:vpc`, `ec2:subnet`, `ec2:instance`, `ec2:security-group`, `ec2:volume`
  - `ecs:cluster`, `ecs:service`
  - `elbv2:load-balancer`, `elbv2:listener`, `elbv2:target-group`
  - `rds:db-instance`, `rds:db-cluster`
  - `lambda:function`, `dynamodb:table`, `sqs:queue`, `sns:topic`, `s3:bucket`
  - `kms:key`, `secretsmanager:secret`, `iam:role`, `iam:user`, `iam:group`
  - `logs:log-group`
- Include edge kinds:
  - `member-of`, `attached-to`, `uses`, `targets`, `forwards-to`, `contains`, `belongs-to`
- Fold types by default:
  - `logs:log-group`, `sns:subscription`, `elbv2:rule`, `ecs:task`, `ecs:task-definition`, `iam:policy`, `iam:access-key`, `kms:alias`
- Component policy:
  - Keep top 3 components by edge count.
  - Always keep components containing any ingress/workload anchor type.
  - Show isolated resources as summary boxes per `(service,type)`; do not render each isolated node.
- Default caps:
  - `max-nodes=240`, `max-edges=420`

### 5.2 `network`

For VPC/subnet path and data-plane connectivity.

- Include types:
  - `ec2:vpc`, `ec2:subnet`, `ec2:security-group`, `ec2:instance`, `ec2:volume`
  - `elbv2:load-balancer`, `elbv2:listener`, `elbv2:target-group`
  - `ecs:service`, `lambda:function`, `rds:db-instance`, `rds:db-cluster`
- Include edge kinds:
  - `member-of`, `attached-to`, `targets`, `forwards-to`, `contains`, `uses`
- Fold:
  - `elbv2:rule`, `ecs:task`, `ecs:task-definition`, `logs:log-group`
- Default caps:
  - `max-nodes=280`, `max-edges=520`

### 5.3 `eventing`

For async/event flow.

- Include types:
  - `sns:topic`, `sns:subscription`, `sqs:queue`, `lambda:function`, `dynamodb:table`, `s3:bucket`, `ecs:service`
- Include edge kinds:
  - `targets`, `uses`, `member-of`, `contains`
- Fold:
  - none for `sns:subscription` in this view (subscriptions are signal here).
- Default caps:
  - `max-nodes=260`, `max-edges=500`

### 5.4 `security`

For IAM/KMS/secrets trust and usage.

- Include types:
  - `iam:user`, `iam:group`, `iam:role`, `iam:policy`, `iam:access-key`
  - `kms:key`, `kms:alias`, `secretsmanager:secret`, `lambda:function`, `ec2:instance`, `rds:db-instance`, `s3:bucket`
- Include edge kinds:
  - `member-of`, `attached-to`, `uses`, `contains`
- Fold:
  - `iam:access-key` folded into users by default when user has >5 keys shown.
- Default caps:
  - `max-nodes=260`, `max-edges=500`

### 5.5 `full`

Raw graph projection.

- Include all types and all edges in scope.
- No folding.
- Caps only if explicitly passed by user.

## 6. Exact Pruning and Folding Rules

### 6.1 Pre-filter

Drop nodes/edges not in view allowlists (except `full`).

### 6.2 Leaf folding

A node is fold-eligible when all are true:

1. Type is in view fold list.
2. Degree <= 1 after pre-filter.
3. Not user-pinned root (future interactive option).

Fold target:

- If one parent exists, aggregate under parent and edge kind.
- If isolated, aggregate into region/service-type summary.

Aggregate node label format:

- `<type short> x<count>`
- Example: `log groups x55`, `subscriptions x49`

### 6.3 Parallel edge folding

For each `(from, to, kind)`, merge duplicates into one edge with `count`.

For folded groups:

- Emit one edge per `(fromGroup, toGroup, kind)` with `count`.
- Edge label in overview/network:
  - show label only if `count > 1` or kind in `{targets,forwards-to,uses}`.

### 6.4 Component selection

For non-full views:

1. Build undirected components on post-fold graph.
2. Mark keep components:
  - top 3 by edge count, plus
  - any containing anchor types:
    - `elbv2:load-balancer`, `elbv2:target-group`, `ecs:service`, `ec2:instance`, `lambda:function`, `rds:db-instance`, `rds:db-cluster`
3. Non-kept components:
  - convert to summary notes by service/type counts.

### 6.5 Rank-based capping

If still above caps, rank nodes:

`score = 5*anchor + 3*workload + 2*entrypoint + 2*criticalStatus + log2(1+degree) + min(2, log10(1+estMonthlyUSD))`

Where:

- `anchor=1` for VPC/subnet/LB/TG/service/instance/db/lambda.
- `workload=1` for compute/db/function/service.
- `entrypoint=1` for LB/listener/topic.
- `criticalStatus=1` for failing/degraded-like states.

Keep highest scoring nodes; edges are retained only when both endpoints remain.
Tie-break: `service`, `type`, `name`, `key` ascending.

## 7. Layout Profiles

### 7.1 Graphviz default profile (`overview/network/eventing/security`)

Use:

- `rankdir=LR`
- `compound=true`
- `newrank=true`
- `concentrate=true`
- `splines=ortho` (fallback to `spline` if routing quality is poor in a view)
- `nodesep=0.35`
- `ranksep=0.65`
- `pad=0.2`
- `pack=true`
- `packmode=clust`

Clustering:

1. Region cluster.
2. VPC clusters.
3. Subnet subclusters under VPC.
4. Service clusters for non-VPC-contained nodes.
5. Linked global cluster.

Edge constraints:

- Structural edges (`member-of`, `contains`) constrain rank.
- Informational edges (`uses`) set `constraint=false` unless they are only path between major anchors.

### 7.2 Graphviz full profile

Use:

- `splines=true`
- `concentrate=false`
- less aggressive packing.

### 7.3 Mermaid profile

Mermaid is companion and should mirror the processed model.

- Default to `flowchart LR`.
- For large outputs (`nodes > 180`), include warning header comment.
- Render only `overview`/`network` by default when format is Mermaid unless user explicitly requests `--view full`.

## 8. Visual Grammar

1. Node title: `display_name`
2. Secondary line: `type`
3. Optional third line: status badge text if non-empty.
4. Keep long IDs/ARNs out of labels; include as tooltip metadata in DOT.
5. Service family colors should be muted and consistent across both renderers.

## 9. CLI Contract Changes (v2)

Add flags:

- `--view overview|network|eventing|security|full` (default `overview`)
- `--include-isolated summary|full|none` (default `summary`)
- `--layout dot|sfdp` (default `dot`; `sfdp` only for `full`)
- `--no-fold` (disable leaf/edge folding for debugging)
- `--component-limit int` (default `3`, ignored by `full`)

Existing flags remain valid; `--full` becomes alias for `--view full`.

## 10. Determinism Rules

1. All maps sorted before output.
2. Stable node IDs assigned from sorted `(region,service,type,name,key)`.
3. Stable edge ordering `(kind,from,to)`.
4. Stable component ordering by `(edgeCount desc, nodeCount desc, minNodeKey asc)`.

## 11. Acceptance Criteria

1. Default `overview` for medium/large regions should stay under:
  - 240 visible nodes
  - 420 visible edges
  unless user passes larger caps.
2. Generated SVG should be materially less tall/wide than current condensed baseline for the same scope.
3. High-signal chains remain visible:
  - LB -> target group -> service/instance
  - service/function -> queue/topic/table
  - workload -> KMS/secret/role (if edges exist)
4. Isolated resources are represented as concise summaries, not hundreds of separate nodes.

## 12. Implementation Order

1. Add `ViewProfile` definitions and pipeline stages in `internal/diagram/model.go`.
2. Implement leaf and edge folding.
3. Implement component selection and summary-note generation.
4. Add Graphviz layout profile tuning.
5. Add CLI flags and backward-compatible aliases.
6. Add Mermaid guardrails and warnings.
7. Add tests:
  - view filtering
  - fold determinism
  - cap determinism
  - snapshot tests for overview/network/full.

