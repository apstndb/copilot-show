-- 19. Count workspace files with glob()
--
--
-- Use this when you want a `Files (N)`-style count from the session workspace.

SELECT count(*) AS workspace_file_rows
FROM glob($session_files_glob);
--
-- Example parameter:
--
-- - `$session_files_glob = '/Users/apstndb/.copilot/session-state/<session-id>/files/**/*'`
