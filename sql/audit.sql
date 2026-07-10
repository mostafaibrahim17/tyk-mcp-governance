-- The audit trail Tyk Pump writes to Postgres. Every agent call is one row in
-- tyk_analytics, attributed by `alias` (the per-agent key alias we set).
--
--   psql "host=localhost port=5433 user=tyk password=tyk dbname=tyk_analytics" -f sql/audit.sql

\echo '== Per-agent call volume and errors (all routes) =='
SELECT
  alias                                        AS agent,
  api_name,
  COUNT(*)                                     AS calls,
  COUNT(*) FILTER (WHERE responsecode >= 400)  AS errors
FROM tyk_analytics
WHERE alias <> ''
GROUP BY alias, api_name
ORDER BY agent, api_name;

\echo ''
\echo '== Per-agent LLM token spend (parsed from recorded responses) =='
SELECT
  alias                                        AS agent,
  COUNT(*)                                     AS llm_calls,
  SUM(
    (regexp_match(
       convert_from(decode(rawresponse, 'base64'), 'UTF8'),
       'total_tokens"?\s*:\s*(\d+)'
     ))[1]::int
  )                                            AS total_tokens
FROM tyk_analytics
WHERE api_name = 'openai-llm' AND responsecode = 200
GROUP BY alias
ORDER BY agent;

\echo ''
\echo '== Per-agent, per-MCP-tool call counts (parsed from request bodies) =='
SELECT
  alias                                        AS agent,
  (regexp_match(
     convert_from(decode(rawrequest, 'base64'), 'UTF8'),
     '"name"\s*:\s*"([a-z_]+)"'
   ))[1]                                       AS tool,
  COUNT(*)                                     AS calls
FROM tyk_analytics
WHERE api_name = 'internal-tools'
  AND convert_from(decode(rawrequest, 'base64'), 'UTF8') LIKE '%tools/call%'
GROUP BY agent, tool
ORDER BY agent, tool;
