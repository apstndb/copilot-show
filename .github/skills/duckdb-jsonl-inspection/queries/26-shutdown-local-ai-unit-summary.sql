-- 26. Summarize shutdown Local AI Units
--
-- Compare each session's latest optional top-level session.shutdown
-- totalNanoAiu value with the corresponding sum of modelMetrics.*.totalNanoAiu.
-- The latest snapshot avoids double-counting resumed sessions because the SDK
-- documents these fields as session-wide accumulated values. This query reads
-- metering metadata only; it does not select user or assistant message content.
--
-- Output divides nano-AIU by 1,000,000,000 as a readable Local AI Unit. This is
-- not asserted to equal a billed GitHub AI Credit. Missing and malformed values
-- remain NULL so they are not mistaken for explicit zero usage.
--
-- Example:
--   EVENTS_JSONL="$HOME/.copilot/session-state/*/events.jsonl" \
--     duckdb :memory: -markdown -c ".read path/to/26-shutdown-local-ai-unit-summary.sql"

CREATE OR REPLACE TEMP VIEW shutdown_aiu_all AS
SELECT
  regexp_extract(filename, 'session-state[/\\]([^/\\]+)[/\\]events\.jsonl$', 1) AS session_id,
  id AS shutdown_id,
  try_cast(timestamp AS TIMESTAMPTZ) AS shutdown_ts,
  try_cast(json_extract(data['totalNanoAiu'], '$') AS DOUBLE) AS top_level_nano_aiu,
  data['modelMetrics'] AS model_metrics
FROM read_ndjson_auto(
  getenv('EVENTS_JSONL'),
  ignore_errors := true,
  union_by_name := true,
  filename := true
)
WHERE type = 'session.shutdown';

CREATE OR REPLACE TEMP VIEW shutdown_aiu AS
SELECT *
FROM shutdown_aiu_all
QUALIFY row_number() OVER (
  PARTITION BY session_id
  ORDER BY shutdown_ts DESC NULLS LAST, shutdown_id DESC
) = 1;

CREATE OR REPLACE TEMP VIEW shutdown_aiu_model_sums AS
SELECT
  shutdown_aiu.session_id,
  shutdown_aiu.shutdown_id,
  shutdown_aiu.shutdown_ts,
  shutdown_aiu.top_level_nano_aiu,
  count(*) FILTER (
    WHERE try_cast(json_extract(model.value, '$.totalNanoAiu') AS DOUBLE) IS NOT NULL
  ) AS models_with_nano_aiu,
  sum(
    try_cast(json_extract(model.value, '$.totalNanoAiu') AS DOUBLE)
  ) AS model_nano_aiu
FROM shutdown_aiu
LEFT JOIN LATERAL json_each(to_json(shutdown_aiu.model_metrics)) AS model ON true
GROUP BY 1, 2, 3, 4;

SELECT
  date_trunc('month', shutdown_ts AT TIME ZONE 'UTC') AS month_utc,
  count(*) AS session_rows,
  count(*) FILTER (WHERE top_level_nano_aiu IS NOT NULL) AS sessions_with_top_level_aiu,
  sum(models_with_nano_aiu) AS model_values,
  sum(top_level_nano_aiu) / 1000000000.0 AS top_level_local_ai_units,
  sum(model_nano_aiu) / 1000000000.0 AS model_local_ai_units,
  max(abs(top_level_nano_aiu - model_nano_aiu)) / 1000000000.0 AS max_delta_local_ai_units
FROM shutdown_aiu_model_sums
GROUP BY 1
ORDER BY 1;
