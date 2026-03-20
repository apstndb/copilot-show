-- 18. Read session.db with the SQLite extension
--
--
-- Use this when the session folder contains SQLite state you want to inspect directly from DuckDB.

INSTALL sqlite;
LOAD sqlite;

SELECT 'todos' AS table_name, count(*) AS rows
FROM sqlite_scan($session_db, 'todos')
UNION ALL
SELECT 'todo_deps' AS table_name, count(*) AS rows
FROM sqlite_scan($session_db, 'todo_deps');
