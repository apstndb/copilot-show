---
name: duckdb-jsonl-inspection
description: Investigate local JSON and JSONL datasets with DuckDB, especially Copilot CLI session-state event logs, type and ID relationships, parent-child lineage, and sparse JSON key distributions.
---

Use this skill when a user wants structured analysis of local JSON or JSONL files with DuckDB instead of ad-hoc line parsing.

This skill is especially useful for:

- `events.jsonl` investigation under `~/.copilot/session-state`
- combining `events.jsonl` with companion files such as `workspace.yaml` and `session.db`
- Finding how event `type` relates to `id`, `parentId`, and IDs stored inside sparse JSON payloads
- Summarizing schema, per-file distributions, and cross-file relationships
- Expanding arrays such as tool request lists and joining them to later execution events

## Core workflow

1. Inventory the candidate files first, then narrow quickly to the relevant JSON or JSONL paths.
2. Use DuckDB CLI with an in-memory database unless the dataset is large enough to justify a temporary database file.
3. Prefer:
   - `read_ndjson_auto(...)` for newline-delimited JSON
   - `read_json_auto(...)` for standard JSON files
   - `read_ndjson_objects(...)` or `read_json_objects(...)` when you want one raw JSON object per row and schema inference is getting in the way
   - `ignore_errors := true` when reading live or partially written NDJSON logs that may contain malformed lines
   - `union_by_name := true` when reading multiple files whose inferred schemas differ
   - the `filename` virtual column in current DuckDB releases when you need file provenance; `filename := true` is optional explicitness or compatibility
   - `sample_size := -1`, `map_inference_threshold := -1`, or `field_appearance_threshold` tuning when sparse objects are being inferred too aggressively as `MAP`
4. If DuckDB infers a column as `map(varchar, json)` because the payload is sparse or heterogeneous:
   - discover keys with `unnest(json_keys(to_json(column)))` to make the `MAP` to `JSON` conversion explicit
   - summarize combined structure with `json_group_structure(to_json(column))`
   - extract scalar values with `json_extract_string(column['key'], '$')`
   - use `json_each(column['arrayKey'])` to expand JSON arrays
   - use `json_tree(to_json(column))` for deep inspection of nested hierarchies; `fullkey` is especially useful for sparse event payloads
   - use `from_json(to_json(column), '{...}')` or `json_transform(...)` when you want a consistent subset of fields instead of many one-off extracts
   - qualify source-table columns such as `events.type` and `events.id` when using `json_each(...)` or `json_tree(...)`, because those table functions also emit columns named `type`, `id`, and `parent`
5. Always give the user both:
   - reusable DuckDB queries
   - interpreted findings, including caveats

## Companion session-state files

When working under `~/.copilot/session-state/<session-id>` do not assume `events.jsonl` is the only useful source.

- `workspace.yaml` can provide session metadata such as the human-readable summary and created or updated timestamps.
- `session.db` can provide per-session SQLite state if present.
- `files/**/*` can be counted with DuckDB `glob()` when the user wants workspace file totals.
- `inuse.<pid>.lock` can help relate a session to a process-specific log file name under `~/.copilot/logs/`, although the final log path is a filesystem convention rather than a JSONL field.

Useful extension combinations:

- `INSTALL yaml FROM community; LOAD yaml;` to read `workspace.yaml`
- `INSTALL sqlite; LOAD sqlite;` to query `session.db`
- `INSTALL httpfs; LOAD httpfs;` to read remote HTTP(S) JSON and attach authentication with `CREATE SECRET (TYPE http, ...)`
- `INSTALL http_client FROM community; LOAD http_client;` for explicit request/response handling when that community extension is available on the current DuckDB version and platform
- `INSTALL duckpgq FROM community; LOAD duckpgq;` to prototype property-graph queries over derived `events`, `interactions`, and `tool calls` tables when plain joins start to feel graph-shaped

When `read_yaml(...)` returns a single row whose interesting columns are `LIST`s, expand them with `unnest(...)`. This is useful for comparing saved YAML snapshots to live command output, for example `unnest(models)` or `unnest(usageItems)`.

When remote JSON is involved, distinguish simple file-style reads from explicit API requests:

- `read_json('https://...')` is convenient for anonymous JSON endpoints that behave like downloadable resources.
- For authenticated HTTP(S) endpoints, prefer `httpfs` plus `CREATE SECRET (TYPE http, BEARER_TOKEN ... )` or `EXTRA_HTTP_HEADERS` when you still want a file-style reader such as `read_json_auto('https://...')`.
- `http_client` is useful when you want an explicit request/response object instead of a file-style scan, but availability can vary by DuckDB version and platform.
- `httpfs` may issue `HEAD` requests and ranged `GET` requests while reading, so local test servers should handle both.
- Some API endpoints can change their `ETag` between DuckDB's initial metadata read and the later body read; if you hit an ETag mismatch on a volatile endpoint, `SET unsafe_disable_etag_checks = true;` can be a pragmatic workaround, but mention that you deliberately relaxed a safety check.

