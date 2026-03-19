# DuckDB query patterns for JSONL investigation

These recipes assume:

- newline-delimited JSON files
- DuckDB CLI
- a shell variable such as `GLOB="$HOME/.copilot/session-state/*/events.jsonl"`

Current DuckDB releases expose `filename` as a virtual column automatically when reading JSON files.

## 1. Per-file summary

```sql
SELECT
  regexp_extract(filename, '.*/session-state/([^/]+)/events\\.jsonl$', 1) AS file_session_id,
  count(*) AS events,
  count(DISTINCT type) AS types,
  min(timestamp) AS first_ts,
  max(timestamp) AS last_ts
FROM read_ndjson_auto($glob)
GROUP BY 1
ORDER BY events DESC, file_session_id;
```

## 2. Event type counts

```sql
SELECT type, count(*) AS rows
FROM read_ndjson_auto($glob)
GROUP BY 1
ORDER BY rows DESC, type;
```

## 3. Sparse payload key distribution

Use this when a payload column was inferred as `map(varchar, json)`.

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT key, count(*) AS rows
FROM (
  SELECT unnest(json_keys(to_json(data))) AS key
  FROM events
)
GROUP BY 1
ORDER BY rows DESC, key;
```

`to_json(data)` is explicit and docs-backed when `data` was inferred as `MAP` rather than `JSON`.

## 4. Keys by event type

```sql
WITH events AS (
  SELECT type, data
  FROM read_ndjson_auto($glob)
),
typed_keys AS (
  SELECT type, unnest(json_keys(to_json(data))) AS key
  FROM events
)
SELECT
  type,
  string_agg(DISTINCT key, ', ' ORDER BY key) AS keys
FROM typed_keys
GROUP BY 1
ORDER BY type;
```

## 5. Type versus ID presence

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
),
norm AS (
  SELECT
    type,
    id,
    parentId,
    json_extract_string(data['sessionId'], '$') AS data_session_id,
    json_extract_string(data['interactionId'], '$') AS data_interaction_id,
    json_extract_string(data['turnId'], '$') AS data_turn_id,
    json_extract_string(data['messageId'], '$') AS data_message_id,
    json_extract_string(data['toolCallId'], '$') AS data_tool_call_id
  FROM events
)
SELECT
  type,
  count(*) AS rows,
  count(id) AS rows_with_id,
  count(parentId) AS rows_with_parent_id,
  count_if(data_session_id IS NOT NULL) AS rows_with_data_session_id,
  count_if(data_interaction_id IS NOT NULL) AS rows_with_data_interaction_id,
  count_if(data_turn_id IS NOT NULL) AS rows_with_data_turn_id,
  count_if(data_message_id IS NOT NULL) AS rows_with_data_message_id,
  count_if(data_tool_call_id IS NOT NULL) AS rows_with_data_tool_call_id
FROM norm
GROUP BY 1
ORDER BY rows DESC, type;
```

## 6. Parent-child event matrix

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT
  child.type AS child_type,
  coalesce(parent.type, '[missing]') AS parent_type,
  count(*) AS rows
FROM events child
LEFT JOIN events parent
  ON child.parentId = parent.id
WHERE child.parentId IS NOT NULL
GROUP BY 1, 2
ORDER BY rows DESC, child_type, parent_type;
```

## 7. Tool request to execution lifecycle

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
),
requests AS (
  SELECT
    events.id AS assistant_event_id,
    json_extract_string(events.data['messageId'], '$') AS assistant_message_id,
    json_extract_string(j.value, '$.toolCallId') AS tool_call_id,
    json_extract_string(j.value, '$.name') AS requested_tool_name
  FROM events,
       json_each(events.data['toolRequests']) AS j
  WHERE events.type = 'assistant.message'
    AND events.data['toolRequests'] IS NOT NULL
),
starts AS (
  SELECT
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['toolName'], '$') AS tool_name
  FROM events
  WHERE type = 'tool.execution_start'
),
completes AS (
  SELECT
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['model'], '$') AS model,
    CAST(data['success'] AS VARCHAR) AS success_json
  FROM events
  WHERE type = 'tool.execution_complete'
)
SELECT
  requested_tool_name,
  count(*) AS requests,
  count(starts.tool_call_id) AS starts,
  count(completes.tool_call_id) AS completes
FROM requests
LEFT JOIN starts USING (tool_call_id)
LEFT JOIN completes USING (tool_call_id)
GROUP BY 1
ORDER BY requests DESC, requested_tool_name;
```

