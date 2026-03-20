-- 17. Read workspace.yaml with the YAML extension
--
--
-- Use this when session metadata is missing from `events.jsonl`.

INSTALL yaml FROM community;
LOAD yaml;

SELECT *
FROM read_yaml($workspace_yaml);
