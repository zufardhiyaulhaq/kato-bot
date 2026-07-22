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
