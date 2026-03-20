-- 2. Event type counts
--

SELECT type, count(*) AS rows
FROM read_ndjson_auto($glob)
GROUP BY 1
ORDER BY rows DESC, type;
