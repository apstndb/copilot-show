-- 25. Reconstruct billable user-message windows and weighted premium requests
--
--
-- Use this when a closed segment's `session.shutdown.data.totalPremiumRequests` does not match raw `user.message` count and you want a reproducible DuckDB explanation.
--
-- This recipe:
--
-- - builds user windows from one `user.message` to the next within each closed segment
-- - counts assistant output per window
-- - uses `tool.execution_complete.data.model` when present
-- - assigns model-less assistant-output windows only when the residual shutdown `modelMetrics.*.requests.count` makes a unique assignment possible
--
-- Treat `interactionId` as metadata here, not as the primary billing join key. Active segments are intentionally excluded because they do not yet have authoritative shutdown totals.

CREATE OR REPLACE TEMP VIEW raw_events AS
SELECT
  regexp_extract(filename, '.*/session-state/([^/]+)/events\.jsonl$', 1) AS session_id,
  type,
  id,
  parentId,
  try_cast(timestamp AS TIMESTAMPTZ) AS ts,
  data
FROM read_ndjson_auto(
  $events_jsonl,
  ignore_errors := true,
  union_by_name := true,
  filename := true
);

CREATE OR REPLACE TEMP VIEW labeled AS
SELECT
  *,
  sum(CASE WHEN type IN ('session.start', 'session.resume') THEN 1 ELSE 0 END)
    OVER (
      PARTITION BY session_id
      ORDER BY ts, id
      ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
    ) AS segment_no
FROM raw_events;

CREATE OR REPLACE TEMP VIEW closed_segments AS
SELECT
  session_id,
  segment_no,
  max(ts) FILTER (WHERE type = 'session.shutdown') AS shutdown_ts,
  max(CAST(json_extract(data['totalPremiumRequests'], '$') AS DOUBLE))
    FILTER (WHERE type = 'session.shutdown') AS total_premium_requests
FROM labeled
GROUP BY 1, 2
HAVING count(*) FILTER (WHERE type = 'session.shutdown') > 0;

CREATE OR REPLACE TEMP VIEW shutdown_models AS
SELECT
  l.session_id,
  l.segment_no,
  j.key AS model,
  CAST(json_extract(j.value, '$.requests.count') AS DOUBLE) AS request_count,
  CAST(json_extract(j.value, '$.requests.cost') AS DOUBLE) AS request_cost
FROM labeled l
JOIN closed_segments c USING (session_id, segment_no),
     json_each(to_json(l.data['modelMetrics'])) AS j
WHERE l.type = 'session.shutdown';

CREATE OR REPLACE TEMP VIEW user_messages AS
SELECT
  l.session_id,
  l.segment_no,
  l.id AS user_message_id,
  l.ts AS user_ts,
  row_number() OVER (
    PARTITION BY l.session_id, l.segment_no
    ORDER BY l.ts, l.id
  ) AS user_seq,
  json_extract_string(l.data['interactionId'], '$') AS user_interaction_id
FROM labeled l
JOIN closed_segments c USING (session_id, segment_no)
WHERE l.type = 'user.message';

CREATE OR REPLACE TEMP VIEW user_windows AS
SELECT
  u.*,
  coalesce(
    lead(u.user_ts) OVER (
      PARTITION BY u.session_id, u.segment_no
      ORDER BY u.user_ts, u.user_message_id
    ),
    c.shutdown_ts
  ) AS window_end_ts
FROM user_messages u
JOIN closed_segments c USING (session_id, segment_no);

CREATE OR REPLACE TEMP VIEW window_stats AS
SELECT
  uw.session_id,
  uw.segment_no,
  uw.user_seq,
  uw.user_message_id,
  uw.user_ts,
  uw.window_end_ts,
  uw.user_interaction_id,
  count(*) FILTER (WHERE e.type = 'assistant.message') AS assistant_messages,
  count(*) FILTER (WHERE e.type = 'tool.execution_complete') AS tool_completions,
  count(*) FILTER (WHERE e.type = 'abort') AS aborts,
  string_agg(
    DISTINCT json_extract_string(e.data['model'], '$'),
    ', '
    ORDER BY json_extract_string(e.data['model'], '$')
  ) FILTER (
    WHERE e.type = 'tool.execution_complete'
      AND json_extract_string(e.data['model'], '$') IS NOT NULL
  ) AS observed_models,
  count(DISTINCT json_extract_string(e.data['model'], '$')) FILTER (
    WHERE e.type = 'tool.execution_complete'
      AND json_extract_string(e.data['model'], '$') IS NOT NULL
  ) AS distinct_models
