-- 24. Prototype a property graph with DuckPGQ
--
--
-- Use this when the event log starts to feel more like a graph than a tree and you want to analyze `parentId`, `interactionId`, `toolCallId`, and `parentToolCallId` as separate edge types.
--
-- This example builds a property graph for one session. It keeps unresolved links in the relational summary and only creates graph edges when both referenced vertices exist.

INSTALL duckpgq FROM community;
LOAD duckpgq;

CREATE TEMP TABLE raw_events AS
SELECT
  CAST(id AS VARCHAR) AS event_id,
  CAST(parentId AS VARCHAR) AS parent_event_id,
  regexp_extract(filename, '/session-state/([^/]+)/events\.jsonl$', 1) AS session_id,
  type AS event_type,
  CAST(timestamp AS TIMESTAMP) AS ts,
  json_extract_string(data['interactionId'], '$') AS interaction_id,
  json_extract_string(data['toolCallId'], '$') AS tool_call_id,
  json_extract_string(data['parentToolCallId'], '$') AS parent_tool_call_id
FROM read_ndjson_auto(
  '/Users/apstndb/.copilot/session-state/<session-id>/events.jsonl',
  ignore_errors := true,
  filename := true
);

CREATE TEMP TABLE event_vertices (
  event_id VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  event_type VARCHAR,
  ts TIMESTAMP,
  interaction_id VARCHAR,
  tool_call_id VARCHAR
);
INSERT INTO event_vertices
SELECT event_id, session_id, event_type, ts, interaction_id, tool_call_id
FROM raw_events
WHERE event_id IS NOT NULL;

CREATE TEMP TABLE interaction_vertices (
  interaction_key VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  interaction_id VARCHAR,
  event_count BIGINT
);
INSERT INTO interaction_vertices
SELECT
  session_id || ':' || interaction_id AS interaction_key,
  session_id,
  interaction_id,
  count(*)
FROM raw_events
WHERE interaction_id IS NOT NULL
GROUP BY 1, 2, 3;

CREATE TEMP TABLE tool_call_vertices (
  tool_call_key VARCHAR PRIMARY KEY,
  session_id VARCHAR,
  tool_call_id VARCHAR,
  parent_tool_call_id VARCHAR,
  event_count BIGINT
);
INSERT INTO tool_call_vertices
SELECT
  session_id || ':' || tool_call_id AS tool_call_key,
  session_id,
  tool_call_id,
  max(parent_tool_call_id) FILTER (WHERE parent_tool_call_id IS NOT NULL) AS parent_tool_call_id,
  count(*)
FROM raw_events
WHERE tool_call_id IS NOT NULL
GROUP BY 1, 2, 3;

CREATE TEMP TABLE event_parent_edge (
  edge_id VARCHAR PRIMARY KEY,
  child_event_id VARCHAR,
  parent_event_id VARCHAR
);
INSERT INTO event_parent_edge
SELECT
  child.event_id || '->' || child.parent_event_id,
  child.event_id,
  child.parent_event_id
FROM raw_events child
JOIN event_vertices parent ON parent.event_id = child.parent_event_id
WHERE child.parent_event_id IS NOT NULL;

CREATE TEMP TABLE event_interaction_edge (
  edge_id VARCHAR PRIMARY KEY,
  event_id VARCHAR,
  interaction_key VARCHAR
);
INSERT INTO event_interaction_edge
SELECT
  event_id || '->' || session_id || ':' || interaction_id,
  event_id,
  session_id || ':' || interaction_id
FROM raw_events
WHERE event_id IS NOT NULL AND interaction_id IS NOT NULL;

CREATE TEMP TABLE event_tool_call_edge (
  edge_id VARCHAR PRIMARY KEY,
  event_id VARCHAR,
  tool_call_key VARCHAR
);
INSERT INTO event_tool_call_edge
SELECT
  event_id || '->' || session_id || ':' || tool_call_id,
  event_id,
  session_id || ':' || tool_call_id
