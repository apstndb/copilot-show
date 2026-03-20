-- 8. Missing-parent rates by event type
--

WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT
  child.type AS child_type,
  count(*) AS rows_with_parent_id,
  count_if(parent.id IS NULL) AS missing_parent_rows,
  round(100.0 * count_if(parent.id IS NULL) / count(*), 1) AS missing_parent_pct
FROM events child
LEFT JOIN events parent
  ON child.parentId = parent.id
WHERE child.parentId IS NOT NULL
GROUP BY 1
ORDER BY missing_parent_pct DESC, rows_with_parent_id DESC, child_type;
