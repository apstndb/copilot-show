-- 12. Deep nested path discovery with json_tree
--
--
-- Use this when key-level summaries are not enough and you need to see nested paths.

WITH events AS (
  SELECT type, to_json(data) AS data_json
  FROM read_ndjson_auto($glob)
),
nested AS (
  SELECT
    events.type AS event_type,
    jt.fullkey,
    jt.type AS json_type,
    count(*) AS occurrences
  FROM events,
       json_tree(events.data_json) AS jt
  WHERE jt.parent IS NOT NULL
  GROUP BY 1, 2, 3
)
SELECT
  event_type,
  fullkey,
  json_type,
  occurrences
FROM nested
ORDER BY occurrences DESC, event_type, fullkey;