FROM raw_events
WHERE event_id IS NOT NULL AND tool_call_id IS NOT NULL;

CREATE TEMP TABLE tool_call_parent_edge (
  edge_id VARCHAR PRIMARY KEY,
  child_tool_call_key VARCHAR,
  parent_tool_call_key VARCHAR
);
INSERT INTO tool_call_parent_edge
SELECT
  child.tool_call_key || '->' || parent.tool_call_key,
  child.tool_call_key,
  parent.tool_call_key
FROM tool_call_vertices child
JOIN tool_call_vertices parent
  ON parent.session_id = child.session_id
 AND parent.tool_call_id = child.parent_tool_call_id
WHERE child.parent_tool_call_id IS NOT NULL;

CREATE PROPERTY GRAPH copilot_graph
VERTEX TABLES (
  event_vertices,
  interaction_vertices,
  tool_call_vertices
)
EDGE TABLES (
  event_parent_edge
    SOURCE KEY (child_event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (parent_event_id) REFERENCES event_vertices (event_id),
  event_interaction_edge
    SOURCE KEY (event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (interaction_key) REFERENCES interaction_vertices (interaction_key),
  event_tool_call_edge
    SOURCE KEY (event_id) REFERENCES event_vertices (event_id)
    DESTINATION KEY (tool_call_key) REFERENCES tool_call_vertices (tool_call_key),
  tool_call_parent_edge
    SOURCE KEY (child_tool_call_key) REFERENCES tool_call_vertices (tool_call_key)
    DESTINATION KEY (parent_tool_call_key) REFERENCES tool_call_vertices (tool_call_key)
);

SELECT
  interaction_id,
  matched_events,
  tool_calls,
  event_types
FROM (
  SELECT
    interaction_id,
    count(*) AS matched_events,
    count(DISTINCT tool_call_id) FILTER (WHERE tool_call_id IS NOT NULL) AS tool_calls,
    string_agg(DISTINCT event_type, ', ' ORDER BY event_type) AS event_types
  FROM GRAPH_TABLE (copilot_graph
    MATCH (i:interaction_vertices)<-[ei:event_interaction_edge]-(e:event_vertices)
    COLUMNS (
      i.interaction_id AS interaction_id,
      e.event_type AS event_type,
      e.tool_call_id AS tool_call_id
    )
  )
  GROUP BY 1
)
ORDER BY matched_events DESC, interaction_id
LIMIT 10;

SELECT
  parent_tool_call_id,
  child_tool_calls,
  child_event_types
FROM (
  SELECT
    parent_tool_call_id,
    count(DISTINCT child_tool_call_id) AS child_tool_calls,
    string_agg(DISTINCT child_event_type, ', ' ORDER BY child_event_type) AS child_event_types
  FROM GRAPH_TABLE (copilot_graph
    MATCH (child_event:event_vertices)-[etc:event_tool_call_edge]->(child_tc:tool_call_vertices)-[pt:tool_call_parent_edge]->(parent:tool_call_vertices)
    COLUMNS (
      parent.tool_call_id AS parent_tool_call_id,
      child_tc.tool_call_id AS child_tool_call_id,
      child_event.event_type AS child_event_type
    )
  )
  GROUP BY 1
)
ORDER BY child_tool_calls DESC, parent_tool_call_id
LIMIT 10;
--
-- Caveats:
--
-- - Keep a relational summary for unresolved links. In one live Copilot session, `parentToolCallId` resolved cleanly but many `parentId` rows still pointed to parents missing from the log.
-- - `MATCH` patterns must bind a variable on each edge, e.g. `[ei:event_interaction_edge]`, not just `[:event_interaction_edge]`.
-- - Casting `id` and `parentId` to `VARCHAR` up front avoids `UUID` versus `VARCHAR` comparison issues when mixing JSONL-derived IDs with derived graph tables.
