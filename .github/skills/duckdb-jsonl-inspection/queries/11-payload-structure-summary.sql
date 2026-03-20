-- 11. Payload structure summary with json_group_structure
--
--
-- Use this to see the combined shape of sparse payloads at a glance.

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