## 8. Missing-parent rates by event type

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT
  child.type AS child_type,
  count(*) AS rows_with_parent_id,
  count_if(parent.id IS NULL) AS missing_parent_rows,
  round(100.0 * count_if(parent.id IS NULL) / count(*), 1) AS missing_parent_pct
FROM events child
LEFT JOIN events parent
  ON child.parentId = parent.id
WHERE child.parentId IS NOT NULL
GROUP BY 1
ORDER BY missing_parent_pct DESC, rows_with_parent_id DESC, child_type;
```

## 9. Failed tool completions

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT
  json_extract_string(data['toolCallId'], '$') AS tool_call_id,
  json_extract_string(data['model'], '$') AS model,
  left(
    replace(replace(CAST(data['result'] AS VARCHAR), chr(10), ' '), chr(13), ' '),
    220
  ) AS result_prefix,
  parentId
FROM events
WHERE type = 'tool.execution_complete'
  AND CAST(data['success'] AS VARCHAR) <> 'true'
ORDER BY timestamp;
```

## 10. Incomplete tool lifecycles

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
),
requests AS (
  SELECT
    json_extract_string(j.value, '$.toolCallId') AS tool_call_id,
    json_extract_string(j.value, '$.name') AS requested_tool_name
  FROM events,
       json_each(events.data['toolRequests']) AS j
  WHERE events.type = 'assistant.message'
    AND events.data['toolRequests'] IS NOT NULL
),
starts AS (
  SELECT
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['toolName'], '$') AS tool_name
  FROM events
  WHERE type = 'tool.execution_start'
),
completes AS (
  SELECT
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    CAST(data['success'] AS VARCHAR) AS success_json
  FROM events
  WHERE type = 'tool.execution_complete'
)
SELECT
  coalesce(requested_tool_name, starts.tool_name, '<unknown>') AS tool_name,
  coalesce(requests.tool_call_id, starts.tool_call_id, completes.tool_call_id) AS tool_call_id,
  requested_tool_name IS NOT NULL AS has_request,
  starts.tool_call_id IS NOT NULL AS has_start,
  completes.tool_call_id IS NOT NULL AS has_complete,
  completes.success_json
FROM requests
FULL OUTER JOIN starts USING (tool_call_id)
FULL OUTER JOIN completes USING (tool_call_id)
WHERE requested_tool_name IS NULL
   OR starts.tool_call_id IS NULL
   OR completes.tool_call_id IS NULL
ORDER BY tool_name, tool_call_id;
```

## 11. Payload structure summary with json_group_structure

Use this to see the combined shape of sparse payloads at a glance.

```sql
WITH events AS (
  SELECT type, to_json(data) AS data_json
  FROM read_ndjson_auto($glob)
)
SELECT
  type,
  json_group_structure(data_json) AS data_structure
FROM events
GROUP BY 1
ORDER BY type;
```

## 12. Deep nested path discovery with json_tree

Use this when key-level summaries are not enough and you need to see nested paths.

```sql
WITH events AS (
  SELECT type, to_json(data) AS data_json
  FROM read_ndjson_auto($glob)
),
nested AS (
  SELECT
    events.type AS event_type,
    jt.fullkey,
    jt.type AS json_type,
    count(*) AS occurrences
  FROM events,
       json_tree(events.data_json) AS jt
  WHERE jt.parent IS NOT NULL
  GROUP BY 1, 2, 3
)
SELECT
  event_type,
  fullkey,
  json_type,
  occurrences