FROM user_windows uw
LEFT JOIN labeled e
  ON e.session_id = uw.session_id
 AND e.segment_no = uw.segment_no
 AND e.ts >= uw.user_ts
 AND e.ts < uw.window_end_ts
 AND e.id <> uw.user_message_id
GROUP BY 1, 2, 3, 4, 5, 6, 7;

CREATE OR REPLACE TEMP VIEW observed_windows AS
SELECT
  *,
  CASE WHEN distinct_models = 1 THEN observed_models END AS observed_model
FROM window_stats;

CREATE OR REPLACE TEMP VIEW observed_model_request_counts AS
SELECT
  session_id,
  segment_no,
  observed_model AS model,
  sum(assistant_messages) AS observed_assistant_messages
FROM observed_windows
WHERE observed_model IS NOT NULL
GROUP BY 1, 2, 3;

CREATE OR REPLACE TEMP VIEW residual_models AS
SELECT
  sm.session_id,
  sm.segment_no,
  sm.model,
  sm.request_count,
  sm.request_cost,
  coalesce(omrc.observed_assistant_messages, 0) AS observed_assistant_messages,
  sm.request_count - coalesce(omrc.observed_assistant_messages, 0) AS residual_assistant_messages
FROM shutdown_models sm
LEFT JOIN observed_model_request_counts omrc
  USING (session_id, segment_no, model);

CREATE OR REPLACE TEMP VIEW residual_summary AS
SELECT
  session_id,
  segment_no,
  count(*) FILTER (WHERE residual_assistant_messages > 0.000001) AS positive_residual_models,
  max(model) FILTER (WHERE residual_assistant_messages > 0.000001) AS sole_residual_model,
  sum(residual_assistant_messages) FILTER (WHERE residual_assistant_messages > 0.000001) AS total_residual_assistant_messages
FROM residual_models
GROUP BY 1, 2;

CREATE OR REPLACE TEMP VIEW unattributed_windows AS
SELECT
  session_id,
  segment_no,
  sum(assistant_messages) AS unattributed_assistant_messages
FROM observed_windows
WHERE observed_model IS NULL
GROUP BY 1, 2;

CREATE OR REPLACE TEMP VIEW assigned_windows AS
SELECT
  ow.*,
  CASE
    WHEN ow.assistant_messages = 0 THEN NULL
    WHEN ow.observed_model IS NOT NULL THEN ow.observed_model
    WHEN rs.positive_residual_models = 1
      AND coalesce(uw.unattributed_assistant_messages, 0) = rs.total_residual_assistant_messages
      THEN rs.sole_residual_model
    ELSE NULL
  END AS assigned_model,
  CASE
    WHEN ow.assistant_messages = 0 THEN 'no assistant output'
    WHEN ow.observed_model IS NOT NULL THEN 'observed via tool.execution_complete.model'
    WHEN rs.positive_residual_models = 1
      AND coalesce(uw.unattributed_assistant_messages, 0) = rs.total_residual_assistant_messages
      THEN 'assigned by segment residual assistant-message count'
    ELSE 'ambiguous: no model in window payloads'
  END AS assignment_method
FROM observed_windows ow
LEFT JOIN residual_summary rs USING (session_id, segment_no)
LEFT JOIN unattributed_windows uw USING (session_id, segment_no);

CREATE OR REPLACE TEMP VIEW model_weights AS
SELECT
  sm.session_id,
  sm.segment_no,
  sm.model,
  sm.request_count,
  sm.request_cost,
  count(*) FILTER (
    WHERE aw.assigned_model = sm.model
      AND aw.assistant_messages > 0
  ) AS billed_user_messages,
  CASE
    WHEN count(*) FILTER (
      WHERE aw.assigned_model = sm.model
        AND aw.assistant_messages > 0
    ) > 0
      THEN sm.request_cost / count(*) FILTER (
        WHERE aw.assigned_model = sm.model
          AND aw.assistant_messages > 0
      )
  END AS per_user_weight
