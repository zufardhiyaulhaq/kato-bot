{% raw %}
# kato-bot MCP Server + Multi-Cluster REST Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give kato-bot a second listener (`API_ADDR`, default `:9090`) serving an MCP server (streamable HTTP at `/mcp`) and a cluster-prefixed REST proxy (`/api/v1/clusters/...`) over the existing multi-cluster registry — full kato API coverage including the new direct-method-run endpoint.

**Architecture:** `internal/kato` grows a raw (verbatim `json.RawMessage`) surface beside its typed methods, sharing `do()`. A new `internal/gateway` package owns cluster resolution and error normalization; `internal/api` (REST handlers) and `internal/mcp` (MCP tools) are thin translations of it. `main.go` registers each cluster's one `*kato.Client` into both the core registry (Lark) and the gateway, and starts the API listener when enabled. Lark adapter and `internal/core` are untouched.

**Tech Stack:** Go 1.24, net/http (method-prefixed mux, `PathValue`), `github.com/modelcontextprotocol/go-sdk` **v1.6.1** (pinned; API verified against `go doc` — see Task 4 notes), httptest table tests, Helm + helm-docs.

Spec: `docs/superpowers/specs/2026-07-22-kato-bot-mcp-rest-proxy-design.md`

## Global Constraints

- **Go toolchain:** prefix every `go`/`make`/`gofmt` invocation with `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go` and use `$GOROOT/bin/go` / `$GOROOT/bin/gofmt` (NOT kato's 1.25.2 — this repo is Go 1.24 per `.tool-versions`).
- **DO NOT COMMIT.** Stage with `git add` only. The user commits their own work. Every "Stage" step means stage only.
- **Do NOT create CLAUDE.md.** Do not create branches.
- **Verbatim proxying (spec):** REST responses are kato's JSON byte-for-byte (including kato's capitalized-key wart in `Param`/`OutputField`); MCP tool results carry the same raw JSON as text. Never decode-and-re-encode kato payloads on the proxy path.
- **No auth, no bot-side rate limiting** for the proxy: kato's own 429s pass through. The Lark semaphore is untouched.
- **Error mapping (spec, exact):** unknown cluster → 404 `{"error":"unknown cluster \"x\""}` (REST) / tool error (MCP); kato non-2xx → same status + kato's `{"error":...}` message verbatim; transport failure → 502 `{"error":"cluster \"x\" unreachable: ..."}` / tool error.
- **Cluster listing shape (spec, exact):** `{"clusters":[{"name":"prod","label":"Production"},{"name":"staging"}]}` — name + label only, `label` omitted when empty, registry insertion order. Never expose the kato URL.
- **`internal/core` and `internal/platform/lark` must not change.** `core.KatoClient` stays at 3 methods; the gateway defines its own consumer-side interface.
- **MCP tool names (spec, exact):** `list_clusters`, `list_usecases`, `get_usecase`, `run_usecase`, `list_methods`, `run_method`, `list_runs`, `get_run`. `cluster` is a required param on all but `list_clusters`; `run_usecase` has `include_outputs` (bool, default false).
- **Config:** `API_ADDR` default `:9090`; explicitly set to empty string = listener disabled. Health server on `:8080` unchanged.
- Repo test convention: `$GOROOT/bin/go test -race ./...` (Makefile `test` target uses `-race`).

---

## Task 1: `internal/kato` raw client surface

**Files:**
- Modify: `internal/kato/client.go` (add `raw` helper + 7 exported `Raw*` methods)
- Test: `internal/kato/raw_test.go` (create)

**Interfaces:**
- Consumes: existing `Client.do(ctx, method, path string, body io.Reader) ([]byte, error)` (returns body bytes on 2xx, `*APIError` on non-2xx, plain error on transport failure).
- Produces (Tasks 2–4 rely on these exact signatures on `*kato.Client`):
```go
RawListUseCases(ctx context.Context) (json.RawMessage, error)
RawGetUseCase(ctx context.Context, name string) (json.RawMessage, error)
RawRunUseCase(ctx context.Context, name, rawQuery string, body []byte) (json.RawMessage, error)
RawListMethods(ctx context.Context) (json.RawMessage, error)
RawRunMethod(ctx context.Context, name string, body []byte) (json.RawMessage, error)
RawListRuns(ctx context.Context, rawQuery string) (json.RawMessage, error)
RawGetRun(ctx context.Context, name string) (json.RawMessage, error)
```

- [ ] **Step 1: Write the failing tests**

Create `internal/kato/raw_test.go`:

```go
package kato

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The raw surface forwards method, path, query, and body verbatim, and returns
// kato's response bytes verbatim (no decode/re-encode).
func TestRaw_VerbatimForwarding(t *testing.T) {
	const katoJSON = `{"methods":[{"name":"pod_status","params":[{"Name":"pod","Required":true}]}]}`
	tests := []struct {
		name     string
		call     func(c *Client) ([]byte, error)
		wantMeth string
		wantURI  string // path + rawquery as kato receives it
		wantBody string
	}{
		{"list usecases", func(c *Client) ([]byte, error) { return c.RawListUseCases(context.Background()) },
			"GET", "/api/v1/usecases", ""},
		{"get usecase escapes name", func(c *Client) ([]byte, error) { return c.RawGetUseCase(context.Background(), "a b") },
			"GET", "/api/v1/usecases/a%20b", ""},
		{"run usecase with query+body", func(c *Client) ([]byte, error) {
			return c.RawRunUseCase(context.Background(), "pod-x", "includeOutputs=true", []byte(`{"inputs":{"ns":"a"}}`))
		}, "POST", "/api/v1/usecases/pod-x/run?includeOutputs=true", `{"inputs":{"ns":"a"}}`},
		{"list methods", func(c *Client) ([]byte, error) { return c.RawListMethods(context.Background()) },
			"GET", "/api/v1/methods", ""},
		{"run method", func(c *Client) ([]byte, error) {
			return c.RawRunMethod(context.Background(), "pod_status", []byte(`{"params":{"pod":"x"}}`))
		}, "POST", "/api/v1/methods/pod_status/run", `{"params":{"pod":"x"}}`},
		{"list runs with query", func(c *Client) ([]byte, error) { return c.RawListRuns(context.Background(), "usecase=pod-x") },
			"GET", "/api/v1/runs?usecase=pod-x", ""},
		{"get run", func(c *Client) ([]byte, error) { return c.RawGetRun(context.Background(), "run-1") },
			"GET", "/api/v1/runs/run-1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotReq *http.Request
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotReq = r
				b := make([]byte, 4096)
				n, _ := r.Body.Read(b)
				gotBody = string(b[:n])
				_, _ = w.Write([]byte(katoJSON))
			}))
			defer srv.Close()
			c := New(srv.URL, 5*time.Second, false)

			raw, err := tc.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if string(raw) != katoJSON {
				t.Errorf("raw = %s, want verbatim %s", raw, katoJSON)
			}
			if gotReq.Method != tc.wantMeth {
				t.Errorf("method = %s, want %s", gotReq.Method, tc.wantMeth)
			}
			if got := gotReq.URL.RequestURI(); got != tc.wantURI {
				t.Errorf("uri = %s, want %s", got, tc.wantURI)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tc.wantBody)
			}
		})
	}
}

// Non-2xx surfaces as *APIError with kato's status and extracted message.
func TestRaw_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"too many concurrent method runs"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, 5*time.Second, false)

	_, err := c.RawRunMethod(context.Background(), "pod_status", []byte(`{}`))
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Status != 429 || apiErr.Msg != "too many concurrent method runs" {
		t.Errorf("APIError = %d %q, want 429 + message", apiErr.Status, apiErr.Msg)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test ./internal/kato/ -run TestRaw -v`
