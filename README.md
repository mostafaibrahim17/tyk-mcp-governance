# Putting an MCP server behind Tyk

A runnable governance stack for AI-agent traffic, built entirely on the
**open-source Tyk Gateway** (MPL 2.0, no license, no Dashboard). One gateway
sits in front of two upstreams:

- an **MCP server** (FastMCP, Streamable HTTP) exposing internal tools
- the **OpenAI API**

…and enforces the five things raw MCP leaves to you: per-agent **auth**,
**rate limits & quotas**, per-tool **access control**, a custom **token budget**,
and a SQL-queryable **audit trail**.

```
                         ┌──────────────────────────── Tyk Gateway (OSS) ───────────┐
   agent-alpha  ─┐       │  per-agent keys · rate limits · quotas                    │      ┌── MCP server
                 ├─ :8080 ┤  Go plugin: per-tool MCP access control + token budget   ├──────┤   (FastMCP)
   agent-beta   ─┘       │                                                           │      └── api.openai.com
                         └───────────────┬───────────────────────────────────────────┘
                                         │ analytics
                                    Redis ─→ Tyk Pump ─→ Postgres  (audit trail)
```

> **OSS vs Dashboard.** This repo runs on the **free open-source gateway**, so the MCP
> server is proxied as a classic Tyk API and per-tool access control is done in the Go
> plugin (which parses the JSON-RPC body). Tyk's **native MCP Gateway** does the same
> declaratively (per-tool rate limits, filtered `tools/list` discovery, JSON-RPC policy),
> and its enforcement engine is open source. The catch: managing its OAS/MCP API
> definitions needs the **licensed Tyk Dashboard**, and a Dashboard-less gateway won't
> mount OAS defs. See the article for that production upgrade path.

## What's here

| Path | What it is |
|------|-----------|
| `docker-compose.yml` | Gateway, Redis, Pump, Postgres, MCP server |
| `mcp-server/server.py` | FastMCP server: `lookup_order` (read), `issue_refund` (sensitive) |
| `plugin/token_guard.go` | Go plugin: per-tool MCP access control + meters `usage.total_tokens` and rejects over-budget agents |
| `tyk/apps/*.json` | Tyk classic API defs (MCP proxy + OpenAI route, both with the plugin bound) |
| `tyk/bootstrap/setup.sh` | Issues one key per agent (with per-API rate limits + tool allow-list) |
| `client/agent.py` | Drives both agents; shows every control firing |
| `sql/audit.sql` | Per-agent call volume, errors, token spend |

## Run it

Requires Docker. A real `OPENAI_API_KEY` is only needed for the token-budget
demo (step 4).

```bash
# 0. Clone
git clone https://github.com/mostafaibrahim17/tyk-mcp-governance
cd tyk-mcp-governance

# 1. Configure
cp .env.example .env         # set OPENAI_API_KEY for the LLM demo

# 2. Build the Go plugin against the exact gateway version (Docker-based).
#    A plugin .so must match the gateway's toolchain/version/arch or it won't load.
make -C plugin plugin

# 3. Start the stack and issue per-agent keys
docker compose up -d
./tyk/bootstrap/setup.sh

# 4. Drive it (needs Python: pip install -r client/requirements.txt)
python client/agent.py

# 5. Query the audit trail (either works)
psql "host=localhost port=5433 user=tyk password=tyk dbname=tyk_analytics" -f sql/audit.sql
# ...or without a local psql:
docker compose exec -T postgres psql -U tyk -d tyk_analytics -f - < sql/audit.sql
```

## What you should see

- **Per-tool access control:** both agents can list tools, but `agent-alpha`'s `issue_refund` call is blocked (`403`) by the plugin while `agent-beta`'s succeeds.
- **Rate-limit isolation:** `agent-alpha` bursts and trips `429`s while `agent-beta` keeps getting `200`.
- **Token budget:** each agent's OpenAI calls succeed until cumulative `total_tokens` crosses its budget, then `429 token budget exhausted`. The Go plugin enforces it, per agent.
- **Audit trail:** `audit.sql` returns per-agent call counts, error counts, and summed token spend from Postgres.

## Notes & gotchas

- **Version pinning is not optional.** The plugin is compiled with
  `tykio/tyk-plugin-compiler:v5.14.0` to match `tykio/tyk-gateway:v5.14.0`. Change
  one, change both (`TYK_VERSION` in `plugin/Makefile` and the compose image tag).
- **stdio MCP servers can't be proxied.** Tyk speaks Streamable HTTP. A stdio
  server needs a stdio→HTTP bridge in front.
- **In-memory token counter.** The plugin keeps per-agent totals in process, which
  is fine for one gateway node. For a cluster, move the counter to Redis.
- **Native MCP Gateway (Dashboard).** To get declarative per-tool rate limits and
  filtered discovery instead of the plugin, run the licensed Tyk Dashboard and define
  the MCP server as an OAS/MCP proxy. The enforcement is open-source; the management
  tooling is not.
- **Managed alternative.** Tyk AI Studio does token/cost and model governance as a
  no-code product if you'd rather not write the plugin.
