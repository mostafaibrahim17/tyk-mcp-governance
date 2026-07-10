"""
A tiny "internal tools" MCP server, exposed over Streamable HTTP so Tyk can
proxy and govern it. Two tools on purpose:

  - lookup_order    (read-only, low-risk)   -> every agent may call it
  - issue_refund    (write, sensitive)      -> only privileged agents may call it

That split is what makes Tyk's per-tool access control and *filtered discovery*
(`tools/list` returning only the tools an agent is entitled to) demonstrable.

Run locally:  python server.py   ->   http://0.0.0.0:8000/mcp/
"""

from fastmcp import FastMCP

mcp = FastMCP("internal-tools")

# A pretend order book so the tools return something concrete.
_ORDERS = {
    "A-1001": {"customer": "acme-corp", "total": 420.00, "status": "shipped"},
    "A-1002": {"customer": "hooli", "total": 99.50, "status": "processing"},
}


@mcp.tool
def lookup_order(order_id: str) -> dict:
    """Look up an internal order by its ID. Read-only."""
    order = _ORDERS.get(order_id)
    if order is None:
        return {"error": f"no such order: {order_id}"}
    return {"order_id": order_id, **order}


@mcp.tool
def issue_refund(order_id: str, amount: float) -> dict:
    """Issue a refund against an order. Sensitive / state-changing."""
    order = _ORDERS.get(order_id)
    if order is None:
        return {"error": f"no such order: {order_id}"}
    return {
        "order_id": order_id,
        "refunded": amount,
        "status": "refund_issued",
    }


if __name__ == "__main__":
    # transport="http" is the current name for Streamable HTTP in FastMCP.
    # ("streamable-http" still works as a legacy alias.) Default path is /mcp/.
    mcp.run(transport="http", host="0.0.0.0", port=8000)
