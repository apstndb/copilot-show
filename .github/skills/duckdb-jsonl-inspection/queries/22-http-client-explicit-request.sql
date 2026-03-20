-- 22. Explicit HTTP request/response with the optional http_client extension
--
--
-- Use this when a file-style reader is not enough and you want an explicit response object with status, reason, and raw body.
--
-- This is a community extension, so availability can vary by DuckDB version and platform.

INSTALL http_client FROM community;
LOAD http_client;

WITH response AS (
  SELECT http_get(
    'https://api.github.com/copilot_internal/user',
    headers => MAP {
      'authorization': 'Bearer ' || $token,
      'accept': 'application/vnd.github+json',
      'x-github-api-version': '2022-11-28'
    }
  ) AS res
)
SELECT
  (res->>'status')::INT AS status,
  res->>'reason' AS reason,
  (res->>'body')::JSON AS body
FROM response;
--
-- If you want a typed projection instead of a raw JSON blob, parse the body with `from_json(...)`:

-- ---

WITH response AS (
  SELECT http_get(
    'https://api.github.com/copilot_internal/user',
    headers => MAP {
      'authorization': 'Bearer ' || $token,
      'accept': 'application/vnd.github+json',
      'x-github-api-version': '2022-11-28'
    }
  ) AS res
)
SELECT user_response.*
FROM (
  SELECT from_json(
    (res->>'body')::JSON,
    '{"chat_enabled":"BOOLEAN","sku":"VARCHAR","assigned_models":"JSON"}'
  ) AS user_response
  FROM response
);
