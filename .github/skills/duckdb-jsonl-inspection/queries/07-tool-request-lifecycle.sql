-- 7. Tool request to execution lifecycle
--

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