Expected: FAIL — compile error `c.RawListUseCases undefined`.

- [ ] **Step 3: Implement the raw surface**

Append to `internal/kato/client.go` (after the existing typed methods, before the `var _ core.KatoClient` line):

```go
// ---- raw surface (verbatim proxying for the gateway/MCP/REST front doors) ----
//
// These return kato's 2xx response bytes untouched (including kato's
// capitalized-key wart on Param/OutputField), so the proxy never reshapes
// payloads. Non-2xx becomes *APIError via do(), like the typed methods.

// raw performs a request with an optional raw query string and body.
func (c *Client) raw(ctx context.Context, method, path, rawQuery string, body []byte) (json.RawMessage, error) {
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	return c.do(ctx, method, path, rd)
}

func (c *Client) RawListUseCases(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/usecases", "", nil)
}

func (c *Client) RawGetUseCase(ctx context.Context, name string) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/usecases/"+url.PathEscape(name), "", nil)
}

// RawRunUseCase forwards the caller's body and raw query (e.g. "includeOutputs=true") verbatim.
func (c *Client) RawRunUseCase(ctx context.Context, name, rawQuery string, body []byte) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/usecases/"+url.PathEscape(name)+"/run", rawQuery, body)
}

func (c *Client) RawListMethods(ctx context.Context) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/methods", "", nil)
}

func (c *Client) RawRunMethod(ctx context.Context, name string, body []byte) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodPost, "/api/v1/methods/"+url.PathEscape(name)+"/run", "", body)
}

// RawListRuns forwards the caller's raw query (e.g. "usecase=pod-x") verbatim.
func (c *Client) RawListRuns(ctx context.Context, rawQuery string) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/runs", rawQuery, nil)
}

func (c *Client) RawGetRun(ctx context.Context, name string) (json.RawMessage, error) {
	return c.raw(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(name), "", nil)
}
```

All needed imports (`bytes`, `io`, `net/url`, `encoding/json`, `net/http`) are already imported by `client.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test -race ./internal/kato/ -v`
Expected: PASS (new `TestRaw_*` plus all existing client tests — regression guard in the same run).

- [ ] **Step 5: gofmt and stage**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/gofmt -l internal/kato/` — must print nothing (run `gofmt -w` on anything it lists, then re-run tests).

```bash
git add internal/kato/client.go internal/kato/raw_test.go
```
(Stage only — do not commit.)

---

## Task 2: `internal/gateway` — cluster resolution + error normalization

**Files:**
- Create: `internal/gateway/gateway.go`
- Test: `internal/gateway/gateway_test.go` (create)

**Interfaces:**
- Consumes: `core.Cluster{Name, Label}`, `core.HTTPStatusError` (implemented by `kato.APIError`), the 7 `Raw*` signatures from Task 1.
- Produces (Tasks 3–5 rely on these exactly):
```go
package gateway

type Client interface { // the 7 Raw* methods, exact Task 1 signatures
	RawListUseCases(ctx context.Context) (json.RawMessage, error)
	RawGetUseCase(ctx context.Context, name string) (json.RawMessage, error)
	RawRunUseCase(ctx context.Context, name, rawQuery string, body []byte) (json.RawMessage, error)
	RawListMethods(ctx context.Context) (json.RawMessage, error)
	RawRunMethod(ctx context.Context, name string, body []byte) (json.RawMessage, error)
	RawListRuns(ctx context.Context, rawQuery string) (json.RawMessage, error)
	RawGetRun(ctx context.Context, name string) (json.RawMessage, error)
}

func New() *Gateway
func (g *Gateway) Add(c core.Cluster, cl Client)
func (g *Gateway) Clusters() []core.Cluster            // insertion order, copy
func (g *Gateway) Get(name string) (Client, bool)
func (g *Gateway) ClustersJSON() []byte                // {"clusters":[...]} spec shape

type Error struct{ Status int; Msg string }             // normalized proxy error
func (e *Error) Error() string
func UnknownCluster(name string) *Error                 // 404, `unknown cluster "x"`
func Normalize(cluster string, err error) *Error        // APIError passthrough | 502 unreachable
```

- [ ] **Step 1: Write the failing tests**

Create `internal/gateway/gateway_test.go`:

```go
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
	"github.com/zufardhiyaulhaq/kato-bot/internal/kato"
)

// *kato.Client must satisfy gateway.Client (compile-time proof the Raw surface matches).
var _ Client = (*kato.Client)(nil)

// fakeClient satisfies Client; only the methods a test calls matter.
type fakeClient struct{ Client }

func TestClustersJSON_SpecShape(t *testing.T) {
	g := New()
	g.Add(core.Cluster{Name: "prod", Label: "Production"}, fakeClient{})
	g.Add(core.Cluster{Name: "staging"}, fakeClient{})

	got := string(g.ClustersJSON())
	want := `{"clusters":[{"name":"prod","label":"Production"},{"name":"staging"}]}`
	if got != want {
		t.Errorf("ClustersJSON = %s, want %s (label omitted when empty, insertion order)", got, want)
	}
}

func TestGet(t *testing.T) {
	g := New()
	g.Add(core.Cluster{Name: "prod"}, fakeClient{})
	if _, ok := g.Get("prod"); !ok {
		t.Error("Get(prod) = miss, want hit")
	}
	if _, ok := g.Get("nope"); ok {
		t.Error("Get(nope) = hit, want miss")
	}
}

func TestClusters_IsACopy(t *testing.T) {
	g := New()
	g.Add(core.Cluster{Name: "prod"}, fakeClient{})
	list := g.Clusters()
	list[0].Name = "mutated"
	if g.Clusters()[0].Name != "prod" {
		t.Error("Clusters() must return a copy")
	}
}

// Error normalization: kato *APIError (via core.HTTPStatusError) passes its
// status + message through; anything else is 502 unreachable.
func TestNormalize(t *testing.T) {
	apiErr := &kato.APIError{Status: 429, Msg: "too many concurrent method runs"}
	if e := Normalize("prod", apiErr); e.Status != 429 || e.Msg != "too many concurrent method runs" {
		t.Errorf("Normalize(APIError) = %d %q, want 429 passthrough", e.Status, e.Msg)
	}
	if e := Normalize("prod", errors.New("dial tcp: connection refused")); e.Status != 502 {
		t.Errorf("Normalize(transport) status = %d, want 502", e.Status)
	} else if e.Msg != `cluster "prod" unreachable: dial tcp: connection refused` {
		t.Errorf("Normalize(transport) msg = %q", e.Msg)
	}
}

