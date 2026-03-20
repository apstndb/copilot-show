-- 21. Authenticated HTTP GET with httpfs and CREATE SECRET
--
--
-- Use this when you want `read_json(...)` or `read_json_auto(...)` to call an authenticated JSON endpoint.
--
-- Pass secrets such as bearer tokens from the outer shell or client and substitute them into `$token`; do not commit real tokens into SQL files.

INSTALL httpfs;
LOAD httpfs;
SET unsafe_disable_etag_checks = true;

CREATE OR REPLACE SECRET gh_copilot_auth (
  TYPE http,
  EXTRA_HTTP_HEADERS MAP {
    'Authorization': 'Bearer ' || $token,
    'Accept': 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28'
  }
);

SELECT *
FROM read_json_auto('https://api.github.com/copilot_internal/user');
--
-- If DuckDB reports that the endpoint changed `ETag` between reads, keep the `SET unsafe_disable_etag_checks = true;` line and call out that the endpoint appears volatile.
--
-- If the endpoint only needs bearer-token auth, this is a smaller alternative:

-- ---

CREATE OR REPLACE SECRET gh_copilot_auth (
  TYPE http,
  BEARER_TOKEN $token
);
--
-- `httpfs` can issue `HEAD` requests and ranged `GET` requests while reading, so local test servers should support both when you validate this pattern offline.
