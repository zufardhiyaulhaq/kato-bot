{% raw %}
# kato-bot MCP Server + Multi-Cluster REST Proxy — Design

**Status:** Approved (design)

**Goal:** Give kato-bot two new inbound front doors over its existing
multi-cluster registry: an **MCP server** (streamable HTTP) and a **REST proxy**
with cluster-prefixed paths. Both expose the full kato API — usecases, methods
(including direct method execution), and run history — for every configured
cluster, with the cluster always an explicit parameter. The Lark adapter is
untouched.

**Depends on:** kato's direct method run endpoint
(`POST /api/v1/methods/{name}/run` — see kato's
`docs/superpowers/specs/2026-07-22-kato-method-run-design.md`). kato ships that
first; everything else here works against current kato.

## Problem

kato-bot already solves multi-cluster fan-out (clusters file → registry →
per-cluster `kato.Client`), but the only consumer is the Lark card flow. Agent
and programmatic consumers (Claude Code, MCP clients, curl/scripts) have no way
in: kato itself is single-cluster and per-cluster URLs may not be reachable from
a laptop. The aggregation kato-bot already owns should be exposed —
"one endpoint, pick your cluster per call."

## Non-goals

- **Auth.** None, matching kato's own stance: network reach is the boundary
  (ClusterIP by default; the operator decides what to expose). Revisit if the
  surface is ever put on an Ingress.
- **stdio MCP transport.** Streamable HTTP only; an in-cluster server cannot be
  spawned as a client subprocess.
- **Session/cluster state in MCP.** Cluster is a parameter on every tool call —
  same stateless philosophy as the card flow.
- **Bot-side rate limiting for the proxy.** kato's own caps
  (`KATO_MAX_CONCURRENT`, `KATO_METHOD_MAX_CONCURRENT`) return 429s that pass
  through. The Lark semaphore (`MAX_CONCURRENT_RUNS`) stays — it protects card
  UX only.
- **Changing the Lark adapter or `internal/core`.** The Lark port keeps its
  narrow 3-method `KatoClient`.

## Topology

```
kato-bot pod
 ├─ :8080  /healthz /readyz          (unchanged)
 ├─ Lark WebSocket (dial-out)        (unchanged)
 └─ :9090                            (new, API_ADDR)
     ├─ /mcp                         MCP streamable HTTP
     └─ /api/v1/clusters[...]        REST proxy
```

One new listener serves both front doors. Env `API_ADDR` (default `:9090`);
chart value `api.enabled` (default `true`) gates the listener, the Service, and
the container port.

## Architecture

```
internal/kato/       client grows: ListMethods, RunMethod, ListRuns, GetRun
                     (full kato API coverage; core.KatoClient stays at 3 methods)

internal/gateway/    NEW — the aggregation layer both front doors share:
                     · its own Client interface (all 7 kato calls), satisfied
                       by *kato.Client (consumer-side interface, so core's
                       port is not widened)
                     · registry: ordered cluster list + name → Client
                     · cluster resolution + error mapping (below)

internal/api/        NEW — REST proxy handlers over the gateway (std mux)
internal/mcp/        NEW — MCP tool definitions over the gateway
                     (github.com/modelcontextprotocol/go-sdk, streamable HTTP)

cmd/kato-bot/        builds one *kato.Client per cluster, registers it in BOTH
                     the core registry (Lark) and the gateway registry; starts
                     the :9090 server when enabled
```

The gateway is the single place that resolves a cluster name and normalizes
errors; REST handlers and MCP tools are thin translations of it.

## REST proxy surface

kato's paths, prefixed by `/api/v1/clusters/{cluster}`; bodies and responses are
kato's JSON **verbatim** (the proxy does not reshape):

