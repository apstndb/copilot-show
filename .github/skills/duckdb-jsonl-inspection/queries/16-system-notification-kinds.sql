-- 16. System notification kinds
--
--
-- Use this to inspect runtime notifications that were appended into the session log.

WITH events AS (
  SELECT
    json_extract_string(data['kind']['type'], '$') AS kind_type,
    json_extract_string(data['kind']['agentId'], '$') AS agent_id,
    json_extract_string(data['kind']['status'], '$') AS agent_status
  FROM read_ndjson_auto($glob, ignore_errors := true)
  WHERE type = 'system.notification'
)
SELECT
  kind_type,
  agent_status,
  count(*) AS rows,
  string_agg(DISTINCT agent_id, ', ' ORDER BY agent_id) AS agent_ids
FROM events
GROUP BY 1, 2
ORDER BY rows DESC, kind_type, agent_status;
