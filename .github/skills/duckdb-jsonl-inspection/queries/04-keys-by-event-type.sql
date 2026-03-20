-- 4. Keys by event type
--

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
