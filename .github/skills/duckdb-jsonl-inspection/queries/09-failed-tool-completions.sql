-- 9. Failed tool completions
--

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