func TestUnknownCluster(t *testing.T) {
	e := UnknownCluster("x")
	if e.Status != 404 || e.Msg != `unknown cluster "x"` {
		t.Errorf("UnknownCluster = %d %q", e.Status, e.Msg)
	}
}

// ClustersJSON output must be stable, valid JSON even with zero clusters.
func TestClustersJSON_Empty(t *testing.T) {
	var v struct {
		Clusters []core.Cluster `json:"clusters"`
	}
	if err := json.Unmarshal(New().ClustersJSON(), &v); err != nil {
		t.Fatalf("empty ClustersJSON invalid: %v", err)
	}
	if len(v.Clusters) != 0 {
		t.Errorf("want zero clusters, got %v", v.Clusters)
	}
}

// Silence unused-import when context isn't otherwise referenced.
var _ = context.Background
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test ./internal/gateway/ -v`
Expected: FAIL — package does not exist / undefined symbols.

- [ ] **Step 3: Implement**

Create `internal/gateway/gateway.go`:

```go
// Package gateway is the aggregation layer shared by kato-bot's REST proxy and
// MCP server: it resolves a cluster name to its kato client and normalizes
// upstream failures. Both front doors are thin translations of this package.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// Client is the raw kato surface the gateway proxies (implemented by
// *kato.Client's Raw* methods). Consumer-side interface: core.KatoClient
// stays narrow for the Lark flow; this one covers the full kato API.
type Client interface {
	RawListUseCases(ctx context.Context) (json.RawMessage, error)
	RawGetUseCase(ctx context.Context, name string) (json.RawMessage, error)
	RawRunUseCase(ctx context.Context, name, rawQuery string, body []byte) (json.RawMessage, error)
	RawListMethods(ctx context.Context) (json.RawMessage, error)
	RawRunMethod(ctx context.Context, name string, body []byte) (json.RawMessage, error)
	RawListRuns(ctx context.Context, rawQuery string) (json.RawMessage, error)
	RawGetRun(ctx context.Context, name string) (json.RawMessage, error)
}

// Gateway maps cluster names to clients, preserving configuration order.
// Built once at startup, read-only thereafter (safe for concurrent reads).
type Gateway struct {
	order   []core.Cluster
	clients map[string]Client
}

func New() *Gateway {
	return &Gateway{clients: map[string]Client{}}
}

// Add registers a cluster; a duplicate name overwrites the client without
// duplicating the ordered entry (mirrors core.Registry semantics).
func (g *Gateway) Add(c core.Cluster, cl Client) {
	if _, exists := g.clients[c.Name]; !exists {
		g.order = append(g.order, c)
	}
	g.clients[c.Name] = cl
}

// Clusters returns the registered clusters in insertion order (a copy).
func (g *Gateway) Clusters() []core.Cluster {
	out := make([]core.Cluster, len(g.order))
	copy(out, g.order)
	return out
}

// Get resolves the client for a cluster name.
func (g *Gateway) Get(name string) (Client, bool) {
	c, ok := g.clients[name]
	return c, ok
}

// clusterView is the wire shape of one cluster: name + optional label, never the URL.
type clusterView struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// ClustersJSON renders the spec's cluster listing: insertion order, label
// omitted when empty. Marshaling a []clusterView cannot fail.
func (g *Gateway) ClustersJSON() []byte {
	views := make([]clusterView, 0, len(g.order))
	for _, c := range g.order {
		views = append(views, clusterView{Name: c.Name, Label: c.Label})
	}
	b, _ := json.Marshal(map[string]any{"clusters": views})
	return b
}

// Error is a normalized proxy failure: the HTTP status the REST front door
// returns and the message both front doors surface.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// UnknownCluster is the 404 for a cluster name not in the gateway.
func UnknownCluster(name string) *Error {
	return &Error{Status: http.StatusNotFound, Msg: fmt.Sprintf("unknown cluster %q", name)}
}

// Normalize maps an upstream call failure: a kato status-bearing error passes
// its status + message through verbatim; anything else (transport, timeout)
// is a 502 naming the unreachable cluster.
func Normalize(cluster string, err error) *Error {
	var se core.HTTPStatusError
	if errors.As(err, &se) {
		return &Error{Status: se.HTTPStatus(), Msg: se.Detail()}
	}
	return &Error{Status: http.StatusBadGateway, Msg: fmt.Sprintf("cluster %q unreachable: %v", cluster, err)}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test -race ./internal/gateway/ -v`
Expected: PASS (all six tests, including the `var _ Client = (*kato.Client)(nil)` compile-time check).

- [ ] **Step 5: gofmt and stage**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/gofmt -l internal/gateway/` — must print nothing.

```bash
git add internal/gateway/
```
(Stage only — do not commit.)

---

## Task 3: `internal/api` — REST proxy handlers

**Files:**
- Create: `internal/api/api.go`
- Test: `internal/api/api_test.go` (create)

**Interfaces:**
- Consumes: `gateway.Gateway` (`Get`, `ClustersJSON`), `gateway.Normalize`, `gateway.UnknownCluster` (Task 2).
- Produces: `func Register(mux *http.ServeMux, g *gateway.Gateway)` — Task 5's `main.go` calls this on the API listener's mux.

- [ ] **Step 1: Write the failing tests**

Create `internal/api/api_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
)

// fakeKato records the last raw call and returns canned bytes or an error.
type fakeKato struct {
	lastCall  string // e.g. "RunMethod pod_status" — method + name
	lastQuery string
	lastBody  []byte
	resp      json.RawMessage
	err       error
}

func (f *fakeKato) ret() (json.RawMessage, error) { return f.resp, f.err }
func (f *fakeKato) RawListUseCases(context.Context) (json.RawMessage, error) {
	f.lastCall = "ListUseCases"
	return f.ret()
}
func (f *fakeKato) RawGetUseCase(_ context.Context, name string) (json.RawMessage, error) {
	f.lastCall = "GetUseCase " + name
	return f.ret()
}
func (f *fakeKato) RawRunUseCase(_ context.Context, name, rawQuery string, body []byte) (json.RawMessage, error) {
	f.lastCall, f.lastQuery, f.lastBody = "RunUseCase "+name, rawQuery, body
	return f.ret()
}
func (f *fakeKato) RawListMethods(context.Context) (json.RawMessage, error) {
	f.lastCall = "ListMethods"
	return f.ret()
}
func (f *fakeKato) RawRunMethod(_ context.Context, name string, body []byte) (json.RawMessage, error) {
	f.lastCall, f.lastBody = "RunMethod "+name, body
	return f.ret()
}
func (f *fakeKato) RawListRuns(_ context.Context, rawQuery string) (json.RawMessage, error) {
	f.lastCall, f.lastQuery = "ListRuns", rawQuery
	return f.ret()
}
func (f *fakeKato) RawGetRun(_ context.Context, name string) (json.RawMessage, error) {
	f.lastCall = "GetRun " + name
	return f.ret()
}

func newAPI(fake *fakeKato) http.Handler {
	g := gateway.New()
	g.Add(core.Cluster{Name: "prod", Label: "Production"}, fake)
	mux := http.NewServeMux()
	Register(mux, g)
	return mux
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestClustersEndpoint(t *testing.T) {
	w := do(newAPI(&fakeKato{}), "GET", "/api/v1/clusters", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	want := `{"clusters":[{"name":"prod","label":"Production"}]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// Every proxy route forwards to the right client call and returns kato's
// bytes verbatim with pass-through of query and body.
func TestRoutes_VerbatimPassthrough(t *testing.T) {
	const katoJSON = `{"anything":["verbatim",{"Name":"CapitalizedWartKept"}]}`
	tests := []struct {
		name, method, target, body string
		wantCall, wantQuery        string
		wantBody                   string
	}{
		{"list usecases", "GET", "/api/v1/clusters/prod/usecases", "", "ListUseCases", "", ""},
		{"get usecase", "GET", "/api/v1/clusters/prod/usecases/pod-x", "", "GetUseCase pod-x", "", ""},
		{"run usecase", "POST", "/api/v1/clusters/prod/usecases/pod-x/run?includeOutputs=true",
			`{"inputs":{"ns":"a"}}`, "RunUseCase pod-x", "includeOutputs=true", `{"inputs":{"ns":"a"}}`},
		{"list methods", "GET", "/api/v1/clusters/prod/methods", "", "ListMethods", "", ""},
		{"run method", "POST", "/api/v1/clusters/prod/methods/pod_status/run",
			`{"params":{"pod":"x"}}`, "RunMethod pod_status", "", `{"params":{"pod":"x"}}`},
		{"list runs", "GET", "/api/v1/clusters/prod/runs?usecase=pod-x", "", "ListRuns", "usecase=pod-x", ""},
		{"get run", "GET", "/api/v1/clusters/prod/runs/run-1", "", "GetRun run-1", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKato{resp: json.RawMessage(katoJSON)}
			w := do(newAPI(fake), tc.method, tc.target, tc.body)
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
			}
			if w.Body.String() != katoJSON {
				t.Errorf("body = %s, want verbatim kato bytes", w.Body.String())
			}
			if fake.lastCall != tc.wantCall {
				t.Errorf("call = %q, want %q", fake.lastCall, tc.wantCall)
			}
			if fake.lastQuery != tc.wantQuery {
				t.Errorf("query = %q, want %q", fake.lastQuery, tc.wantQuery)
			}
			if string(fake.lastBody) != tc.wantBody {
				t.Errorf("body forwarded = %q, want %q", fake.lastBody, tc.wantBody)
			}
		})
	}
}