FROM nested
ORDER BY occurrences DESC, event_type, fullkey;
```

## 13. Normalize a common subset with from_json

Use this when many event types share a few fields and you want a stable projection.

```sql
WITH events AS (
  SELECT type, to_json(data) AS data_json
  FROM read_ndjson_auto($glob)
),
norm AS (
  SELECT
    type,
    from_json(
      data_json,
      '{"interactionId":"VARCHAR","turnId":"VARCHAR","toolCallId":"VARCHAR","messageId":"VARCHAR","success":"BOOLEAN"}'
    ) AS extracted
  FROM events
)
SELECT
  type,
  extracted.interactionId AS interaction_id,
  extracted.turnId AS turn_id,
  extracted.toolCallId AS tool_call_id,
  extracted.messageId AS message_id,
  extracted.success AS success
FROM norm
ORDER BY type;
```

## 14. Parent tool lineage

Use this when nested child events carry `parentToolCallId` and you want to see which parent tool invocation they belong to.

```sql
WITH events AS (
  SELECT
    regexp_extract(filename, '.*/session-state/([^/]+)/events\\.jsonl$', 1) AS file_session_id,
    *,
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['parentToolCallId'], '$') AS parent_tool_call_id,
    json_extract_string(data['interactionId'], '$') AS interaction_id
  FROM read_ndjson_auto($glob, ignore_errors := true)
),
tool_calls AS (
  SELECT
    file_session_id,
    tool_call_id,
    max(CASE WHEN type = 'tool.execution_start' THEN json_extract_string(data['toolName'], '$') END) AS parent_tool_name
  FROM events
  WHERE tool_call_id IS NOT NULL
  GROUP BY 1, 2
)
SELECT
  child.file_session_id,
  child.parent_tool_call_id,
  coalesce(tool_calls.parent_tool_name, '<unknown>') AS parent_tool_name,
  count(*) AS child_rows,
  count(DISTINCT child.type) AS child_type_count,
  count(DISTINCT coalesce(child.interaction_id, '<null>')) AS interaction_count,
  string_agg(DISTINCT child.type, ', ' ORDER BY child.type) AS child_types
FROM events child
LEFT JOIN tool_calls
  ON child.file_session_id = tool_calls.file_session_id
 AND child.parent_tool_call_id = tool_calls.tool_call_id
WHERE child.parent_tool_call_id IS NOT NULL
GROUP BY 1, 2, 3
ORDER BY child_rows DESC, child.parent_tool_call_id;
```

## 15. Control-event field summary

Use this to quickly inspect control-plane events in newer Copilot CLI logs.

```sql
WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob, ignore_errors := true)
),
norm AS (
  SELECT
    type,
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['parentToolCallId'], '$') AS parent_tool_call_id,
    json_extract_string(data['agentName'], '$') AS agent_name,
    json_extract_string(data['agentDisplayName'], '$') AS agent_display_name,
    json_extract_string(data['name'], '$') AS skill_name
  FROM events
)
SELECT
  type,
  count(*) AS rows,
  count_if(tool_call_id IS NOT NULL) AS rows_with_tool_call_id,
  count_if(parent_tool_call_id IS NOT NULL) AS rows_with_parent_tool_call_id,
  count_if(agent_name IS NOT NULL) AS rows_with_agent_name,
  count_if(skill_name IS NOT NULL) AS rows_with_skill_name
FROM norm
WHERE type IN ('skill.invoked', 'subagent.started', 'subagent.completed', 'system.notification')
GROUP BY 1
ORDER BY rows DESC, type;
```

## 16. System notification kinds

Use this to inspect runtime notifications that were appended into the session log.

```sql
WITH events AS (
  SELECT
    json_extract_string(data['kind']['type'], '$') AS kind_type,
    json_extract_string(data['kind']['agentId'], '$') AS agent_id,
    json_extract_string(data['kind']['status'], '$') AS agent_status
  FROM read_ndjson_auto($glob, ignore_errors := true)
  WHERE type = 'system.notification'
)
SELECT
  kind_type,
  agent_status,
  count(*) AS rows,
  string_agg(DISTINCT agent_id, ', ' ORDER BY agent_id) AS agent_ids
FROM events
GROUP BY 1, 2
ORDER BY rows DESC, kind_type, agent_status;
```

## 17. Read workspace.yaml with the YAML extension

Use this when session metadata is missing from `events.jsonl`.

```sql
INSTALL yaml FROM community;
LOAD yaml;