## Copilot session-state guidance

When analyzing `~/.copilot/session-state/*/events.jsonl`:

- derive the session folder ID from `filename` with `regexp_extract`
- expect the top-level schema to look roughly like:
  - `type`
  - `data`
  - `id`
  - `timestamp`
  - `parentId`
  - `filename` as a virtual column in current DuckDB releases
- expect `data` to often infer as `map(varchar, json)` because different event types use different payload keys
- note that current-session logs can grow while the investigation is running

For event-log work, produce these views unless the user asks for something narrower:

1. file or session summary
2. event type counts
3. payload structure summary overall and by event type
4. payload key distribution overall and by event type
5. `type` versus ID presence:
   - top-level `id`
   - top-level `parentId`
   - `data['sessionId']`
   - `data['interactionId']`
   - `data['turnId']`
   - `data['messageId']`
   - `data['toolCallId']`
   - `data['parentToolCallId']`
6. parent-child matrix by joining `child.parentId = parent.id`
7. tool-call lifecycle by joining:
   - `assistant.message.data.toolRequests[*].toolCallId`
   - `tool.execution_start.data.toolCallId`
   - `tool.execution_complete.data.toolCallId`
8. deep nested path discovery with `json_tree(...)` when payloads are large or heavily nested
9. control-event inspection for `skill.invoked`, `subagent.started`, `subagent.completed`, and `system.notification` in newer Copilot CLI logs
10. when needed, enrich the event-log view with `workspace.yaml`, `session.db`, and `glob()` counts to reconstruct more of the session overview

## Interpretation guidance

- Missing parents are meaningful. They can indicate truncated history, an event whose parent was not persisted, or an event chain that starts from a root node.
- `interactionId` and `turnId` are not guaranteed to appear on every logically related event type, so compare both presence and absence.
- `turnId` is not necessarily unique across a whole session, and `interactionId` can span many `assistant.turn_start` rows. For turn-level analysis, pair `assistant.turn_start` and `assistant.turn_end` by `(session, turnId, occurrence)` and assign tool activity by time window rather than grouping only by `interactionId`.
- If `assistant.turn_end` lacks `interactionId`, say so explicitly instead of assuming a join path that is not present in the data.
- Failed `tool.execution_complete` rows may still have a `NULL` or sparse `result`, so inspect the `success` flag first and treat the payload as optional detail rather than a guaranteed explanation.
- `toolCallId` joins are powerful but imperfect in live or mixed-history logs. You may see a started call that has not completed yet, or a completion row without a matching request or start event.
- `parentToolCallId` is separate from `parentId`. It can be the better join key when a nested tool or subagent emits child events under one parent tool invocation.
- Copilot event logs are closer to a multi-layer graph than a single tree: `parentId`, `parentToolCallId`, `toolCallId`, and `interactionId` represent different relationship types and should not be collapsed into one hierarchy too early.
- Newer logs may include `skill.invoked`, `subagent.started`, `subagent.completed`, and `system.notification`; treat these as control-plane events rather than ordinary user or assistant turns.
- DuckDB uses 0-based indexing for the `JSON` type but 1-based indexing for `ARRAY` and `LIST`, so call that out when users mix JSON arrays with extracted lists.
- When counts are changing because the current session is active, mention the observation window and that the dataset is live.
- If you are explaining `history`-style output, note that `tool.execution_complete` may not include `toolName`, so blank tool names are expected unless you join back to `tool.execution_start` on `toolCallId`.
- For `duckpgq`, keep unresolved `parentId` / `parentToolCallId` links in relational side tables and only build graph edges for rows whose referenced vertices actually exist. In current DuckDB builds, it is also safer to cast UUID-like JSONL IDs to `VARCHAR`, and every edge pattern in `MATCH` must bind to a variable.

## Output expectations

- Prefer concise tables or CSV snippets from DuckDB for raw facts.
- Then summarize the important structural findings in plain language.
- If you discover a reusable pattern, add it to `queries.md` so the skill improves over time.

## CLI usage tips

- For one-shot machine-readable output, prefer command-line flags such as `-csv`, `-json`, `-line`, `-markdown`, and `-noheader`.
- Use `.mode jsonlines` when you want NDJSON output from query results.
- Use `.once` or `.output` when a result set is large enough that copying from the terminal would be clumsy.
- Use `-readonly` when opening an existing DuckDB database file only for inspection. For pure JSON or JSONL analysis, `duckdb :memory:` remains a good default.

See also: [queries.md](queries.md)