func TestUnknownCluster404(t *testing.T) {
	w := do(newAPI(&fakeKato{}), "GET", "/api/v1/clusters/nope/usecases", "")
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if want := `{"error":"unknown cluster \"nope\""}`; strings.TrimSpace(w.Body.String()) != want {
		t.Errorf("body = %s, want %s", w.Body.String(), want)
	}
}

// kato non-2xx passes through status + {"error"} body; transport error is 502.
func TestUpstreamErrorMapping(t *testing.T) {
	t.Run("kato 429 passthrough", func(t *testing.T) {
		fake := &fakeKato{err: &apiErr429{}}
		w := do(newAPI(fake), "GET", "/api/v1/clusters/prod/usecases", "")
		if w.Code != 429 {
			t.Fatalf("status = %d, want 429", w.Code)
		}
		if want := `{"error":"kato is busy"}`; strings.TrimSpace(w.Body.String()) != want {
			t.Errorf("body = %s, want %s", w.Body.String(), want)
		}
	})
	t.Run("transport 502", func(t *testing.T) {
		fake := &fakeKato{err: errors.New("dial tcp: refused")}
		w := do(newAPI(fake), "GET", "/api/v1/clusters/prod/usecases", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), `cluster \"prod\" unreachable`) {
			t.Errorf("body = %s, want unreachable message", w.Body.String())
		}
	})
}

// apiErr429 implements core.HTTPStatusError like kato.APIError does.
type apiErr429 struct{}

func (*apiErr429) Error() string   { return "kato 429: kato is busy" }
func (*apiErr429) HTTPStatus() int { return 429 }
func (*apiErr429) Detail() string  { return "kato is busy" }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test ./internal/api/ -v`
Expected: FAIL — package/`Register` undefined.

- [ ] **Step 3: Implement**

Create `internal/api/api.go`:

```go
// Package api is kato-bot's REST proxy front door: kato's API surface,
// prefixed by /api/v1/clusters/{cluster}, passing requests and responses
// through verbatim. All logic lives in internal/gateway.
package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
)

// maxBodyBytes bounds proxied request bodies; kato inputs/params are tiny.
const maxBodyBytes = 1 << 20

// Register mounts the proxy routes on mux.
func Register(mux *http.ServeMux, g *gateway.Gateway) {
	mux.HandleFunc("GET /api/v1/clusters", func(w http.ResponseWriter, _ *http.Request) {
		writeRaw(w, http.StatusOK, g.ClustersJSON())
	})
	proxy := func(pattern string, call func(cl gateway.Client, r *http.Request) (json.RawMessage, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("cluster")
			cl, ok := g.Get(name)
			if !ok {
				writeGatewayErr(w, gateway.UnknownCluster(name))
				return
			}
			raw, err := call(cl, r)
			if err != nil {
				writeGatewayErr(w, gateway.Normalize(name, err))
				return
			}
			writeRaw(w, http.StatusOK, raw)
		})
	}

	proxy("GET /api/v1/clusters/{cluster}/usecases", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListUseCases(r.Context())
	})
	proxy("GET /api/v1/clusters/{cluster}/usecases/{name}", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawGetUseCase(r.Context(), r.PathValue("name"))
	})
	proxy("POST /api/v1/clusters/{cluster}/usecases/{name}/run", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		return cl.RawRunUseCase(r.Context(), r.PathValue("name"), r.URL.RawQuery, body)
	})
	proxy("GET /api/v1/clusters/{cluster}/methods", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListMethods(r.Context())
	})
	proxy("POST /api/v1/clusters/{cluster}/methods/{name}/run", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		return cl.RawRunMethod(r.Context(), r.PathValue("name"), body)
	})
	proxy("GET /api/v1/clusters/{cluster}/runs", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawListRuns(r.Context(), r.URL.RawQuery)
	})
	proxy("GET /api/v1/clusters/{cluster}/runs/{name}", func(cl gateway.Client, r *http.Request) (json.RawMessage, error) {
		return cl.RawGetRun(r.Context(), r.PathValue("name"))
	})
}

// readBody reads a bounded request body ([] for none).
func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeGatewayErr(w http.ResponseWriter, e *gateway.Error) {
	b, _ := json.Marshal(map[string]string{"error": e.Msg})
	writeRaw(w, e.Status, b)
}
```

Note on `readBody` errors: an oversized body makes `io.ReadAll` return an error, which flows through `gateway.Normalize` → 502. That is acceptable for v1 (bodies are tiny maps); do not add a special 413 path.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test -race ./internal/api/ -v`
Expected: PASS (all four test functions, 7 passthrough subtests).

