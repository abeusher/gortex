# HTTP server, MCP 2026 transport, and Web UI

Gortex exposes three transports — stdio MCP (the default `gortex mcp`), a Unix-socket daemon, and an HTTP API. The HTTP layer is what IDE plugins, CI, the web UI, and remote agents talk to.

- [Server mode (`/v1/*` JSON API)](#server-mode-v1-json-api)
- [MCP 2026 Streamable HTTP transport (`/mcp`)](#mcp-2026-streamable-http-transport-mcp)
- [Web UI](#web-ui)

## Server mode (`/v1/*` JSON API)

The `gortex server` command exposes all MCP tools as an HTTP/JSON API under versioned `/v1/*` routes:

```bash
# Standalone HTTP backend (default bind 127.0.0.1:4747)
gortex server --index /path/to/repo --watch

# Non-localhost bind requires an auth token
gortex server --index . --bind 0.0.0.0 --auth-token "$(openssl rand -hex 32)"

# HTTP API alongside MCP stdio (same process)
gortex mcp --index /path/to/repo --server --port 8765
```

**Endpoints (all under `/v1/`):**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/health` | GET | Status, node/edge counts, uptime |
| `/v1/tools` | GET | List all available tools with descriptions |
| `/v1/tools/{name}` | POST | Invoke any MCP tool with JSON arguments. Accepts `?format=gcx` or top-level `"format"` in the body |
| `/v1/stats` | GET | Graph statistics by kind and language, plus `server_id` + `started_at` |
| `/v1/graph` | GET | Full brief-graph dump (nodes + edges + stats); accepts `?project=` and/or `?repo=` for scoping |
| `/v1/events` | GET | SSE stream of graph-change events (requires `--watch`). Accepts `?token=<t>` for `EventSource` auth |

**Auth & binding.** The server defaults to `--bind 127.0.0.1` and runs unauthenticated on localhost only (logs `server: unauthenticated mode; localhost only`). Set `--auth-token <token>` or `$GORTEX_SERVER_TOKEN` to require `Authorization: Bearer <token>` on every `/v1/*` request (constant-time compare; CORS preflights bypass). Non-localhost binds without a token are rejected at startup. CORS origin is configurable via `--cors-origin` (default `*`). `--bind` also accepts `unix:///path/to.sock` for a Unix-domain socket.

**Scoping the graph.** Pass `--workspace <slug>` to restrict both indexing and queries to repos that resolve to that workspace, and `--scope-project <slug>` to narrow further. A scope that matches zero tracked repos errors out at startup rather than silently producing an empty graph.

**Multi-server roster.** When the daemon is running, it can route MCP traffic across multiple Gortex servers — a local Unix socket for the repos on this machine, plus one or more remote HTTPS servers for shared / cloud indexes. The roster lives at `~/.gortex/servers.toml`; manage it with `gortex daemon server list / add / remove`. Auth tokens can be embedded directly (`--auth-token`) or pulled from an env var the daemon reads at request time (`--auth-token-env`, preferred). Restart the daemon to pick up roster changes.

## MCP 2026 Streamable HTTP transport (`/mcp`)

`gortex server` and `gortex daemon --http-addr <addr>` both expose the **MCP 2026 Streamable HTTP transport** — the wire format the June 2026 MCP release locks in.

| Verb | Path | Behaviour |
|------|------|-----------|
| `POST` | `/mcp` | One or more JSON-RPC frames in, one or a JSON-RPC array out. Notification-only batches return 202. |
| `GET` | `/mcp` | Opens an SSE stream the server uses to push server-initiated notifications (progress, sampling) onto the bound session. |
| `DELETE` | `/mcp` | Terminates a session. Idempotent — returns 204 even when the id is unknown. |
| `OPTIONS` | `/mcp` | CORS preflight; advertises the allowed methods. |

**Stateless per request.** Every POST carries `Mcp-Session-Id`; the transport replays the matching state out of a `streamable.SessionStore` (the default in-memory `MemoryStore` is TTL-evicted; swap for a Redis-backed adapter to share state across replicas behind a load balancer). `initialize` mints the id and returns it on the response header; an unknown id replies with a JSON-RPC `-32001 session not found` envelope. The `Mcp-Protocol-Version` header is echoed when provided; absent, the transport advertises its default. `tools/call` frames flow through the same multi-server router that serves `/v1/tools/<name>`, so workspace scoping carries over unchanged.

**Daemon enablement.** `gortex daemon start --http-addr 127.0.0.1:7411 [--http-auth-token <token>]` brings the transport up alongside the unix-socket dispatcher. Non-localhost binds require an auth token (or `$GORTEX_DAEMON_HTTP_TOKEN`). `/healthz` is exempt so liveness probes work. `gortex server` mounts `/mcp` unconditionally alongside the legacy `/v1/*` surface — no flag needed.

## Web UI

The web UI lives in its own repo at [`gortexhq/web`](https://github.com/gortexhq/web) so it can be deployed independently of the backend (Vercel / a static host / your own Next.js deployment). It's a standalone Next.js 15 app that talks to `gortex server` over `/v1/*`:

```bash
# 1) Start the HTTP backend (localhost:4747 by default, bearer-auth in non-localhost binds)
gortex server --index /path/to/repo --watch

# 2) Clone and run the UI in another terminal
git clone https://github.com/gortexhq/web.git gortex-web && cd gortex-web
echo 'NEXT_PUBLIC_GORTEX_URL=http://localhost:4747' > .env.local
npm install && npm run dev
# Open http://localhost:3000
```

| Page | Features |
|------|----------|
| **Dashboard** | Health, stats, language pie chart, node kind bar chart |
| **Graph Explorer** | Sigma.js 2D + five react-three-fiber 3D modes (City / Strata / Galaxies / Constellation / Graph3D), node filters, selection, detail panel |
| **Search** | Semantic + BM25 search via `/v1/*`, results grouped by kind |
| **Symbol Detail** | Source code, signature, callers/callees/usages/deps tabs |
| **Communities** | Community cards with cohesion bars, expandable members |
| **Processes** | Collapsible call-tree steps, product vs test process split |
| **Analysis** | Dead code, hotspots, cycles, index health — 4 tabs |
| **Contracts** | API contracts (HTTP, gRPC, GraphQL, topics, WebSocket, env vars) with provider/consumer matching, request/response type tracing, `yours / tests / deps / all` scope filter |
| **Services** | Service-level graph visualization with per-repo stats |
| **AI Chat** | LLM-powered chat with code context (placeholder) |
