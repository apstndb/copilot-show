-- 15. Control-event field summary
--
--
-- Use this to quickly inspect control-plane events in newer Copilot CLI logs.

WITH events AS (
  SELECT *
  FROM read_ndjson_auto($glob, ignore_errors := true)
),
norm AS (
  SELECT
    type,
    json_extract_string(data['toolCallId'], '$') AS tool_call_id,
    json_extract_string(data['parentToolCallId'], '$') AS parent_tool_call_id,
    json_extract_string(data['agentName'], '$') AS agent_name,
    json_extract_string(data['agentDisplayName'], '$') AS agent_display_name,
    json_extract_string(data['name'], '$') AS skill_name
  FROM events
)
SELECT
  type,
  count(*) AS rows,
  count_if(tool_call_id IS NOT NULL) AS rows_with_tool_call_id,
  count_if(parent_tool_call_id IS NOT NULL) AS rows_with_parent_tool_call_id,
  count_if(agent_name IS NOT NULL) AS rows_with_agent_name,
  count_if(skill_name IS NOT NULL) AS rows_with_skill_name
FROM norm
WHERE type IN ('skill.invoked', 'subagent.started', 'subagent.completed', 'system.notification')
GROUP BY 1
ORDER BY rows DESC, type;
