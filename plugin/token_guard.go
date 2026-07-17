// token_guard is a Tyk custom Go plugin that gives the gateway the one piece of
// LLM governance the platform deliberately leaves to you: a per-agent token
// budget, metered off the model's own response.
//
// Two hooks, bound only on the OpenAI route (see tyk/apps/openai.json):
//
//   EnforceBudget  (post hook, runs after auth, before upstream)
//       - rejects the agent's request with 429 once it is over budget
//       - injects the real OpenAI key upstream, so agents never see it
//
//   MeterTokens    (response hook, runs on the upstream response)
//       - reads usage.total_tokens off the (non-streaming) OpenAI response
//       - adds it to that agent's running total
//
// State is an in-memory map keyed by agent, shared across both hooks because
// they live in the same compiled .so loaded once into the gateway process.
// For a multi-node gateway you'd move this counter into Redis; the single-node
// tutorial keeps it in-process so the repo has no extra moving parts.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/TykTechnologies/tyk/ctx"
)

// defaultBudget applies when a key carries no meta_data.token_budget.
const defaultBudget int64 = 1000

var (
	mu    sync.Mutex
	spent = map[string]int64{} // agent id -> cumulative total_tokens
)

// agentID identifies the caller. We set a human-readable alias on each key
// (agent-alpha, agent-beta), so the audit trail and this counter line up.
func agentID(r *http.Request) string {
	if s := ctx.GetSession(r); s != nil {
		if s.Alias != "" {
			return s.Alias
		}
		if s.KeyID != "" {
			return s.KeyID
		}
	}
	return ctx.GetAuthToken(r)
}

// budgetFor reads the per-agent budget from the key's session metadata
// (meta_data.token_budget), falling back to defaultBudget.
func budgetFor(r *http.Request) int64 {
	s := ctx.GetSession(r)
	if s == nil {
		return defaultBudget
	}
	v, ok := s.MetaData["token_budget"]
	if !ok {
		return defaultBudget
	}
	switch t := v.(type) {
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return n
		}
	case float64:
		return int64(t)
	case int64:
		return t
	}
	return defaultBudget
}

// EnforceBudget is the post hook: gate over-budget agents, then swap in the
// real upstream credential.
func EnforceBudget(rw http.ResponseWriter, r *http.Request) {
	id := agentID(r)
	budget := budgetFor(r)

	mu.Lock()
	used := spent[id]
	mu.Unlock()

	rw.Header().Set("X-Tokens-Used", strconv.FormatInt(used, 10))
	rw.Header().Set("X-Tokens-Budget", strconv.FormatInt(budget, 10))

	if used >= budget {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(rw, `{"error":"token budget exhausted","agent":"`+id+`","used":`+
			strconv.FormatInt(used, 10)+`,"budget":`+strconv.FormatInt(budget, 10)+`}`)
		return // stops the middleware chain; upstream is never called
	}

	// Budget OK: replace the client's Tyk key with the real OpenAI key so the
	// agent never handles the provider credential.
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
}

// MeterTokens is the response hook: read usage.total_tokens and meter it.
func MeterTokens(rw http.ResponseWriter, res *http.Response, req *http.Request) {
	if res == nil || res.Body == nil {
		return
	}

	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	// Always restore the body untouched so the client gets the full response.
	res.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return
	}

	// The client may have negotiated gzip; decode a copy for parsing only.
	body := raw
	if res.Header.Get("Content-Encoding") == "gzip" {
		if zr, zerr := gzip.NewReader(bytes.NewReader(raw)); zerr == nil {
			if dec, derr := io.ReadAll(zr); derr == nil {
				body = dec
			}
			_ = zr.Close()
		}
	}

	var parsed struct {
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Usage.TotalTokens == 0 {
		return
	}

	id := agentID(req)
	mu.Lock()
	spent[id] += parsed.Usage.TotalTokens
	total := spent[id]
	mu.Unlock()

	// Surface the numbers on the response for the client. The audit trail gets
	// its per-request token count independently, by extracting usage.total_tokens
	// from the recorded response body (detailed analytics recording is on for
	// this route) — see sql/audit.sql.
	res.Header.Set("X-Tokens-Request", strconv.FormatInt(parsed.Usage.TotalTokens, 10))
	res.Header.Set("X-Tokens-Used-Total", strconv.FormatInt(total, 10))
}

// --- MCP per-tool access control -------------------------------------------
//
// The open-source gateway proxies the MCP server as a classic API, so it does
// not parse JSON-RPC itself. This post hook gives us MCP-aware governance: it
// reads the JSON-RPC body and blocks a `tools/call` for any tool the agent is
// not entitled to. Each agent's allow-list rides in its key metadata
// (meta_data.allowed_tools, comma-separated). An empty/absent list = no limit.
//
// (Tyk's native MCP Gateway does this — plus filtered discovery and per-tool
// rate limits — declaratively, with the Dashboard. This is the OSS equivalent.)

func allowedTools(r *http.Request) (map[string]bool, bool) {
	s := ctx.GetSession(r)
	if s == nil {
		return nil, false
	}
	v, ok := s.MetaData["allowed_tools"]
	if !ok {
		return nil, false
	}
	str, _ := v.(string)
	str = strings.TrimSpace(str)
	if str == "" {
		return nil, false
	}
	set := map[string]bool{}
	for _, t := range strings.Split(str, ",") {
		if t = strings.TrimSpace(t); t != "" {
			set[t] = true
		}
	}
	return set, true
}

func McpAccessControl(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body)) // restore for the upstream
	if err != nil {
		return
	}

	var msg struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &msg) != nil || msg.Method != "tools/call" {
		return
	}

	allow, restricted := allowedTools(r)
	if !restricted || allow[msg.Params.Name] {
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(rw, `{"jsonrpc":"2.0","error":{"code":-32001,"message":"tool not permitted for this agent: `+
		msg.Params.Name+`"}}`)
}

func main() {}
