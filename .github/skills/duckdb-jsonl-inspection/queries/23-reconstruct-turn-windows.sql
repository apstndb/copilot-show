-- 23. Reconstruct turn windows for turns/history analysis
--
--
-- Use this when `turnId` repeats within a session or `interactionId` spans many assistant turns.
--
-- This pairs `assistant.turn_start` and `assistant.turn_end` by `(turn_id, occurrence)` and then assigns tool activity by time window.

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