FROM shutdown_models sm
LEFT JOIN assigned_windows aw
  ON aw.session_id = sm.session_id
 AND aw.segment_no = sm.segment_no
GROUP BY 1, 2, 3, 4, 5;

CREATE OR REPLACE TEMP VIEW model_weight_totals AS
SELECT
  session_id,
  segment_no,
  sum(request_cost) AS shutdown_request_cost,
  sum(per_user_weight * billed_user_messages) AS reconstructed_premium_requests
FROM model_weights
GROUP BY 1, 2;

CREATE OR REPLACE TEMP VIEW window_totals AS
SELECT
  session_id,
  segment_no,
  count(*) AS user_messages,
  sum(assistant_messages) AS assistant_messages,
  count(*) FILTER (WHERE assistant_messages = 0) AS no_output_user_messages,
  count(*) FILTER (
    WHERE assistant_messages > 0
      AND assigned_model IS NULL
  ) AS ambiguous_user_messages
FROM assigned_windows
GROUP BY 1, 2;

CREATE OR REPLACE TEMP VIEW segment_summary AS
SELECT
  cs.session_id,
  cs.segment_no,
  coalesce(wt.user_messages, 0) AS user_messages,
  coalesce(wt.assistant_messages, 0) AS assistant_messages,
  cs.total_premium_requests,
  coalesce(mwt.shutdown_request_cost, 0) AS shutdown_request_cost,
  coalesce(mwt.reconstructed_premium_requests, 0) AS reconstructed_premium_requests,
  coalesce(wt.no_output_user_messages, 0) AS no_output_user_messages,
  coalesce(wt.ambiguous_user_messages, 0) AS ambiguous_user_messages
FROM closed_segments cs
LEFT JOIN model_weight_totals mwt USING (session_id, segment_no)
LEFT JOIN window_totals wt USING (session_id, segment_no);

CREATE OR REPLACE TEMP VIEW window_trace AS
SELECT
  aw.session_id,
  aw.segment_no,
  aw.user_seq,
  aw.user_ts,
  aw.user_interaction_id,
  aw.assistant_messages,
  aw.tool_completions,
  aw.aborts,
  aw.observed_models,
  aw.assigned_model,
  aw.assignment_method,
  round(mw.per_user_weight, 2) AS billed_weight
FROM assigned_windows aw
LEFT JOIN model_weights mw
  ON mw.session_id = aw.session_id
 AND mw.segment_no = aw.segment_no
 AND mw.model = aw.assigned_model;
--
-- Start with the segment-level check:

-- ---

SELECT
  session_id,
  segment_no,
  user_messages,
  assistant_messages,
  total_premium_requests,
  shutdown_request_cost,
  reconstructed_premium_requests,
  no_output_user_messages,
  ambiguous_user_messages
FROM segment_summary
ORDER BY session_id, segment_no;
--
-- Then inspect the per-window trace:

-- ---

SELECT
  session_id,
  segment_no,
  user_seq,
  user_interaction_id,
  assistant_messages,
  tool_completions,
  aborts,
  observed_models,
  assigned_model,
  assignment_method,
  billed_weight
FROM window_trace
ORDER BY session_id, segment_no, user_seq;
--
-- Useful follow-up filters:

-- ---

SELECT *
FROM window_trace
WHERE assistant_messages = 0
   OR assigned_model IS NULL
ORDER BY session_id, segment_no, user_seq;
--
-- Interpretation notes:
--
-- - `no assistant output` windows are good blacklist candidates when you are trying to explain why raw `user.message` count overstates premium usage.
-- - `reconstructed_premium_requests` should match shutdown totals only for closed segments where residual model assignment is unambiguous.
-- - If many assistant-output windows stay ambiguous, say so explicitly instead of forcing a model assignment from `interactionId` or `parentId`.
-- - Some resumed or immediately-closed segments can legitimately appear as all-zero rows because they have a shutdown but no `user.message` windows. Filter `WHERE user_messages > 0` if you want only billing-relevant segments.
