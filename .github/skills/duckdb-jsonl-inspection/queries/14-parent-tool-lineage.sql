-- 14. Parent tool lineage
--
--
-- Use this when nested child events carry `parentToolCallId` and you want to see which parent tool invocation they belong to.

WITH events AS (
  SELECT
    regexp_extract(filename, '.*/session-state/([^/]+)/events\.jsonl$', 1) AS file_session_id,
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