- [ ] **Step 5: gofmt and stage**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/gofmt -l internal/api/` — must print nothing.

```bash
git add internal/api/
```
(Stage only — do not commit.)

---

## Task 4: `internal/mcp` — MCP server with 8 tools

**Files:**
- Modify: `go.mod`/`go.sum` (add `github.com/modelcontextprotocol/go-sdk v1.6.1`)
- Create: `internal/mcp/server.go`
- Test: `internal/mcp/server_test.go` (create)

**Interfaces:**
- Consumes: `gateway.Gateway` / `gateway.Client` / `gateway.Normalize` / `gateway.UnknownCluster` (Task 2).
- Produces: `func NewServer(g *gateway.Gateway) *sdkmcp.Server` and `func Handler(s *sdkmcp.Server) http.Handler` — Task 5 mounts `Handler` at `/mcp`.

**Verified SDK facts (go doc, v1.6.1)** — trust these over memory:
- `sdkmcp.NewServer(&sdkmcp.Implementation{Name, Version}, nil) *Server`.
- `sdkmcp.AddTool[In, Out](s, &sdkmcp.Tool{Name, Description}, h)` — input schema inferred from `In` (struct or map; `jsonschema` struct tags become property descriptions). `Out = any` → no output schema.
- Handler type: `func(ctx context.Context, req *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, Out, error)`. **A returned error becomes a tool error (`IsError` set), not a protocol error.** Returning a `*CallToolResult` with `Content` set is the way to return unstructured text.
- `sdkmcp.TextContent{Text: string}` is the text content type; `CallToolResult.Content []sdkmcp.Content`.
- `sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server, nil)` returns an `http.Handler`.
- Tests: `st, ct := sdkmcp.NewInMemoryTransports()`; connect server first (`s.Connect(ctx, st, nil)`), then `sdkmcp.NewClient(&sdkmcp.Implementation{...}, nil).Connect(ctx, ct, nil)` → `*ClientSession` with `CallTool(ctx, &sdkmcp.CallToolParams{Name, Arguments})`.
- VERIFY during implementation: required-vs-optional fields in the inferred schema (jsonschema-go marks fields required unless the json tag has `omitempty`). Confirm with `go doc github.com/google/jsonschema-go/jsonschema For` once the dep is fetched; if the semantics differ, adjust the input structs so `cluster` & co. are required and the optional fields (`inputs`, `params`, `usecase` filter, `include_outputs`) are not, and note it in your report.

- [ ] **Step 1: Add the dependency**

```bash
GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go get github.com/modelcontextprotocol/go-sdk@v1.6.1
```
Expected: go.mod gains the require; go.sum updated.

- [ ] **Step 2: Write the failing tests**

Create `internal/mcp/server_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
)

// fakeKato mirrors internal/api's fake: records the last call, returns canned bytes.
type fakeKato struct {
	lastCall  string
	lastQuery string
	lastBody  []byte
	resp      json.RawMessage
	err       error
}

func (f *fakeKato) ret() (json.RawMessage, error) { return f.resp, f.err }
func (f *fakeKato) RawListUseCases(context.Context) (json.RawMessage, error) {
	f.lastCall = "ListUseCases"
	return f.ret()
}
func (f *fakeKato) RawGetUseCase(_ context.Context, name string) (json.RawMessage, error) {
	f.lastCall = "GetUseCase " + name
	return f.ret()
}
func (f *fakeKato) RawRunUseCase(_ context.Context, name, rawQuery string, body []byte) (json.RawMessage, error) {
	f.lastCall, f.lastQuery, f.lastBody = "RunUseCase "+name, rawQuery, body
	return f.ret()
}
func (f *fakeKato) RawListMethods(context.Context) (json.RawMessage, error) {
	f.lastCall = "ListMethods"
	return f.ret()
}
func (f *fakeKato) RawRunMethod(_ context.Context, name string, body []byte) (json.RawMessage, error) {
	f.lastCall, f.lastBody = "RunMethod "+name, body
	return f.ret()
}
func (f *fakeKato) RawListRuns(_ context.Context, rawQuery string) (json.RawMessage, error) {
	f.lastCall, f.lastQuery = "ListRuns", rawQuery
	return f.ret()
}
func (f *fakeKato) RawGetRun(_ context.Context, name string) (json.RawMessage, error) {
	f.lastCall = "GetRun " + name
	return f.ret()
}

// session spins up the MCP server over in-memory transports and returns a
// connected client session.
func session(t *testing.T, fake *fakeKato) *sdkmcp.ClientSession {
	t.Helper()
	g := gateway.New()
	g.Add(core.Cluster{Name: "prod", Label: "Production"}, fake)
	srv := NewServer(g)

	st, ct := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// textOf returns the concatenated text content of a result.
func textOf(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func call(t *testing.T, cs *sdkmcp.ClientSession, tool string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	return res
}

// All 8 tools are registered.
func TestToolsRegistered(t *testing.T) {
	cs := session(t, &fakeKato{resp: json.RawMessage(`{}`)})
	tools, err := cs.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"list_clusters": false, "list_usecases": false, "get_usecase": false,
		"run_usecase": false, "list_methods": false, "run_method": false,
		"list_runs": false, "get_run": false,
	}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		} else {
			t.Errorf("unexpected tool %q", tool.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q not registered", name)
		}
	}
}

func TestListClusters(t *testing.T) {
	cs := session(t, &fakeKato{})
	res := call(t, cs, "list_clusters", map[string]any{})
	if res.IsError {
		t.Fatalf("IsError, content: %s", textOf(t, res))
	}
	want := `{"clusters":[{"name":"prod","label":"Production"}]}`
	if got := textOf(t, res); got != want {
		t.Errorf("text = %s, want %s", got, want)
	}
}

// A proxy tool returns kato's JSON verbatim as text.
func TestListUseCases_Verbatim(t *testing.T) {
	const katoJSON = `{"usecases":[{"name":"pod-x","ready":true}]}`
	fake := &fakeKato{resp: json.RawMessage(katoJSON)}
	cs := session(t, fake)
	res := call(t, cs, "list_usecases", map[string]any{"cluster": "prod"})
	if res.IsError {
		t.Fatalf("IsError, content: %s", textOf(t, res))
	}
	if got := textOf(t, res); got != katoJSON {
		t.Errorf("text = %s, want verbatim %s", got, katoJSON)
	}
	if fake.lastCall != "ListUseCases" {
		t.Errorf("call = %q", fake.lastCall)
	}
}

