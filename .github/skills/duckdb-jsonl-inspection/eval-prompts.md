# Suggested eval prompts

Use these prompts to sanity-check whether the skill is being triggered and whether it helps produce useful DuckDB-driven analysis.

## Prompt 1

Inspect `~/.copilot/session-state/*/events.jsonl` with DuckDB and summarize the event type distribution, the top payload keys, and which IDs each event type carries.

## Prompt 2

Use the `duckdb-jsonl-inspection` skill to find how `parentId` links event types in Copilot session logs, and tell me which event types most often point to missing parents.

## Prompt 3

Analyze `events.jsonl` with DuckDB and trace tool-call lifecycles from `assistant.message.data.toolRequests[*].toolCallId` to `tool.execution_start` and `tool.execution_complete`.

## Prompt 4

Compare multiple Copilot session-state `events.jsonl` files with DuckDB and tell me which event types appear in only one session versus all sessions.

## Prompt 5

Find all non-successful `tool.execution_complete` events in Copilot session logs with DuckDB, and explain what fields are actually present when a completion fails.

## Prompt 6

Use DuckDB to compare `read_ndjson_auto(...)` against `read_ndjson_objects(...)` for Copilot `events.jsonl`, and explain when raw-object mode is a better fit than auto-unpacked columns.

## Prompt 7

Investigate nested Copilot event payloads with DuckDB `json_group_structure(...)` and `json_tree(...)`, and tell me which nested paths appear most often by event type.

## Prompt 8

Use DuckDB with the YAML and SQLite extensions to combine `workspace.yaml`, `session.db`, and `events.jsonl` into a single session summary for one Copilot session-state directory.

## Prompt 9

Use DuckDB `httpfs` and `CREATE SECRET` to call a JSON REST API with authentication headers, and explain when this is a better fit than unauthenticated `read_json('https://...')`.

## Prompt 10

Compare `httpfs + CREATE SECRET` against the optional `http_client` community extension for authenticated HTTP GETs, including any version or platform caveats you find.

## Prompt 11

Use DuckDB to inspect `https://api.github.com/copilot_internal/user` with an authenticated `httpfs` secret, and explain any `ETag` or volatile-endpoint caveats that show up while reading it.

## Prompt 12

Use DuckDB to reconstruct `assistant.turn_start` / `assistant.turn_end` windows from Copilot `events.jsonl`, explain why `turnId` and `interactionId` can both be reused, and compare that to what `turns` or `history`-style output would show.

## Prompt 13

Use DuckDB with the `duckpgq` community extension to turn one Copilot `events.jsonl` file into a property graph with `Event`, `Interaction`, and `ToolCall` vertices, then explain which relationships are worth modeling as graph edges and which missing links must remain a relational caveat.
