-- 20. Multisource session summary
--
--
-- Use this to combine `workspace.yaml`, `events.jsonl`, and `session.db` into one DuckDB result.

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