SELECT *
FROM read_yaml($workspace_yaml);
```

## 18. Read session.db with the SQLite extension

Use this when the session folder contains SQLite state you want to inspect directly from DuckDB.

```sql
INSTALL sqlite;
LOAD sqlite;

SELECT 'todos' AS table_name, count(*) AS rows
FROM sqlite_scan($session_db, 'todos')
UNION ALL
SELECT 'todo_deps' AS table_name, count(*) AS rows
FROM sqlite_scan($session_db, 'todo_deps');
```

## 19. Count workspace files with glob()

Use this when you want a `Files (N)`-style count from the session workspace.

```sql
SELECT count(*) AS workspace_file_rows
FROM glob($session_files_glob);
```

Example parameter:

- `$session_files_glob = '/Users/apstndb/.copilot/session-state/<session-id>/files/**/*'`

## 20. Multisource session summary

Use this to combine `workspace.yaml`, `events.jsonl`, and `session.db` into one DuckDB result.

```sql
INSTALL yaml FROM community;
LOAD yaml;
INSTALL sqlite;
LOAD sqlite;

WITH ws AS (
  SELECT *
  FROM read_yaml($workspace_yaml)
),
start_event AS (
  SELECT
    json_extract_string(to_json(data), '$.sessionId') AS session_id,
    json_extract_string(to_json(data), '$.startTime') AS start_time,
    json_extract_string(to_json(data), '$.context.cwd') AS cwd
  FROM read_ndjson_auto($events_jsonl, ignore_errors := true)
  WHERE type = 'session.start'
  LIMIT 1
),
bounds AS (
  SELECT
    min(timestamp) AS first_ts,
    max(timestamp) AS last_ts,
    count(*) AS event_count,
    count(DISTINCT type) AS type_count,
    count_if(type = 'session.plan_changed') AS plan_change_rows,
    count_if(type = 'session.shutdown') AS shutdown_rows
  FROM read_ndjson_auto($events_jsonl, ignore_errors := true)
),
sqlite_counts AS (
  SELECT
    (SELECT count(*) FROM sqlite_scan($session_db, 'todos')) AS todo_rows,
    (SELECT count(*) FROM sqlite_scan($session_db, 'todo_deps')) AS todo_dep_rows
)
SELECT
  ws.id,
  ws.summary,
  ws.created_at,
  ws.updated_at,
  start_event.session_id,
  start_event.start_time,
  start_event.cwd,
  bounds.first_ts,
  bounds.last_ts,
  bounds.event_count,
  bounds.type_count,
  bounds.plan_change_rows,
  bounds.shutdown_rows,
  sqlite_counts.todo_rows,
  sqlite_counts.todo_dep_rows
FROM ws
CROSS JOIN start_event
CROSS JOIN bounds
CROSS JOIN sqlite_counts;
```

## 21. Authenticated HTTP GET with httpfs and CREATE SECRET

Use this when you want `read_json(...)` or `read_json_auto(...)` to call an authenticated JSON endpoint.

Pass secrets such as bearer tokens from the outer shell or client and substitute them into `$token`; do not commit real tokens into SQL files.

```sql
INSTALL httpfs;
LOAD httpfs;
SET unsafe_disable_etag_checks = true;

CREATE OR REPLACE SECRET gh_copilot_auth (
  TYPE http,
  EXTRA_HTTP_HEADERS MAP {
    'Authorization': 'Bearer ' || $token,
    'Accept': 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28'
  }
);

SELECT *
FROM read_json_auto('https://api.github.com/copilot_internal/user');
```

If DuckDB reports that the endpoint changed `ETag` between reads, keep the `SET unsafe_disable_etag_checks = true;` line and call out that the endpoint appears volatile.

If the endpoint only needs bearer-token auth, this is a smaller alternative:

```sql
CREATE OR REPLACE SECRET gh_copilot_auth (
  TYPE http,
  BEARER_TOKEN $token
);
```

`httpfs` can issue `HEAD` requests and ranged `GET` requests while reading, so local test servers should support both when you validate this pattern offline.

## 22. Explicit HTTP request/response with the optional http_client extension

Use this when a file-style reader is not enough and you want an explicit response object with status, reason, and raw body.

This is a community extension, so availability can vary by DuckDB version and platform.

```sql
INSTALL http_client FROM community;
LOAD http_client;