// run_method marshals params into kato's body shape and names the method.
func TestRunMethod_Plumbing(t *testing.T) {
	fake := &fakeKato{resp: json.RawMessage(`{"outcome":"completed","outputs":{}}`)}
	cs := session(t, fake)
	res := call(t, cs, "run_method", map[string]any{
		"cluster": "prod", "method": "pod_status",
		"params": map[string]any{"namespace": "payments", "pod": "api-0"},
	})
	if res.IsError {
		t.Fatalf("IsError, content: %s", textOf(t, res))
	}
	if fake.lastCall != "RunMethod pod_status" {
		t.Errorf("call = %q, want RunMethod pod_status", fake.lastCall)
	}
	var body struct {
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal(fake.lastBody, &body); err != nil {
		t.Fatalf("body sent: %s (%v)", fake.lastBody, err)
	}
	if body.Params["namespace"] != "payments" || body.Params["pod"] != "api-0" {
		t.Errorf("params = %v", body.Params)
	}
}

// run_usecase: include_outputs=false (default) → includeOutputs=false query;
// true → includeOutputs=true; inputs marshaled into kato's body shape.
func TestRunUseCase_Plumbing(t *testing.T) {
	fake := &fakeKato{resp: json.RawMessage(`{"run":"r1","phase":"Succeeded"}`)}
	cs := session(t, fake)
	res := call(t, cs, "run_usecase", map[string]any{
		"cluster": "prod", "usecase": "pod-x",
		"inputs": map[string]any{"namespace": "payments"},
	})
	if res.IsError {
		t.Fatalf("IsError, content: %s", textOf(t, res))
	}
	if fake.lastCall != "RunUseCase pod-x" {
		t.Errorf("call = %q", fake.lastCall)
	}
	if fake.lastQuery != "includeOutputs=false" {
		t.Errorf("query = %q, want includeOutputs=false (default)", fake.lastQuery)
	}
	call(t, cs, "run_usecase", map[string]any{
		"cluster": "prod", "usecase": "pod-x", "include_outputs": true,
	})
	if fake.lastQuery != "includeOutputs=true" {
		t.Errorf("query = %q, want includeOutputs=true", fake.lastQuery)
	}
}

// list_runs forwards the optional usecase filter.
func TestListRuns_Filter(t *testing.T) {
	fake := &fakeKato{resp: json.RawMessage(`{"runs":[]}`)}
	cs := session(t, fake)
	call(t, cs, "list_runs", map[string]any{"cluster": "prod"})
	if fake.lastQuery != "" {
		t.Errorf("query = %q, want empty when no filter", fake.lastQuery)
	}
	call(t, cs, "list_runs", map[string]any{"cluster": "prod", "usecase": "pod x"})
	if fake.lastQuery != "usecase=pod+x" {
		t.Errorf("query = %q, want usecase=pod+x (escaped)", fake.lastQuery)
	}
}

// Unknown cluster and upstream failures surface as tool errors with the
// gateway's message, never as protocol errors.
func TestToolErrors(t *testing.T) {
	cs := session(t, &fakeKato{err: apiErr429{}})
	res := call(t, cs, "list_usecases", map[string]any{"cluster": "nope"})
	if !res.IsError || !strings.Contains(textOf(t, res), `unknown cluster "nope"`) {
		t.Errorf("unknown cluster: IsError=%v content=%s", res.IsError, textOf(t, res))
	}
	res = call(t, cs, "list_usecases", map[string]any{"cluster": "prod"})
	if !res.IsError || !strings.Contains(textOf(t, res), "kato is busy") {
		t.Errorf("upstream error: IsError=%v content=%s", res.IsError, textOf(t, res))
	}
}

type apiErr429 struct{}

func (apiErr429) Error() string   { return "kato 429: kato is busy" }
func (apiErr429) HTTPStatus() int { return 429 }
func (apiErr429) Detail() string  { return "kato is busy" }
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test ./internal/mcp/ -v`
Expected: FAIL — package/`NewServer` undefined.

- [ ] **Step 4: Implement**

Create `internal/mcp/server.go`:

```go
// Package mcp is kato-bot's MCP front door: 8 tools over the gateway,
// served via streamable HTTP. Tool results carry kato's JSON verbatim as
// text; failures surface as tool errors with the gateway's message.
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
)

// serverName/serverVersion identify kato-bot to MCP clients.
const (
	serverName    = "kato-bot"
	serverVersion = "0.1.5"
)

type listUseCasesIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
}
type getUseCaseIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
	UseCase string `json:"usecase" jsonschema:"use case name (discover via list_usecases)"`
}
type runUseCaseIn struct {
	Cluster        string            `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
	UseCase        string            `json:"usecase" jsonschema:"use case name (discover via list_usecases)"`
	Inputs         map[string]string `json:"inputs,omitempty" jsonschema:"use case inputs; all values are strings"`
	IncludeOutputs bool              `json:"include_outputs,omitempty" jsonschema:"include per-step raw outputs in the response (default false)"`
}
type listMethodsIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
}
type runMethodIn struct {
	Cluster string            `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
	Method  string            `json:"method" jsonschema:"method name (discover via list_methods)"`
	Params  map[string]string `json:"params,omitempty" jsonschema:"method params; all values are strings"`
}
type listRunsIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
	UseCase string `json:"usecase,omitempty" jsonschema:"optional: only runs of this use case"`
}
type getRunIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name (discover via list_clusters)"`
	Run     string `json:"run" jsonschema:"run name (from run_usecase's run field or list_runs)"`
}

// NewServer builds the MCP server with all 8 tools registered over g.
func NewServer(g *gateway.Gateway) *sdkmcp.Server {
	s := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: serverVersion}, nil)

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_clusters",
		Description: "List the configured kato clusters (name + label). Call this first: every other tool needs a cluster name.",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, _ any) (*sdkmcp.CallToolResult, any, error) {
		return textResult(g.ClustersJSON()), nil, nil
	})

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_usecases",
		Description: "List a cluster's kato troubleshooting use cases (name, description, declared inputs, ready).",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in listUseCasesIn) (json.RawMessage, error) {
		return cl.RawListUseCases(ctx)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_usecase",
		Description: "Get one use case's contract: description and declared inputs (name, required, default).",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in getUseCaseIn) (json.RawMessage, error) {
		return cl.RawGetUseCase(ctx, in.UseCase)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "run_usecase",
		Description: "Execute a kato use case. SYNCHRONOUS AND SLOW: kato runs every step and writes an LLM summary before responding (tens of seconds to minutes). Returns run name, phase, summary, warning.",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in runUseCaseIn) (json.RawMessage, error) {
		body, err := json.Marshal(map[string]any{"inputs": nonNil(in.Inputs)})
		if err != nil {
			return nil, err
		}
		q := "includeOutputs=false"
		if in.IncludeOutputs {
			q = "includeOutputs=true"
		}
		return cl.RawRunUseCase(ctx, in.UseCase, q, body)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_methods",
		Description: "List kato's built-in read-only check methods with their params and output fields. Use before run_method to discover a method's params.",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in listMethodsIn) (json.RawMessage, error) {
		return cl.RawListMethods(ctx)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "run_method",
		Description: "Execute one kato method directly — a fast stateless probe (no run persisted, no LLM). Returns outcome (completed|failed), outputs, and error; a failed outcome is a finding, not a transport failure.",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in runMethodIn) (json.RawMessage, error) {
		body, err := json.Marshal(map[string]any{"params": nonNil(in.Params)})
		if err != nil {
			return nil, err
		}
		return cl.RawRunMethod(ctx, in.Method, body)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "list_runs",
		Description: "List past kato runs (newest first), optionally filtered by use case.",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in listRunsIn) (json.RawMessage, error) {
		q := ""
		if in.UseCase != "" {
			q = "usecase=" + url.QueryEscape(in.UseCase)
		}
		return cl.RawListRuns(ctx, q)
	}))

	sdkmcp.AddTool(s, &sdkmcp.Tool{
		Name:        "get_run",
		Description: "Get one past run's full audit record (per-step outputs, summary, timings).",
	}, proxyTool(g, func(ctx context.Context, cl gateway.Client, in getRunIn) (json.RawMessage, error) {
		return cl.RawGetRun(ctx, in.Run)
	}))

	return s
}

