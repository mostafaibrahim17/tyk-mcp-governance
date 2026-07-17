#!/usr/bin/env bash
# Issues one auth-token key per agent and writes them to client/agents.json.
# The API definitions and policies are loaded from files at gateway startup;
# this script only mints the per-agent keys and reloads the gateway.
set -euo pipefail

GW="${GW:-http://localhost:8080}"
SECRET="${TYK_SECRET:-changeme-secret}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

hdr=(-H "x-tyk-authorization: ${SECRET}" -H "Content-Type: application/json")

echo "Waiting for the gateway to be ready..."
for _ in $(seq 1 30); do
  if curl -sf "${hdr[@]}" "${GW}/tyk/reload/group" >/dev/null 2>&1; then break; fi
  sleep 1
done

# Create an auth-token key for an agent. The key carries its own access rights
# and per-API rate limits, plus the per-agent token budget and MCP tool
# allow-list in metadata (both read by the Go plugin).
#   $1 alias  $2 MCP rate/60s  $3 token budget  $4 allowed tools (csv)
create_key() {
  local alias="$1" mcp_rate="$2" budget="$3" tools="$4"
  curl -s "${hdr[@]}" -X POST "${GW}/tyk/keys" -d @- <<JSON | python3 -c "import sys,json;print(json.load(sys.stdin)['key'])"
{
  "org_id": "default",
  "alias": "${alias}",
  "access_rights": {
    "internal-tools": { "api_id": "internal-tools", "api_name": "internal-tools", "versions": ["Default"],
                        "limit": { "rate": ${mcp_rate}, "per": 60, "quota_max": -1 } },
    "openai-llm":     { "api_id": "openai-llm", "api_name": "openai-llm", "versions": ["Default"],
                        "limit": { "rate": 1000, "per": 60, "quota_max": -1 } }
  },
  "meta_data": { "token_budget": ${budget}, "allowed_tools": "${tools}" }
}
JSON
}

echo "Issuing agent keys..."
# agent-alpha: read-only, tight MCP rate limit (20/60) to show isolation.
# agent-beta:  privileged, generous limits.
ALPHA_KEY="$(create_key agent-alpha 20   150    "lookup_order")"
BETA_KEY="$(create_key agent-beta  1000 100000 "lookup_order,issue_refund")"

curl -s "${hdr[@]}" "${GW}/tyk/reload/group" >/dev/null

cat > "${ROOT}/client/agents.json" <<JSON
{
  "gateway": "${GW}",
  "mcp_path": "/internal-tools",
  "openai_path": "/openai",
  "agents": {
    "agent-alpha": { "key": "${ALPHA_KEY}", "token_budget": 150 },
    "agent-beta":  { "key": "${BETA_KEY}",  "token_budget": 100000 }
  }
}
JSON

echo "Done. Keys written to client/agents.json:"
echo "  agent-alpha: ${ALPHA_KEY}"
echo "  agent-beta:  ${BETA_KEY}"
