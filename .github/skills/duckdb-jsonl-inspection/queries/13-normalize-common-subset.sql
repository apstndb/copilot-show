-- 13. Normalize a common subset with from_json
--
--
-- Use this when many event types share a few fields and you want a stable projection.

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