// Handler serves s over streamable HTTP (mounted at /mcp by main).
func Handler(s *sdkmcp.Server) http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return s }, nil)
}

// clusterCarrier lets proxyTool read the cluster field off any input struct.
type clusterCarrier interface{ clusterName() string }

func (i listUseCasesIn) clusterName() string { return i.Cluster }
func (i getUseCaseIn) clusterName() string   { return i.Cluster }
func (i runUseCaseIn) clusterName() string   { return i.Cluster }
func (i listMethodsIn) clusterName() string  { return i.Cluster }
func (i runMethodIn) clusterName() string    { return i.Cluster }
func (i listRunsIn) clusterName() string     { return i.Cluster }
func (i getRunIn) clusterName() string       { return i.Cluster }

// proxyTool resolves the cluster and adapts a gateway call into an SDK tool
// handler. A returned error becomes an MCP tool error (IsError), which is
// exactly the spec's mapping for unknown clusters and upstream failures.
func proxyTool[In clusterCarrier](g *gateway.Gateway, call func(ctx context.Context, cl gateway.Client, in In) (json.RawMessage, error)) sdkmcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *sdkmcp.CallToolRequest, in In) (*sdkmcp.CallToolResult, any, error) {
		cl, ok := g.Get(in.clusterName())
		if !ok {
			return nil, nil, gateway.UnknownCluster(in.clusterName())
		}
		raw, err := call(ctx, cl, in)
		if err != nil {
			return nil, nil, gateway.Normalize(in.clusterName(), err)
		}
		return textResult(raw), nil, nil
	}
}

func textResult(raw []byte) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}},
	}
}

// nonNil normalizes a nil map to empty so kato receives {"inputs":{}} not {"inputs":null}.
func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test -race ./internal/mcp/ -v`
Expected: PASS (all seven test functions). If `ListTools`'s params type or the `Connect` signatures differ from the sketch, check with `go doc github.com/modelcontextprotocol/go-sdk/mcp ClientSession` / `Client.Connect` and adapt the TEST code mechanically (the implementation surface above is go-doc-verified; note any test adaptation in your report).

- [ ] **Step 6: gofmt, tidy, and stage**

```bash
GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go mod tidy
GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/gofmt -l internal/mcp/
```
gofmt must print nothing; re-run the package tests after `go mod tidy` if it changed anything.

```bash
git add go.mod go.sum internal/mcp/
```
(Stage only — do not commit.)

---

## Task 5: Config, main wiring, chart, docs, full verification

**Files:**
- Modify: `internal/config/config.go` (+ `APIAddr`), `internal/config/config_test.go` (append)
- Modify: `cmd/kato-bot/main.go` (gateway build + API listener)
- Modify: `charts/kato-bot/values.yaml`, `charts/kato-bot/templates/deployment.yaml`, `charts/kato-bot/README.md.gotmpl`
- Create: `charts/kato-bot/templates/service.yaml`
- Regenerate: `README.md`, `charts/kato-bot/README.md` (via `make readme` / helm-docs 1.14.2)

**Interfaces:**
- Consumes: `gateway.New/Add` (Task 2), `api.Register` (Task 3), `mcp.NewServer/Handler` (Task 4).
- Produces: env `API_ADDR` (default `:9090`, empty = disabled); chart value `api.enabled` (default `true`).

- [ ] **Step 1: Write the failing config test**

Append to `internal/config/config_test.go` (match the file's existing helpers/style — read it first; if it sets required env vars via a helper, reuse that helper):

```go
// API_ADDR: default :9090; explicitly empty disables the API listener.
func TestAPIAddr(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		setRequiredEnv(t) // reuse/create the file's existing helper that sets LARK_APP_ID, LARK_APP_SECRET, KATO_CLUSTERS_FILE
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.APIAddr != ":9090" {
			t.Errorf("APIAddr = %q, want :9090", cfg.APIAddr)
		}
	})
	t.Run("explicit empty disables", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("API_ADDR", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.APIAddr != "" {
			t.Errorf("APIAddr = %q, want empty (disabled)", cfg.APIAddr)
		}
	})
	t.Run("override", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("API_ADDR", ":7777")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.APIAddr != ":7777" {
			t.Errorf("APIAddr = %q, want :7777", cfg.APIAddr)
		}
	})
}
```

If no `setRequiredEnv`-style helper exists, write one in the test file: it must `t.Setenv` `LARK_APP_ID`/`LARK_APP_SECRET` to dummies and point `KATO_CLUSTERS_FILE` at a temp file containing `clusters: [{name: default, url: "http://x"}]` (use `t.TempDir()` + `os.WriteFile`).

**CAUTION on `t.Setenv("API_ADDR", "")`:** Go's `t.Setenv` with an empty value SETS the variable to empty (it does not unset), which is exactly the "explicitly empty" case — good. The default subtest must ensure the var is NOT set; if the test binary environment might carry it, add `os.Unsetenv("API_ADDR")` at the top of the default subtest (with a `t.Setenv` first if you need automatic restore, or simply document it).

- [ ] **Step 2: Implement config**

In `internal/config/config.go`: add `APIAddr string` to `Config` (comment: `// APIAddr is the MCP + REST proxy listen address; empty disables the listener.`), and in `Load()`:

```go
	// API_ADDR: default :9090; an explicitly empty value disables the listener
	// (envOr cannot express that, hence LookupEnv).
	if v, ok := os.LookupEnv("API_ADDR"); ok {
		cfg.APIAddr = v
	} else {
		cfg.APIAddr = ":9090"
	}
```

Run: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go test -race ./internal/config/ -v`
Expected: PASS (new + existing).

- [ ] **Step 3: Wire main.go**

In `cmd/kato-bot/main.go`, extend the cluster loop and start the API server. The current loop:

```go
	registry := core.NewRegistry()
	names := make([]string, 0, len(cfg.Clusters))
	for _, cl := range cfg.Clusters {
		registry.Add(core.Cluster{Name: cl.Name, Label: cl.Label}, kato.New(cl.URL, cfg.KatoRunTimeout, cl.InsecureSkipVerify))
		names = append(names, cl.Name)
	}
```

becomes (one client instance registered in BOTH registries):

```go
	registry := core.NewRegistry()
	gw := gateway.New()
	names := make([]string, 0, len(cfg.Clusters))
	for _, cl := range cfg.Clusters {
		kc := kato.New(cl.URL, cfg.KatoRunTimeout, cl.InsecureSkipVerify)
		c := core.Cluster{Name: cl.Name, Label: cl.Label}
		registry.Add(c, kc)
		gw.Add(c, kc)
		names = append(names, cl.Name)
	}
