-- 6. Parent-child event matrix
--

WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob)
)
SELECT
  child.type AS child_type,
  coalesce(parent.type, '[missing]') AS parent_type,
  count(*) AS rows
FROM events child
LEFT JOIN events parent
  ON child.parentId = parent.id
WHERE child.parentId IS NOT NULL
GROUP BY 1, 2
ORDER BY rows DESC, child_type, parent_type;
