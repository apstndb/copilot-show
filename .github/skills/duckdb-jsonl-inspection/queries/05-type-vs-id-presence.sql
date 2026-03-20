-- 5. Type versus ID presence
--

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