```

After the health-server goroutine, add the API listener (same fatal-on-bind pattern and rationale as the health server):

```go
	// MCP + REST proxy listener (API_ADDR; empty = disabled). Serves the MCP
	// streamable-HTTP endpoint at /mcp and the cluster-prefixed REST proxy.
	if cfg.APIAddr != "" {
		apiMux := http.NewServeMux()
		apiMux.Handle("/mcp", mcpserver.Handler(mcpserver.NewServer(gw)))
		api.Register(apiMux, gw)
		go func() {
			log.Printf("api server (mcp + rest proxy) on %s", cfg.APIAddr)
			if err := http.ListenAndServe(cfg.APIAddr, apiMux); err != nil {
				log.Fatalf("api server on %s: %v", cfg.APIAddr, err)
			}
		}()
	}
```

Imports to add:

```go
	"github.com/zufardhiyaulhaq/kato-bot/internal/api"
	"github.com/zufardhiyaulhaq/kato-bot/internal/gateway"
	mcpserver "github.com/zufardhiyaulhaq/kato-bot/internal/mcp"
```

Build: `GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go /Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go/bin/go build ./...`
Expected: clean.

- [ ] **Step 4: Chart — values, deployment, Service**

`charts/kato-bot/values.yaml` — add (helm-docs comment style `# --` like the file's existing entries):

```yaml
api:
  # -- Enable the MCP + REST proxy listener on port 9090 (MCP at /mcp,
  # cluster-prefixed kato REST proxy at /api/v1/clusters/...). No auth:
  # anyone who can reach the port can run kato on every configured cluster —
  # keep it ClusterIP / network-restricted.
  enabled: true
```

`charts/kato-bot/templates/deployment.yaml` — in the `env:` list add:

```yaml
            - name: API_ADDR
              value: {{ .Values.api.enabled | ternary ":9090" "" | quote }}
```

and in the container `ports:` list (after the health port), add the api port only when enabled:

```yaml
            {{- if .Values.api.enabled }}
            - name: api
              containerPort: 9090
            {{- end }}
```

Create `charts/kato-bot/templates/service.yaml` (kato-bot's first Service — gated):

```yaml
{{- if .Values.api.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "kato-bot.name" . }}
  labels:
    {{- include "kato-bot.labels" . | nindent 4 }}
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: {{ include "kato-bot.name" . }}
    app.kubernetes.io/instance: {{ .Release.Name }}
  ports:
    - name: api
      port: 9090
      targetPort: api
{{- end }}
```

Check the actual helper names first (`grep -n "define" charts/kato-bot/templates/_helpers.tpl`) and the deployment's `selector.matchLabels` — the Service selector must match the pod labels exactly as the Deployment's selector does; copy that selector block.

Lint: `helm template charts/kato-bot --set lark.appId=x --set lark.appSecret=y > /dev/null && echo TEMPLATE-OK` and the same with `--set api.enabled=false` (Service must not render: add `| grep -c "kind: Service"` expecting 0).

- [ ] **Step 5: Docs — README.md.gotmpl + regenerate**

In `charts/kato-bot/README.md.gotmpl`:
1. In the env-var table (the one with `LARK_APP_ID` etc.), add after `HEALTH_ADDR`:
```
| `API_ADDR` | `:9090` | MCP + REST proxy listen address (`/mcp` + `/api/v1/clusters/...`); empty string disables |
```
2. After the "How it works" section, add a short section:

```markdown
## MCP server + REST proxy

kato-bot also exposes its multi-cluster aggregation programmatically on port
9090 (value `api.enabled`, default on):

- **MCP** (streamable HTTP) at `/mcp` — tools: `list_clusters`,
  `list_usecases`, `get_usecase`, `run_usecase`, `list_methods`, `run_method`,
  `list_runs`, `get_run`. Every tool takes `cluster` (from `list_clusters`).
- **REST proxy**: kato's API prefixed by cluster —
  `GET /api/v1/clusters`, then `/api/v1/clusters/{cluster}/usecases[...]`,
  `/methods[...]`, `/runs[...]` — requests and responses pass through verbatim.

No auth (same stance as kato): network reach is the boundary. Local use:

```console
kubectl -n kato port-forward svc/kato-bot 9090
claude mcp add --transport http kato http://localhost:9090/mcp
```

`run_method` needs kato ≥ the version that ships `POST /api/v1/methods/{name}/run`;
against older katos the tool surfaces kato's 404 verbatim.
```

Regenerate: `make readme` (helm-docs 1.14.2 per `.tool-versions`; the Makefile's `readme` target). Both `README.md` and `charts/kato-bot/README.md` must regenerate. If helm-docs is unavailable, hand-edit both generated READMEs identically and say so in the report.

- [ ] **Step 6: Full verification**

```bash
export GOROOT=/Users/zufardhiyaulhaq/.asdf/installs/golang/1.24.8/go
$GOROOT/bin/go build ./...
$GOROOT/bin/go test -race -count=1 ./...
$GOROOT/bin/go vet ./...
$GOROOT/bin/gofmt -l internal/ cmd/
helm template charts/kato-bot --set lark.appId=x --set lark.appSecret=y > /dev/null && echo TEMPLATE-OK
```
Expected: build clean; ALL packages ok (including untouched core/lark — proves no cross-package breakage); vet clean; gofmt prints nothing; TEMPLATE-OK.

- [ ] **Step 7: Stage**

```bash
git add internal/config/ cmd/kato-bot/main.go charts/kato-bot/ README.md
```
(Stage only — do not commit. Present the staged set to the user.)

---

## Self-Review notes (for the executor)

- **Spec coverage:** raw verbatim client (Task 1) ← spec "client grows full API coverage" + "never reshapes"; gateway with spec-exact error mapping and cluster listing (Task 2); REST routes incl. `GET /api/v1/clusters` (Task 3); 8 spec-named MCP tools with operational descriptions (Task 4); `API_ADDR`/chart Service/`api.enabled`/README (Task 5). Non-goals need no code: no auth, no stdio, no bot-side limiter, no session state, no core/lark changes.
- **Type consistency:** the 7 `Raw*` signatures are identical in Task 1 (methods), Task 2 (`gateway.Client`), and both fakes (Tasks 3–4). `gateway.Error/Normalize/UnknownCluster` names match across Tasks 2–4. `api.Register(mux, g)` and `mcpserver.NewServer(g)`/`Handler(s)` match Task 5's main.go.
- **Watch items:** (1) the two fakes (api/mcp tests) are intentionally duplicated per package — do NOT extract a shared test helper package for them. (2) SDK signatures for `NewServer`/`AddTool`/`ToolHandlerFor`/`NewStreamableHTTPHandler`/`NewInMemoryTransports`/`Connect`/`CallTool` were verified against v1.6.1 `go doc`; test-side calls (`ListTools`, `CallToolParams`) may need mechanical adaptation — adapt tests, not the tool surface. (3) jsonschema required-field semantics: verify `omitempty` marks fields optional; adjust input structs if needed so `cluster`/`usecase`/`method`/`run` are required. (4) `t.Setenv("API_ADDR", "")` sets-empty (correct for the disable case); ensure the default subtest runs without the var present.
{% endraw %}
