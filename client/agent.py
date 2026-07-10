"""
Drives the whole governance stack the way two real agents would, and prints what
Tyk enforces at each step. Everything goes through the gateway on :8080 — the
agents never touch the MCP server, the OpenAI key, or each other's budget.

Run after `./tyk/bootstrap/setup.sh` has issued the keys:

    python client/agent.py

It reads the issued keys from client/agents.json.
"""

import asyncio
import json
import pathlib
import sys

import httpx
from fastmcp import Client
from fastmcp.client.transports import StreamableHttpTransport
from openai import OpenAI, APIStatusError

CONF = json.loads((pathlib.Path(__file__).parent / "agents.json").read_text())
GW = CONF["gateway"]
MCP_URL = f"{GW}{CONF['mcp_path']}/mcp"
OPENAI_BASE = f"{GW}{CONF['openai_path']}/v1"
AGENTS = CONF["agents"]


def rule(title):
    print(f"\n{'=' * 68}\n  {title}\n{'=' * 68}")


async def mcp_client(key):
    transport = StreamableHttpTransport(url=MCP_URL, headers={"apikey": key})
    return Client(transport)


async def call_tool(key, tool, args):
    """One tool call in its own MCP session, so a blocked call can't poison others."""
    client = await mcp_client(key)
    async with client:
        res = await client.call_tool(tool, args)
        return res.data


async def demo_per_tool_access():
    rule("1. Per-agent tool access control (enforced at call time by the Go plugin)")
    for name, a in AGENTS.items():
        # Both agents can see both tools...
        client = await mcp_client(a["key"])
        async with client:
            tools = [t.name for t in await client.list_tools()]
        print(f"\n  {name} lists tools: {tools}")
        # ...but only the privileged one can call the sensitive tool.
        try:
            data = await call_tool(a["key"], "lookup_order", {"order_id": "A-1001"})
            print(f"    lookup_order(A-1001) -> {data}")
        except Exception as e:
            print(f"    lookup_order(A-1001) -> {type(e).__name__}")
        try:
            data = await call_tool(a["key"], "issue_refund", {"order_id": "A-1001", "amount": 10})
            print(f"    issue_refund(A-1001) -> {data}")
        except Exception as e:
            print(f"    issue_refund(A-1001) -> BLOCKED by Tyk ({type(e).__name__})")


async def demo_rate_limit_isolation():
    rule("2. Per-agent rate-limit isolation (agent-alpha bursts, agent-beta unaffected)")
    alpha = AGENTS["agent-alpha"]["key"]
    beta = AGENTS["agent-beta"]["key"]
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
    headers_common = {"Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25",
                      "Accept": "application/json, text/event-stream"}
    async with httpx.AsyncClient(timeout=10) as h:
        codes = []
        for _ in range(30):
            r = await h.post(MCP_URL, json=body, headers={**headers_common, "apikey": alpha})
            codes.append(r.status_code)
        print(f"\n  agent-alpha 30 rapid calls -> status codes: {codes}")
        print(f"    {codes.count(429)} rejected with 429 (rate limit tripped)")
        r = await h.post(MCP_URL, json=body, headers={**headers_common, "apikey": beta})
        print(f"  agent-beta call during alpha's burst -> {r.status_code} (isolated, still working)")


def demo_token_budget():
    rule("3. Go token-guard plugin: per-agent LLM token budget")
    for name, a in AGENTS.items():
        budget = a["token_budget"]
        client = OpenAI(base_url=OPENAI_BASE, api_key="unused",
                        default_headers={"apikey": a["key"]})
        print(f"\n  {name} (budget {budget} tokens):")
        for i in range(1, 8):
            try:
                resp = client.chat.completions.create(
                    model="gpt-4o-mini",
                    messages=[{"role": "user", "content": "Write one sentence about API gateways."}],
                    max_tokens=60,
                )
                used = resp.usage.total_tokens
                print(f"    call {i}: 200  (+{used} tokens)")
            except APIStatusError as e:
                if e.status_code == 429:
                    print(f"    call {i}: 429  budget exhausted -> {e.response.text.strip()}")
                    break
                print(f"    call {i}: {e.status_code}  {e.response.text.strip()}")
                break


async def main():
    try:
        await demo_per_tool_access()
        await demo_rate_limit_isolation()
        demo_token_budget()
    except Exception as e:
        print(f"\n[!] {type(e).__name__}: {e}", file=sys.stderr)
        raise
    rule("Done. Now query the audit trail: psql ... -f sql/audit.sql")


if __name__ == "__main__":
    asyncio.run(main())