WITH response AS (
  SELECT http_get(
    'https://api.github.com/copilot_internal/user',
    headers => MAP {
      'authorization': 'Bearer ' || $token,
      'accept': 'application/vnd.github+json',
      'x-github-api-version': '2022-11-28'
    }
  ) AS res
)
SELECT
  (res->>'status')::INT AS status,
  res->>'reason' AS reason,
  (res->>'body')::JSON AS body
FROM response;
```

If you want a typed projection instead of a raw JSON blob, parse the body with `from_json(...)`:

```sql
WITH response AS (
  SELECT http_get(
    'https://api.github.com/copilot_internal/user',
    headers => MAP {
      'authorization': 'Bearer ' || $token,
      'accept': 'application/vnd.github+json',
      'x-github-api-version': '2022-11-28'
    }
  ) AS res
)
SELECT user_response.*
FROM (
  SELECT from_json(
    (res->>'body')::JSON,
    '{"chat_enabled":"BOOLEAN","sku":"VARCHAR","assigned_models":"JSON"}'
  ) AS user_response
  FROM response
);
```

## 23. Reconstruct turn windows for turns/history analysis

Use this when `turnId` repeats within a session or `interactionId` spans many assistant turns.

This pairs `assistant.turn_start` and `assistant.turn_end` by `(turn_id, occurrence)` and then assigns tool activity by time window.

```sql
WITH events AS (
  SELECT
    type,
    id,
    parentId,
    try_cast(timestamp AS TIMESTAMPTZ) AS ts,
    data
  FROM read_ndjson_auto($events_jsonl, ignore_errors := true)
),
labeled AS (
  SELECT
    *,
    sum(CASE WHEN type IN ('session.start', 'session.resume') THEN 1 ELSE 0 END)
      OVER (ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS segment_index
  FROM events
),
session_end AS (
  SELECT max(ts) AS last_ts FROM labeled
),
starts AS (
  SELECT
    row_number() OVER (ORDER BY ts) AS global_turn_seq,
    segment_index,
    ts AS start_ts,
    parentId AS parent_event_id,
    json_extract_string(data['turnId'], '$') AS turn_id,
    json_extract_string(data['interactionId'], '$') AS interaction_id,
    row_number() OVER (
      PARTITION BY json_extract_string(data['turnId'], '$')
      ORDER BY ts
    ) AS turn_occurrence
  FROM labeled
  WHERE type = 'assistant.turn_start'
),
ends AS (
  SELECT
    ts AS end_ts,
    json_extract_string(data['turnId'], '$') AS turn_id,
    row_number() OVER (
      PARTITION BY json_extract_string(data['turnId'], '$')
      ORDER BY ts
    ) AS turn_occurrence
  FROM labeled
  WHERE type = 'assistant.turn_end'
),
turn_windows AS (
  SELECT
    starts.global_turn_seq,
    starts.segment_index,
    starts.turn_id,
    starts.interaction_id,
    starts.start_ts,
    starts.parent_event_id,
    ends.end_ts,
    coalesce(ends.end_ts, session_end.last_ts) AS effective_end_ts,
    round(date_diff('millisecond', starts.start_ts, coalesce(ends.end_ts, session_end.last_ts)) / 1000.0, 3) AS duration_s
  FROM starts
  LEFT JOIN ends
    ON starts.turn_id = ends.turn_id
   AND starts.turn_occurrence = ends.turn_occurrence
  CROSS JOIN session_end
),
window_tools AS (
  SELECT
    turn_windows.global_turn_seq,
    count_if(labeled.type = 'tool.execution_complete') AS tool_calls,
    string_agg(DISTINCT json_extract_string(labeled.data['model'], '$'), ', ' ORDER BY json_extract_string(labeled.data['model'], '$'))
      FILTER (WHERE labeled.type = 'tool.execution_complete' AND json_extract_string(labeled.data['model'], '$') IS NOT NULL) AS models
  FROM turn_windows
  LEFT JOIN labeled
    ON labeled.ts >= turn_windows.start_ts
   AND labeled.ts <= turn_windows.effective_end_ts
  GROUP BY 1
)
SELECT
  turn_windows.global_turn_seq,
  turn_windows.segment_index,
  turn_windows.turn_id,
  turn_windows.duration_s,
  turn_windows.end_ts IS NULL AS is_open,
  coalesce(window_tools.tool_calls, 0) AS tool_calls,
  coalesce(window_tools.models, '-') AS models
FROM turn_windows
LEFT JOIN window_tools USING (global_turn_seq)
ORDER BY turn_windows.global_turn_seq;
```

## 24. Prototype a property graph with DuckPGQ

Use this when the event log starts to feel more like a graph than a tree and you want to analyze `parentId`, `interactionId`, `toolCallId`, and `parentToolCallId` as separate edge types.

This example builds a property graph for one session. It keeps unresolved links in the relational summary and only creates graph edges when both referenced vertices exist.

```sql
INSTALL duckpgq FROM community;
LOAD duckpgq;

CREATE TEMP TABLE raw_events AS
SELECT
  CAST(id AS VARCHAR) AS event_id,
  CAST(parentId AS VARCHAR) AS parent_event_id,
  regexp_extract(filename, '/session-state/([^/]+)/events\.jsonl$', 1) AS session_id,
  type AS event_type,
  CAST(timestamp AS TIMESTAMP) AS ts,
  json_extract_string(data['interactionId'], '$') AS interaction_id,
  json_extract_string(data['toolCallId'], '$') AS tool_call_id,
  json_extract_string(data['parentToolCallId'], '$') AS parent_tool_call_id
FROM read_ndjson_auto(
  '/Users/apstndb/.copilot/session-state/<session-id>/events.jsonl',
  ignore_errors := true,
  filename := true
);

CREATE TEMP TABLE event_vertices (
  event_id VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  event_type VARCHAR,
  ts TIMESTAMP,
  interaction_id VARCHAR,
  tool_call_id VARCHAR
);
INSERT INTO event_vertices
SELECT event_id, session_id, event_type, ts, interaction_id, tool_call_id
FROM raw_events
WHERE event_id IS NOT NULL;

CREATE TEMP TABLE interaction_vertices (
  interaction_key VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  interaction_id VARCHAR,
  event_count BIGINT
);
INSERT INTO interaction_vertices
SELECT
  session_id || ':' || interaction_id AS interaction_key,
  session_id,
  interaction_id,
  count(*)
FROM raw_events
WHERE interaction_id IS NOT NULL
GROUP BY 1, 2, 3;

CREATE TEMP TABLE tool_call_vertices (
  tool_call_key VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  tool_call_id VARCHAR,
  parent_tool_call_id VARCHAR,
  event_count BIGINT
);
INSERT INTO tool_call_vertices
SELECT
  session_id || ':' || tool_call_id AS tool_call_key,
  session_id,
  tool_call_id,
  max(parent_tool_call_id) FILTER (WHERE parent_tool_call_id IS NOT NULL) AS parent_tool_call_id,
  count(*)
FROM raw_events
WHERE tool_call_id IS NOT NULL
GROUP BY 1, 2, 3;

CREATE TEMP TABLE event_parent_edge (
  edge_id VARCHAR PRIMARY KEY,
  child_event_id VARCHAR,
  parent_event_id VARCHAR
);
INSERT INTO event_parent_edge
SELECT
  child.event_id || '->' || child.parent_event_id,
  child.event_id,
  child.parent_event_id
FROM raw_events child
JOIN event_vertices parent ON parent.event_id = child.parent_event_id
WHERE child.parent_event_id IS NOT NULL;

CREATE TEMP TABLE event_interaction_edge (
  edge_id VARCHAR PRIMARY KEY,
  event_id VARCHAR,
  interaction_key VARCHAR
);
INSERT INTO event_interaction_edge
SELECT
  event_id || '->' || session_id || ':' || interaction_id,
  event_id,
  session_id || ':' || interaction_id
FROM raw_events
WHERE event_id IS NOT NULL AND interaction_id IS NOT NULL;

CREATE TEMP TABLE event_tool_call_edge (
  edge_id VARCHAR PRIMARY KEY,
  event_id VARCHAR,
  tool_call_key VARCHAR
);
INSERT INTO event_tool_call_edge
SELECT
  event_id || '->' || session_id || ':' || tool_call_id,
  event_id,
  session_id || ':' || tool_call_id
FROM raw_events
WHERE event_id IS NOT NULL AND tool_call_id IS NOT NULL;

CREATE TEMP TABLE tool_call_parent_edge (
  edge_id VARCHAR PRIMARY KEY,
  child_tool_call_key VARCHAR,
  parent_tool_call_key VARCHAR
);
INSERT INTO tool_call_parent_edge
SELECT
  child.tool_call_key || '->' || parent.tool_call_key,
  child.tool_call_key,
  parent.tool_call_key
FROM tool_call_vertices child
JOIN tool_call_vertices parent
  ON parent.session_id = child.session_id
 AND parent.tool_call_id = child.parent_tool_call_id
WHERE child.parent_tool_call_id IS NOT NULL;

CREATE PROPERTY GRAPH copilot_graph
VERTEX TABLES (
  event_vertices,
  interaction_vertices,
  tool_call_vertices
)
EDGE TABLES (
  event_parent_edge
    SOURCE KEY (child_event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (parent_event_id) REFERENCES event_vertices (event_id),
  event_interaction_edge
    SOURCE KEY (event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (interaction_key) REFERENCES interaction_vertices (interaction_key),
  event_tool_call_edge
    SOURCE KEY (event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (tool_call_key) REFERENCES tool_call_vertices (tool_call_key),
  tool_call_parent_edge
    SOURCE KEY (child_tool_call_key) REFERENCES tool_call_vertices (tool_call_key)
    DESTINATION KEY (parent_tool_call_key) REFERENCES tool_call_vertices (tool_call_key)
);

SELECT
  interaction_id,
  matched_events,
  tool_calls,
  event_types
FROM (
  SELECT
    interaction_id,
    count(*) AS matched_events,
    count(DISTINCT tool_call_id) FILTER (WHERE tool_call_id IS NOT NULL) AS tool_calls,
    string_agg(DISTINCT event_type, ', ' ORDER BY event_type) AS event_types
  FROM GRAPH_TABLE (copilot_graph
    MATCH (i:interaction_vertices)<-[ei:event_interaction_edge]-(e:event_vertices)
    COLUMNS (
      i.interaction_id AS interaction_id,
      e.event_type AS event_type,
      e.tool_call_id AS tool_call_id
    )
  )
  GROUP BY 1
)
ORDER BY matched_events DESC, interaction_id
LIMIT 10;

SELECT
  parent_tool_call_id,
  child_tool_calls,
  child_event_types
FROM (
  SELECT
    parent_tool_call_id,
    count(DISTINCT child_tool_call_id) AS child_tool_calls,
    string_agg(DISTINCT child_event_type, ', ' ORDER BY child_event_type) AS child_event_types
  FROM GRAPH_TABLE (copilot_graph
    MATCH (child_event:event_vertices)-[etc:event_tool_call_edge]->(child_tc:tool_call_vertices)-[pt:tool_call_parent_edge]->(parent:tool_call_vertices)
    COLUMNS (
      parent.tool_call_id AS parent_tool_call_id,
      child_tc.tool_call_id AS child_tool_call_id,
      child_event.event_type AS child_event_type
    )
  )
  GROUP BY 1
)
ORDER BY child_tool_calls DESC, parent_tool_call_id
LIMIT 10;
```

Caveats:

- Keep a relational summary for unresolved links. In one live Copilot session, `parentToolCallId` resolved cleanly but many `parentId` rows still pointed to parents missing from the log.
- `MATCH` patterns must bind a variable on each edge, e.g. `[ei:event_interaction_edge]`, not just `[:event_interaction_edge]`.
- Casting `id` and `parentId` to `VARCHAR` up front avoids `UUID` versus `VARCHAR` comparison issues when mixing JSONL-derived IDs with derived graph tables.
