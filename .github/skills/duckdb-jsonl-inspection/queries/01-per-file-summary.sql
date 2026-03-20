-- 1. Per-file summary
--

SELECT
  regexp_extract(filename, '.*/session-state/([^/]+)/events\.jsonl$', 1) AS file_session_id,
  count(*) AS events,
  count(DISTINCT type) AS types,
  min(timestamp) AS first_ts,
  max(timestamp) AS last_ts
FROM read_ndjson_auto($glob)
GROUP BY 1
ORDER BY events DESC, file_session_id;
