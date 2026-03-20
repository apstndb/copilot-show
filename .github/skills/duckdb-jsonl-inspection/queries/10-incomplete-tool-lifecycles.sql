-- 10. Incomplete tool lifecycles
--

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
