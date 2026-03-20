# DuckDB query patterns for JSONL investigation

These recipes assume:

- newline-delimited JSON files
- DuckDB CLI
- a shell variable such as `GLOB="$HOME/.copilot/session-state/*/events.jsonl"`

Current DuckDB releases expose `filename` as a virtual column automatically when reading JSON files.

For the full query text, see the individual `.sql` files in `queries/`.

## Schema and discovery (1–4)

| # | Name | Description | File |
|---|------|-------------|------|
| 1 | Per-file summary | Count events per session with first/last timestamps | [queries/01-per-file-summary.sql](queries/01-per-file-summary.sql) |
| 2 | Event type counts | Frequency of each event type across all files | [queries/02-event-type-counts.sql](queries/02-event-type-counts.sql) |
| 3 | Sparse payload key distribution | Key occurrence counts when `data` is inferred as `MAP` | [queries/03-sparse-payload-key-distribution.sql](queries/03-sparse-payload-key-distribution.sql) |
| 4 | Keys by event type | Which payload keys each event type uses | [queries/04-keys-by-event-type.sql](queries/04-keys-by-event-type.sql) |

## Relationships and lifecycles (5–10)

| # | Name | Description | File |
|---|------|-------------|------|
| 5 | Type versus ID presence | Which event types carry each ID field | [queries/05-type-vs-id-presence.sql](queries/05-type-vs-id-presence.sql) |
| 6 | Parent-child event matrix | How event types link via `parentId` | [queries/06-parent-child-event-matrix.sql](queries/06-parent-child-event-matrix.sql) |
| 7 | Tool request to execution lifecycle | End-to-end counts from request through start to completion | [queries/07-tool-request-lifecycle.sql](queries/07-tool-request-lifecycle.sql) |
| 8 | Missing-parent rates by event type | How often each event type points to a missing parent | [queries/08-missing-parent-rates.sql](queries/08-missing-parent-rates.sql) |
| 9 | Failed tool completions | Events where `success` is not `true` | [queries/09-failed-tool-completions.sql](queries/09-failed-tool-completions.sql) |
| 10 | Incomplete tool lifecycles | Tool calls missing a request, start, or completion event | [queries/10-incomplete-tool-lifecycles.sql](queries/10-incomplete-tool-lifecycles.sql) |

## Payload analysis (11–13)

| # | Name | Description | File |
|---|------|-------------|------|
| 11 | Payload structure summary with json_group_structure | Combined JSON shape per event type via `json_group_structure` | [queries/11-payload-structure-summary.sql](queries/11-payload-structure-summary.sql) |
| 12 | Deep nested path discovery with json_tree | All nested JSON paths with occurrence counts via `json_tree` | [queries/12-deep-nested-path-discovery.sql](queries/12-deep-nested-path-discovery.sql) |
| 13 | Normalize a common subset with from_json | Extract a consistent subset of fields with `from_json` | [queries/13-normalize-common-subset.sql](queries/13-normalize-common-subset.sql) |

## Lineage and control flow (14–16)

| # | Name | Description | File |
|---|------|-------------|------|
| 14 | Parent tool lineage | Child events grouped by their parent `toolCallId` | [queries/14-parent-tool-lineage.sql](queries/14-parent-tool-lineage.sql) |
| 15 | Control-event field summary | `skill.invoked`, `subagent.*`, `system.notification` field summary | [queries/15-control-event-field-summary.sql](queries/15-control-event-field-summary.sql) |
| 16 | System notification kinds | Breakdown of `system.notification` kinds and agent statuses | [queries/16-system-notification-kinds.sql](queries/16-system-notification-kinds.sql) |

## Companion files (17–20)

| # | Name | Description | File |
|---|------|-------------|------|
| 17 | Read workspace.yaml with the YAML extension | Load session metadata from `workspace.yaml` | [queries/17-read-workspace-yaml.sql](queries/17-read-workspace-yaml.sql) |
| 18 | Read session.db with the SQLite extension | Query SQLite state alongside JSONL | [queries/18-read-session-db.sql](queries/18-read-session-db.sql) |
| 19 | Count workspace files with glob() | Count files in the session workspace | [queries/19-count-workspace-files.sql](queries/19-count-workspace-files.sql) |
| 20 | Multisource session summary | Combine `workspace.yaml`, `events.jsonl`, and `session.db` | [queries/20-multisource-session-summary.sql](queries/20-multisource-session-summary.sql) |

## Remote HTTP (21–22)

| # | Name | Description | File |
|---|------|-------------|------|
| 21 | Authenticated HTTP GET with httpfs and CREATE SECRET | Read JSON APIs with bearer-token auth via `httpfs` | [queries/21-httpfs-authenticated-get.sql](queries/21-httpfs-authenticated-get.sql) |
| 22 | Explicit HTTP request/response with the optional http_client extension | Full response object with status and body via `http_client` | [queries/22-http-client-explicit-request.sql](queries/22-http-client-explicit-request.sql) |

## Session analysis (23–25)

| # | Name | Description | File |
|---|------|-------------|------|
| 23 | Reconstruct turn windows for turns/history analysis | Pair `turn_start`/`turn_end` by occurrence and assign tools by time window | [queries/23-reconstruct-turn-windows.sql](queries/23-reconstruct-turn-windows.sql) |
| 24 | Prototype a property graph with DuckPGQ | Build event/interaction/tool-call vertices and edges with DuckPGQ | [queries/24-duckpgq-property-graph.sql](queries/24-duckpgq-property-graph.sql) |
| 25 | Reconstruct billable user-message windows and weighted premium requests | Reconcile shutdown `totalPremiumRequests` to per-window model weights | [queries/25-billable-user-message-windows.sql](queries/25-billable-user-message-windows.sql) |