| kato-bot route | proxies to kato |
|---|---|
| `GET /api/v1/clusters` | — (registry) |
| `GET /api/v1/clusters/{c}/usecases` | `GET /api/v1/usecases` |
| `GET /api/v1/clusters/{c}/usecases/{name}` | `GET /api/v1/usecases/{name}` |
| `POST /api/v1/clusters/{c}/usecases/{name}/run` | `POST /api/v1/usecases/{name}/run` (incl. `?includeOutputs=`) |
| `GET /api/v1/clusters/{c}/methods` | `GET /api/v1/methods` |
| `POST /api/v1/clusters/{c}/methods/{name}/run` | `POST /api/v1/methods/{name}/run` (new kato endpoint) |
| `GET /api/v1/clusters/{c}/runs` | `GET /api/v1/runs` (incl. `?usecase=`) |
| `GET /api/v1/clusters/{c}/runs/{name}` | `GET /api/v1/runs/{name}` |

Cluster listing (the discovery root — needed before any `/{cluster}/…` path):

```json
GET /api/v1/clusters
200 {"clusters": [{"name": "prod", "label": "Production"}, {"name": "staging"}]}
```

Name + label only (label omitted when empty), registry insertion order. The kato
URL stays internal.

### Error mapping (gateway, shared by both front doors)

| Condition | REST | MCP |
|---|---|---|
| unknown cluster | 404 `{"error": "unknown cluster \"x\""}` | tool error, same message |
| kato non-2xx | same status + kato's `{"error"}` body, verbatim | tool error with kato's detail |
| kato unreachable / timeout | 502 `{"error": "cluster \"x\" unreachable: ..."}` | tool error, same message |

## MCP tools

Served at `/mcp` via the official Go SDK. Cluster is an explicit required
parameter everywhere except `list_clusters`. Tool results carry kato's JSON
verbatim; failures are MCP tool errors.

| Tool | Params | Proxies |
|---|---|---|
| `list_clusters` | — | registry |
| `list_usecases` | `cluster` | GET /usecases |
| `get_usecase` | `cluster`, `usecase` | GET /usecases/{name} |
| `run_usecase` | `cluster`, `usecase`, `inputs` (object), `include_outputs` (bool, default false) | POST /usecases/{name}/run |
| `list_methods` | `cluster` | GET /methods |
| `run_method` | `cluster`, `method`, `params` (object) | POST /methods/{name}/run |
| `list_runs` | `cluster`, `usecase` (optional) | GET /runs |
| `get_run` | `cluster`, `run` | GET /runs/{name} |

Tool descriptions carry the operational facts an agent needs: `run_usecase` is
synchronous and slow (LLM summary; up to `KATO_RUN_TIMEOUT`); `run_method` is a
fast stateless probe; use `list_methods` to discover a method's params before
calling it.

## Config & chart

- Env: `API_ADDR` (default `:9090`). Empty string disables the listener
  (chart sets it from `api.enabled`).
- Timeouts: proxy calls reuse each cluster's existing `kato.Client` and its
  `KATO_RUN_TIMEOUT`-bounded HTTP client. No new timeout knobs.
- Chart: first Service for kato-bot (`port: 9090`, ClusterIP), container port,
  `api.enabled` values flag (default `true`), README regeneration. Single
  replica unchanged — the proxy is stateless.
- Local use: `kubectl port-forward svc/kato-bot 9090` then
  `claude mcp add --transport http kato http://localhost:9090/mcp`.

## Testing strategy

Existing conventions — table tests, `httptest`, no network:

- `internal/kato` — the four new client methods against `httptest` kato fakes,
  including error statuses and the method-run 200-with-`outcome:failed` shape.
- `internal/gateway` — cluster resolution (known/unknown), error normalization
  (kato `*APIError` pass-through, transport → unreachable), cluster listing
  order and label omission. Fake gateway clients.
- `internal/api` — handler tests over the mux: route wiring, verbatim body/
  status pass-through, query-param forwarding (`includeOutputs`, `usecase`).
- `internal/mcp` — tools exercised through the SDK's in-memory transport:
  schema validation (required `cluster`), success payloads, tool-error mapping.
- `internal/config` — `API_ADDR` default/override/disable.

## Sequencing

1. kato: direct method run endpoint (its own spec, in the kato repo).
2. kato-bot: client extension + gateway + REST proxy + MCP server + chart
   (this spec). `run_method` against older katos surfaces kato's 404 verbatim —
   acceptable; operators upgrade kato first.
{% endraw %}
