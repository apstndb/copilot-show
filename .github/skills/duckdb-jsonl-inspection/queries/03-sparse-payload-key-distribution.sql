-- 3. Sparse payload key distribution
--
--
-- Use this when a payload column was inferred as `map(varchar, json)`.

WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT key, count(*) AS rows
FROM (
  SELECT unnest(json_keys(to_json(data))) AS key
  FROM events
)
GROUP BY 1
ORDER BY rows DESC, key;
--
-- `to_json(data)` is explicit and docs-backed when `data` was inferred as `MAP` rather than `JSON`.
