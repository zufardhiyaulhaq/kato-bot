# Multi-cluster (multitenancy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one central kato-bot orchestrate multiple Kubernetes clusters — the user picks a cluster first, then a UseCase, against a configured list of clusters (name → kato URL).

**Architecture:** Replace Core's single `Kato KatoClient` with a `Registry` of clients keyed by cluster name. The chosen cluster rides inside the existing `core.Reply` struct (threaded through every intent and `Render*` call) and inside each card button's `value`. A new cluster-picker step (`ListClusters` → `PickCluster`) precedes the existing use-case flow. Clusters are loaded from a YAML file (Helm ConfigMap).

**Tech Stack:** Go 1.24, larksuite/oapi-sdk-go v3, `gopkg.in/yaml.v3` (new), Helm.

**Spec:** `docs/superpowers/specs/2026-06-17-multitenancy-design.md`

---

## ⚠️ Standing constraints for the executor

1. **DO NOT COMMIT.** The user's standing instruction is "don't commit". Never run `git add` or `git commit`. Leave all changes in the working tree. Each task therefore ends with a **Verify** step (run the tests), **not** a commit step.
2. **Build goes temporarily red mid-sequence.** Tasks 2–4 change `internal/core` before its consumers (`internal/platform/lark`, `cmd/kato-bot`) are updated, so a whole-module `go build ./...` will FAIL between Task 2 and Task 5. This is expected. Each task is verified with its **own package's** test command, which builds only that package. The full module returns to green at Task 5.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/core/registry.go` | `Cluster` type + `Registry` (name → KatoClient) | **create** |
| `internal/core/registry_test.go` | Registry unit tests | **create** |
| `internal/core/types.go` | `Reply.Cluster`, new intents, `Renderer.RenderClusterPicker`, `Core.Clusters` | modify |
| `internal/core/core.go` | state machine: cluster step + per-cluster client resolution | modify |
| `internal/core/core_test.go` | tests for the multi-cluster flow | modify |
| `internal/platform/lark/cards.go` | cluster-picker card + thread cluster into values | modify |
| `internal/platform/lark/render.go` | `RenderClusterPicker` + pass `r.Cluster` to builders | modify |
| `internal/platform/lark/cardaction.go` | capture `RenderClusterPicker`; transient Core uses `Clusters` | modify |
| `internal/platform/lark/decode.go` | extract `cluster`; `pick_cluster` action; message → `ListClusters` | modify |
| `internal/platform/lark/*_test.go` | adapter tests for the new shapes | modify |
| `internal/config/config.go` | load clusters YAML file; remove `KatoBaseURL` | modify |
| `internal/config/config_test.go` | config tests for clusters file | modify |
| `cmd/kato-bot/main.go` | build the registry from config | modify |
| `charts/kato-bot/values.yaml` | `clusters:` list; remove `katoBaseUrl` | modify |
| `charts/kato-bot/templates/clusters-configmap.yaml` | render `clusters.yaml` | **create** |
| `charts/kato-bot/templates/deployment.yaml` | mount ConfigMap; env; checksum | modify |
| `charts/kato-bot/README.md.gotmpl` + `README.md` | docs | modify |

---

## Task 1: Core `Registry` + `Cluster` type (additive)

**Files:**
- Create: `internal/core/registry.go`
- Test: `internal/core/registry_test.go`

This task is purely additive — the whole module still compiles and all existing tests pass.

- [ ] **Step 1: Write the failing test**

Create `internal/core/registry_test.go`:

```go
package core

import (
	"context"
	"testing"
)

// stubKato is a no-op KatoClient used to identify which client the registry returns.
type stubKato struct{ id string }

func (s stubKato) ListUseCases(context.Context) ([]UseCase, error)        { return nil, nil }
func (s stubKato) GetUseCase(context.Context, string) (Contract, error)   { return Contract{}, nil }
func (s stubKato) Run(context.Context, string, map[string]string) (RunResult, error) {
	return RunResult{}, nil
}

func TestRegistryListPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod", Label: "Production"}, stubKato{"p"})
	r.Add(Cluster{Name: "staging"}, stubKato{"s"})

	got := r.List()
	if len(got) != 2 || got[0].Name != "prod" || got[1].Name != "staging" {
		t.Fatalf("list order = %+v", got)
	}
	if got[0].Label != "Production" {
		t.Fatalf("label = %q", got[0].Label)
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod"}, stubKato{"p"})

	c, ok := r.Get("prod")
	if !ok {
		t.Fatal("prod should be present")
	}
	if c.(stubKato).id != "p" {
		t.Fatalf("got client %+v", c)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("nope should be absent")
	}
}

func TestRegistryListReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Add(Cluster{Name: "prod"}, stubKato{"p"})
	r.List()[0].Name = "mutated"
	if r.List()[0].Name != "prod" {
		t.Fatal("List() must return a copy, not the internal slice")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestRegistry -v`
Expected: FAIL — `undefined: NewRegistry`, `undefined: Cluster`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/core/registry.go`:

```go
package core

// Cluster identifies one kato backend the bot can target. Label is the human-facing
// button text shown in the cluster picker; it defaults to Name when empty.
type Cluster struct {
	Name  string
	Label string
}

// Registry resolves a KatoClient by cluster name. It is built once at startup from
// configuration and read-only thereafter (safe for concurrent reads).
type Registry struct {
	order   []Cluster
	clients map[string]KatoClient
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{clients: map[string]KatoClient{}}
}

// Add registers a cluster and its client, preserving insertion order. A duplicate name
// overwrites the previous client without duplicating the ordered entry.
func (r *Registry) Add(c Cluster, client KatoClient) {
	if _, exists := r.clients[c.Name]; !exists {
		r.order = append(r.order, c)
	}
	r.clients[c.Name] = client
}

// List returns the registered clusters in insertion order (a copy; safe to mutate).
func (r *Registry) List() []Cluster {
	out := make([]Cluster, len(r.order))
	copy(out, r.order)
	return out
}

// Get resolves the client for a cluster name.
func (r *Registry) Get(name string) (KatoClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestRegistry -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Verify the whole package still builds and passes**

Run: `go test ./internal/core/`
Expected: PASS (existing tests unaffected — this task is additive). **Do not commit.**

---

## Task 2: Core multi-cluster state machine

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/core/core.go`
- Modify: `internal/core/core_test.go`

> After this task, `go test ./internal/core/` passes but `go build ./...` FAILS (the lark + cmd packages still reference the removed `ListUseCases`/`Core.Kato`). That is fixed in Tasks 3 and 5.

- [ ] **Step 1: Rewrite the core test file to the multi-cluster flow**

Replace the entire contents of `internal/core/core_test.go` with:

```go
package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- fakes ---

type fakeKato struct {
	ucs      []UseCase
	contract Contract
	runRes   RunResult
	runErr   error
	listErr  error
	getErr   error
	runCalls int
}

func (f *fakeKato) ListUseCases(context.Context) ([]UseCase, error) { return f.ucs, f.listErr }
func (f *fakeKato) GetUseCase(context.Context, string) (Contract, error) {
	return f.contract, f.getErr
}
func (f *fakeKato) Run(context.Context, string, map[string]string) (RunResult, error) {
	f.runCalls++
	return f.runRes, f.runErr
}

// regWith builds a single-cluster registry named `name` backed by k.
func regWith(name string, k KatoClient) *Registry {
	r := NewRegistry()
	r.Add(Cluster{Name: name}, k)
	return r
}

type call struct {
	kind     string
	reply    Reply
	clusters []Cluster
	ucs      []UseCase
	c        Contract
	useCase  string
	inputs   map[string]string
	res      RunResult
	msg      string
	prefill  map[string]string
	formErr  string
}

type fakeRenderer struct{ calls []call }

func (r *fakeRenderer) RenderClusterPicker(_ context.Context, rep Reply, clusters []Cluster) error {
	r.calls = append(r.calls, call{kind: "clusterpicker", reply: rep, clusters: clusters})
	return nil
}
func (r *fakeRenderer) RenderPicker(_ context.Context, rep Reply, ucs []UseCase) error {
	r.calls = append(r.calls, call{kind: "picker", reply: rep, ucs: ucs})
	return nil
}
func (r *fakeRenderer) RenderForm(_ context.Context, rep Reply, c Contract, prefill map[string]string, formErr string) error {
	r.calls = append(r.calls, call{kind: "form", reply: rep, c: c, prefill: prefill, formErr: formErr})
	return nil
}
func (r *fakeRenderer) RenderRunning(_ context.Context, rep Reply, uc string, in map[string]string) error {
	r.calls = append(r.calls, call{kind: "running", reply: rep, useCase: uc, inputs: in})
	return nil
}
func (r *fakeRenderer) RenderResult(_ context.Context, rep Reply, uc string, in map[string]string, res RunResult) error {
	r.calls = append(r.calls, call{kind: "result", reply: rep, useCase: uc, inputs: in, res: res})
	return nil
}
func (r *fakeRenderer) RenderError(_ context.Context, rep Reply, msg string) error {
	r.calls = append(r.calls, call{kind: "error", reply: rep, msg: msg})
	return nil
}

func (r *fakeRenderer) kinds() []string {
	var k []string
	for _, c := range r.calls {
		k = append(k, c.kind)
	}
	return k
}

// --- tests ---

func TestHandleListClusters(t *testing.T) {
	reg := NewRegistry()
	reg.Add(Cluster{Name: "prod", Label: "Production"}, &fakeKato{})
	reg.Add(Cluster{Name: "staging"}, &fakeKato{})
	r := &fakeRenderer{}
	c := &Core{Clusters: reg, R: r}

	deferred, err := c.Handle(context.Background(), ListClusters{Reply: Reply{ChatID: "ch", InReplyTo: "m1"}})
	if err != nil || deferred != nil {
		t.Fatalf("deferred=non-nil err=%v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "clusterpicker" {
		t.Fatalf("calls = %v", r.kinds())
	}
	if len(r.calls[0].clusters) != 2 || r.calls[0].clusters[0].Name != "prod" {
		t.Fatalf("clusterpicker call = %+v", r.calls[0])
	}
}

func TestHandlePickClusterListsUseCases(t *testing.T) {
	k := &fakeKato{ucs: []UseCase{{Name: "a"}, {Name: "b"}}}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(),
		PickCluster{Reply: Reply{ChatID: "ch", MessageID: "card1", Cluster: "prod"}})
	if err != nil || deferred != nil {
		t.Fatalf("deferred=non-nil err=%v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "picker" {
		t.Fatalf("calls = %v", r.kinds())
	}
	if len(r.calls[0].ucs) != 2 || r.calls[0].reply.Cluster != "prod" {
		t.Fatalf("picker call = %+v", r.calls[0])
	}
}

func TestHandleUnknownClusterRendersError(t *testing.T) {
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", &fakeKato{}), R: r}

	_, err := c.Handle(context.Background(), PickCluster{Reply: Reply{Cluster: "ghost"}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "error" {
		t.Fatalf("calls = %v", r.kinds())
	}
	if !strings.Contains(r.calls[0].msg, "ghost") {
		t.Fatalf("error msg = %q", r.calls[0].msg)
	}
}

func TestHandlePickClusterKatoDown(t *testing.T) {
	k := &fakeKato{listErr: errors.New("connection refused")}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	_, err := c.Handle(context.Background(), PickCluster{Reply: Reply{Cluster: "prod"}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "error" {
		t.Fatalf("calls = %v", r.kinds())
	}
}

func TestHandlePickUseCase(t *testing.T) {
	k := &fakeKato{contract: Contract{Name: "pod-crashloop", Inputs: []InputDecl{{Name: "ns", Required: true}}}}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(),
		PickUseCase{Reply: Reply{ChatID: "ch", MessageID: "card1", Cluster: "prod"}, Name: "pod-crashloop"})
	if err != nil || deferred != nil {
		t.Fatalf("deferred=non-nil err=%v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "form" {
		t.Fatalf("calls = %v", r.kinds())
	}
	if r.calls[0].c.Name != "pod-crashloop" || r.calls[0].formErr != "" || r.calls[0].prefill != nil {
		t.Fatalf("form call = %+v", r.calls[0])
	}
}

func TestHandlePickUseCaseUnknownCluster(t *testing.T) {
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", &fakeKato{}), R: r}

	_, _ = c.Handle(context.Background(), PickUseCase{Reply: Reply{Cluster: "ghost"}, Name: "x"})
	if len(r.calls) != 1 || r.calls[0].kind != "error" {
		t.Fatalf("calls = %v", r.kinds())
	}
}

func TestHandleSubmitFormMissingRequired(t *testing.T) {
	k := &fakeKato{contract: Contract{Name: "uc", Inputs: []InputDecl{
		{Name: "namespace", Required: true}, {Name: "pod", Required: true},
	}}}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(), SubmitForm{
		Reply: Reply{MessageID: "card1", Cluster: "prod"}, Name: "uc",
		Inputs: map[string]string{"namespace": "payments", "pod": "  "}, // pod blank
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if deferred != nil {
		t.Fatal("must NOT run when required inputs missing")
	}
	if k.runCalls != 0 {
		t.Fatalf("kato.Run called %d times", k.runCalls)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "form" {
		t.Fatalf("calls = %v", r.kinds())
	}
	if r.calls[0].formErr == "" || r.calls[0].prefill["namespace"] != "payments" {
		t.Fatalf("expected form error + prefill, got %+v", r.calls[0])
	}
}

func TestHandleSubmitFormRunsAndRenders(t *testing.T) {
	k := &fakeKato{
		contract: Contract{Name: "uc", Inputs: []InputDecl{{Name: "namespace", Required: true}}},
		runRes:   RunResult{Run: "uc-abc", Phase: "Completed", Summary: "ok"},
	}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(), SubmitForm{
		Reply: Reply{MessageID: "card1", Cluster: "prod"}, Name: "uc",
		Inputs: map[string]string{"namespace": "payments"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].kind != "running" {
		t.Fatalf("after Handle, calls = %v (want [running])", r.kinds())
	}
	if deferred == nil {
		t.Fatal("expected a deferred run thunk")
	}
	if err := deferred(context.Background()); err != nil {
		t.Fatalf("deferred err = %v", err)
	}
	if k.runCalls != 1 {
		t.Fatalf("kato.Run called %d times", k.runCalls)
	}
	if len(r.calls) != 2 || r.calls[1].kind != "result" {
		t.Fatalf("calls = %v (want [running result])", r.kinds())
	}
	if r.calls[1].res.Summary != "ok" || r.calls[1].res.Run != "uc-abc" {
		t.Fatalf("result = %+v", r.calls[1].res)
	}
}

func TestHandleSubmitFormRunErrorBecomesResult(t *testing.T) {
	k := &fakeKato{
		contract: Contract{Name: "uc", Inputs: []InputDecl{{Name: "x", Required: false}}},
		runErr:   errors.New("connection refused"),
	}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(), SubmitForm{
		Reply: Reply{MessageID: "c", Cluster: "prod"}, Name: "uc", Inputs: map[string]string{},
	})
	if err != nil || deferred == nil {
		t.Fatalf("deferred=nil err=%v", err)
	}
	if err := deferred(context.Background()); err != nil {
		t.Fatalf("deferred err = %v", err)
	}
	last := r.calls[len(r.calls)-1]
	if last.kind != "result" || last.res.Err == nil {
		t.Fatalf("expected result with Err set, got %+v", last)
	}
}

// fakeStatusErr implements HTTPStatusError for testing the friendly mapping.
type fakeStatusErr struct {
	status int
	detail string
}

func (e *fakeStatusErr) Error() string   { return "kato error" }
func (e *fakeStatusErr) HTTPStatus() int { return e.status }
func (e *fakeStatusErr) Detail() string  { return e.detail }

func TestFriendlyKatoError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"400", &fakeStatusErr{400, "input pod is required"}, "invalid inputs: input pod is required"},
		{"404", &fakeStatusErr{404, "x"}, "use case not found in the cluster"},
		{"422", &fakeStatusErr{422, "x"}, "this use case failed validation in the cluster"},
		{"429", &fakeStatusErr{429, "x"}, "kato is busy (too many concurrent runs) — try again shortly"},
		{"500", &fakeStatusErr{500, "x"}, "kato had an internal error — try again shortly"},
		{"transport", errors.New("connection refused"), "couldn't reach kato: connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := friendlyKatoError(tc.err); got != tc.want {
				t.Errorf("friendlyKatoError = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleSubmitFormRun400GoesBackToForm(t *testing.T) {
	k := &fakeKato{
		contract: Contract{Name: "uc", Inputs: []InputDecl{{Name: "pod", Required: true}}},
		runErr:   &fakeStatusErr{400, "pod 'x' not found"},
	}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, err := c.Handle(context.Background(), SubmitForm{
		Reply: Reply{MessageID: "card1", Cluster: "prod"}, Name: "uc", Inputs: map[string]string{"pod": "x"},
	})
	if err != nil || deferred == nil {
		t.Fatalf("deferred=nil err=%v", err)
	}
	if err := deferred(context.Background()); err != nil {
		t.Fatalf("deferred err = %v", err)
	}
	last := r.calls[len(r.calls)-1]
	if last.kind != "form" {
		t.Fatalf("expected form on 400, got calls = %v", r.kinds())
	}
	if last.prefill["pod"] != "x" || last.formErr == "" {
		t.Fatalf("form should be prefilled with an error, got %+v", last)
	}
}

func TestHandleSubmitFormRun429IsFriendlyResult(t *testing.T) {
	k := &fakeKato{
		contract: Contract{Name: "uc", Inputs: []InputDecl{{Name: "x", Required: false}}},
		runErr:   &fakeStatusErr{429, "too many concurrent runs"},
	}
	r := &fakeRenderer{}
	c := &Core{Clusters: regWith("prod", k), R: r}

	deferred, _ := c.Handle(context.Background(), SubmitForm{
		Reply: Reply{MessageID: "c", Cluster: "prod"}, Name: "uc", Inputs: map[string]string{},
	})
	if deferred == nil {
		t.Fatal("expected deferred")
	}
	if err := deferred(context.Background()); err != nil {
		t.Fatalf("deferred err = %v", err)
	}
	last := r.calls[len(r.calls)-1]
	if last.kind != "result" || last.res.Err == nil {
		t.Fatalf("expected result with Err, got %+v", last)
	}
	if msg := last.res.Err.Error(); strings.Contains(msg, "kato 429") || !strings.Contains(msg, "busy") {
		t.Fatalf("expected friendly busy message, got %q", msg)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `go test ./internal/core/`
Expected: FAIL — compile errors (`undefined: ListClusters`, `undefined: PickCluster`, `Reply has no field Cluster`, `Core has no field Clusters`, `RenderClusterPicker` not in interface).

- [ ] **Step 3: Update `types.go`**

In `internal/core/types.go`:

(a) Add `Cluster string` to `Reply` (the `Cluster` *type* already lives in `registry.go`):

```go
type Reply struct {
	ChatID    string
	MessageID string // the bot card's own id, for patching; empty before the first card
	InReplyTo string // user's message id to reply to (set only for the initial picker)
	Cluster   string // selected cluster name; empty for the initial cluster-picker step
}
```

(b) Change the `Core` field — replace `Kato KatoClient` usage by giving the renderer a registry. Add `RenderClusterPicker` to the `Renderer` interface:

```go
// Renderer is the outbound port: turn semantic state into platform cards.
type Renderer interface {
	RenderClusterPicker(ctx context.Context, r Reply, clusters []Cluster) error
	RenderPicker(ctx context.Context, r Reply, ucs []UseCase) error
	RenderForm(ctx context.Context, r Reply, c Contract, prefill map[string]string, formErr string) error
	RenderRunning(ctx context.Context, r Reply, useCase string, inputs map[string]string) error
	RenderResult(ctx context.Context, r Reply, useCase string, inputs map[string]string, res RunResult) error
	RenderError(ctx context.Context, r Reply, msg string) error
}
```

(c) Replace the intents block. Remove `ListUseCases`; add `ListClusters` and `PickCluster`:

```go
type ListClusters struct{ Reply Reply }
type PickCluster struct{ Reply Reply } // selected cluster is in Reply.Cluster
type PickUseCase struct {
	Reply Reply
	Name  string
}
type SubmitForm struct {
	Reply  Reply
	Name   string
	Inputs map[string]string
}

func (ListClusters) isIntent() {}
func (PickCluster) isIntent()  {}
func (PickUseCase) isIntent()  {}
func (SubmitForm) isIntent()   {}
```

- [ ] **Step 4: Update `core.go` — `Core` struct + `Handle`**

In `internal/core/core.go`, change the `Core` struct:

```go
type Core struct {
	Clusters *Registry
	R        Renderer
}
```

Replace the entire `Handle` method body's switch with the multi-cluster version, and add the `unknownClusterMsg` helper. The full `Handle` + helper:

```go
func (c *Core) Handle(ctx context.Context, in Intent) (deferred func(context.Context) error, err error) {
	switch v := in.(type) {
	case ListClusters:
		return nil, c.R.RenderClusterPicker(ctx, v.Reply, c.Clusters.List())

	case PickCluster:
		kc, ok := c.Clusters.Get(v.Reply.Cluster)
		if !ok {
			return nil, c.R.RenderError(ctx, v.Reply, unknownClusterMsg(v.Reply.Cluster))
		}
		ucs, e := kc.ListUseCases(ctx)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		return nil, c.R.RenderPicker(ctx, v.Reply, ucs)

	case PickUseCase:
		kc, ok := c.Clusters.Get(v.Reply.Cluster)
		if !ok {
			return nil, c.R.RenderError(ctx, v.Reply, unknownClusterMsg(v.Reply.Cluster))
		}
		ct, e := kc.GetUseCase(ctx, v.Name)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		return nil, c.R.RenderForm(ctx, v.Reply, ct, nil, "")

	case SubmitForm:
		kc, ok := c.Clusters.Get(v.Reply.Cluster)
		if !ok {
			return nil, c.R.RenderError(ctx, v.Reply, unknownClusterMsg(v.Reply.Cluster))
		}
		ct, e := kc.GetUseCase(ctx, v.Name)
		if e != nil {
			return nil, c.R.RenderError(ctx, v.Reply, friendlyKatoError(e))
		}
		if missing := missingRequired(ct, v.Inputs); len(missing) > 0 {
			return nil, c.R.RenderForm(ctx, v.Reply, ct, v.Inputs,
				"required: "+strings.Join(missing, ", "))
		}
		if e := c.R.RenderRunning(ctx, v.Reply, v.Name, v.Inputs); e != nil {
			return nil, e
		}
		reply, name, inputs, contract := v.Reply, v.Name, v.Inputs, ct
		return func(dctx context.Context) error {
			res, runErr := kc.Run(dctx, name, inputs)
			if runErr != nil {
				var se HTTPStatusError
				if errors.As(runErr, &se) && se.HTTPStatus() == 400 {
					return c.R.RenderForm(dctx, reply, contract, inputs, friendlyKatoError(runErr))
				}
				res = RunResult{Err: &RunError{Msg: friendlyKatoError(runErr)}}
			}
			return c.R.RenderResult(dctx, reply, name, inputs, res)
		}, nil

	default:
		return nil, fmt.Errorf("unknown intent %T", in)
	}
}

// unknownClusterMsg is the friendly error when a card carries a cluster name that is no
// longer in the registry (e.g. a stale card after the clusters config changed).
func unknownClusterMsg(name string) string {
	if name == "" {
		return "no cluster selected — start over"
	}
	return "unknown cluster " + name + " — start over"
}
```

- [ ] **Step 5: Run the core tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS. (`go build ./...` will still fail at `lark`/`cmd` — expected, fixed in Tasks 3 & 5.) **Do not commit.**

---

## Task 3: Lark adapter — cluster card, threading, decode

**Files:**
- Modify: `internal/platform/lark/cards.go`
- Modify: `internal/platform/lark/render.go`
- Modify: `internal/platform/lark/cardaction.go`
- Modify: `internal/platform/lark/decode.go`
- Modify: `internal/platform/lark/cards_test.go`
- Modify: `internal/platform/lark/render_test.go`
- Modify: `internal/platform/lark/decode_test.go`
- Modify: `internal/platform/lark/dispatch_test.go`

- [ ] **Step 1: Update card builder tests**

In `internal/platform/lark/cards_test.go`:

(a) Add a cluster-picker test:

```go
func TestBuildClusterPickerCard(t *testing.T) {
	card := buildClusterPickerCard([]core.Cluster{
		{Name: "prod", Label: "Production"},
		{Name: "staging"},
	})
	m := asMap(t, card)
	if m["schema"] != "2.0" {
		t.Errorf("expected schema 2.0, got %v", m["schema"])
	}
	if !strings.Contains(card, "Production") {
		t.Error("missing cluster label")
	}
	if !strings.Contains(card, "staging") {
		t.Error("missing cluster name fallback")
	}
	if !strings.Contains(card, `"action":"pick_cluster"`) || !strings.Contains(card, `"cluster":"prod"`) {
		t.Error("missing pick_cluster action value")
	}
}
```

(b) Update `TestBuildPickerCard` — `buildPickerCard` now takes a cluster argument; assert the cluster is threaded into the pick value:

```go
func TestBuildPickerCard(t *testing.T) {
	card := buildPickerCard("prod", []core.UseCase{
		{Name: "pod-crashloop", Description: "Diagnose crashloop", Ready: true},
		{Name: "broken", Description: "x", Ready: false},
	})
	m := asMap(t, card)
	if m["schema"] != "2.0" {
		t.Errorf("expected card schema 2.0, got %v", m["schema"])
	}
	body, ok := m["body"].(map[string]any)
	if !ok || body["elements"] == nil {
		t.Fatal("no body.elements")
	}
	if !strings.Contains(card, "pod-crashloop") {
		t.Error("missing usecase name")
	}
	if !strings.Contains(card, `"action":"pick"`) || !strings.Contains(card, `"usecase":"pod-crashloop"`) {
		t.Error("missing pick action value")
	}
	if !strings.Contains(card, `"cluster":"prod"`) {
		t.Error("pick action must carry the cluster")
	}
}
```

(c) Update `TestBuildFormCard` — `buildFormCard` now takes a cluster argument; assert cluster in the run value:

```go
func TestBuildFormCard(t *testing.T) {
	c := core.Contract{Name: "pod-crashloop", Description: "d", Inputs: []core.InputDecl{
		{Name: "namespace", Required: true}, {Name: "pod", Required: true},
	}}
	card := buildFormCard("prod", c, map[string]string{"namespace": "payments"}, "required: pod")
	if !strings.Contains(card, "namespace") || !strings.Contains(card, "pod") {
		t.Error("missing input names")
	}
	if !strings.Contains(card, "payments") {
		t.Error("missing prefill value")
	}
	if !strings.Contains(card, "required: pod") {
		t.Error("missing form error text")
	}
	if !strings.Contains(card, `"action":"run"`) || !strings.Contains(card, `"usecase":"pod-crashloop"`) {
		t.Error("missing run action value")
	}
	if !strings.Contains(card, `"cluster":"prod"`) {
		t.Error("run action must carry the cluster")
	}
	asMap(t, card)
}
```

(d) Update the three `buildResultCard` tests — it now takes a cluster argument as the first parameter. Change each call:

```go
// TestBuildResultCardCompleted:
	card := buildResultCard("prod", "pod-crashloop", core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Completed", Summary: "It is OOMKilled.",
	})
```
```go
// TestBuildResultCardFailedPhase:
	card := buildResultCard("prod", "pod-crashloop", core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Failed", Summary: "step errored",
	})
```
```go
// TestBuildResultCardError:
	card := buildResultCard("prod", "uc", core.RunResult{Err: &core.RunError{Msg: "kato is busy"}})
```

In `TestBuildResultCardCompleted`, also assert the run-again carries the cluster — add after the existing `"action":"pick"` check:

```go
	if !strings.Contains(card, `"cluster":"prod"`) {
		t.Error("run-again action must carry the cluster")
	}
```

- [ ] **Step 2: Run card tests to verify they fail**

Run: `go test ./internal/platform/lark/ -run 'TestBuild' `
Expected: FAIL — compile errors (`buildClusterPickerCard` undefined; `buildPickerCard`/`buildFormCard`/`buildResultCard` argument count mismatch).

- [ ] **Step 3: Update `cards.go`**

In `internal/platform/lark/cards.go`:

(a) Add the cluster-picker builder (place it just before `buildPickerCard`):

```go
// buildClusterPickerCard lists each configured cluster with a Select button. The button
// value carries the cluster name so the follow-up pick_cluster callback knows which kato
// backend to target.
func buildClusterPickerCard(clusters []core.Cluster) string {
	elements := []any{markdown("☸️ **kato** — pick a cluster")}
	for _, cl := range clusters {
		label := cl.Label
		if label == "" {
			label = cl.Name
		}
		elements = append(elements, map[string]any{"tag": "hr"})
		elements = append(elements, markdown("**"+label+"**"))
		elements = append(elements, button2("Select ▸", map[string]any{"action": "pick_cluster", "cluster": cl.Name}))
	}
	return card2("kato", elements)
}
```

(b) Change `buildPickerCard` to accept and thread the cluster:

```go
func buildPickerCard(cluster string, ucs []core.UseCase) string {
	elements := []any{markdown("🔧 **kato** — pick a troubleshooting flow")}
	for _, uc := range ucs {
		elements = append(elements, map[string]any{"tag": "hr"})
		elements = append(elements, markdown(fmt.Sprintf("**%s**\n%s", uc.Name, uc.Description)))
		if uc.Ready {
			elements = append(elements, button2("Select ▸", map[string]any{"action": "pick", "cluster": cluster, "usecase": uc.Name}))
		} else {
			elements = append(elements, markdown("_not ready (failed validation in cluster)_"))
		}
	}
	return card2("kato", elements)
}
```

(c) Change `buildFormCard`'s signature and `runValue`:

```go
func buildFormCard(cluster string, c core.Contract, prefill map[string]string, formErr string) string {
	runValue := map[string]any{"action": "run", "cluster": cluster, "usecase": c.Name}
```
(The rest of `buildFormCard`'s body is unchanged.)

(d) Change `buildResultCard`'s signature and the run-again value:

```go
func buildResultCard(cluster, useCase string, res core.RunResult) string {
```
and the final button line:
```go
	elements = append(elements, button2("↻ Run again", map[string]any{"action": "pick", "cluster": cluster, "usecase": useCase}))
```
(The rest of `buildResultCard`'s body is unchanged.)

- [ ] **Step 4: Update `render.go` and `cardaction.go` to pass the cluster**

In `internal/platform/lark/render.go`, add `RenderClusterPicker` and pass `r.Cluster` into the builders:

```go
func (rd *Renderer) RenderClusterPicker(ctx context.Context, r core.Reply, clusters []core.Cluster) error {
	return rd.emit(ctx, r, buildClusterPickerCard(clusters))
}

func (rd *Renderer) RenderPicker(ctx context.Context, r core.Reply, ucs []core.UseCase) error {
	return rd.emit(ctx, r, buildPickerCard(r.Cluster, ucs))
}

func (rd *Renderer) RenderForm(ctx context.Context, r core.Reply, c core.Contract, prefill map[string]string, formErr string) error {
	return rd.emit(ctx, r, buildFormCard(r.Cluster, c, prefill, formErr))
}

func (rd *Renderer) RenderRunning(ctx context.Context, r core.Reply, useCase string, inputs map[string]string) error {
	return rd.emit(ctx, r, buildRunningCard(useCase, inputs))
}

func (rd *Renderer) RenderResult(ctx context.Context, r core.Reply, useCase string, inputs map[string]string, res core.RunResult) error {
	return rd.emit(ctx, r, buildResultCard(r.Cluster, useCase, res))
}
```
(`RenderError` is unchanged.)

In `internal/platform/lark/cardaction.go`:

(i) Add `RenderClusterPicker` to `captureRenderer` and thread `rep.Cluster` into the picker/form/result builders:

```go
func (r *captureRenderer) RenderClusterPicker(_ context.Context, _ core.Reply, clusters []core.Cluster) error {
	r.card = buildClusterPickerCard(clusters)
	return nil
}
func (r *captureRenderer) RenderPicker(_ context.Context, rep core.Reply, ucs []core.UseCase) error {
	r.card = buildPickerCard(rep.Cluster, ucs)
	return nil
}
func (r *captureRenderer) RenderForm(_ context.Context, rep core.Reply, c core.Contract, prefill map[string]string, formErr string) error {
	r.card = buildFormCard(rep.Cluster, c, prefill, formErr)
	return nil
}
func (r *captureRenderer) RenderRunning(_ context.Context, _ core.Reply, uc string, in map[string]string) error {
	r.card = buildRunningCard(uc, in)
	return nil
}
func (r *captureRenderer) RenderResult(_ context.Context, rep core.Reply, uc string, in map[string]string, res core.RunResult) error {
	r.card = buildResultCard(rep.Cluster, uc, res)
	return nil
}
```
(`RenderError` on `captureRenderer` is unchanged.)

(ii) In `handleCardAction`, build the transient Core from the registry instead of a single client:

```go
	tmp := &core.Core{Clusters: a.Core.Clusters, R: cap}
```

(iii) In `replyOf`, add the new intents:

```go
func replyOf(in core.Intent) core.Reply {
	switch v := in.(type) {
	case core.ListClusters:
		return v.Reply
	case core.PickCluster:
		return v.Reply
	case core.PickUseCase:
		return v.Reply
	case core.SubmitForm:
		return v.Reply
	}
	return core.Reply{}
}
```

- [ ] **Step 5: Update `decode.go`**

Replace the bodies of `decodeMessage` and `decodeCardAction` in `internal/platform/lark/decode.go`:

```go
// decodeMessage maps any received user message to a ListClusters intent (show the cluster
// picker). chatID is the message's chat; userMsgID is the message to reply to.
func decodeMessage(chatID, userMsgID string) core.Intent {
	return core.ListClusters{Reply: core.Reply{ChatID: chatID, InReplyTo: userMsgID}}
}
```

```go
// decodeCardAction parses a card.action.trigger payload into a PickCluster, PickUseCase,
// or SubmitForm. The selected cluster (when present) is carried in every action value and
// threaded into Reply.Cluster so the core can resolve the right kato backend.
func decodeCardAction(raw []byte) (core.Intent, error) {
	var p cardActionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode card action: %w", err)
	}
	cluster, _ := p.Action.Value["cluster"].(string)
	reply := core.Reply{ChatID: p.Context.OpenChatID, MessageID: p.Context.OpenMessageID, Cluster: cluster}
	action, _ := p.Action.Value["action"].(string)
	useCase, _ := p.Action.Value["usecase"].(string)
	switch action {
	case "pick_cluster":
		return core.PickCluster{Reply: reply}, nil
	case "pick":
		return core.PickUseCase{Reply: reply, Name: useCase}, nil
	case "run":
		inputs := p.Action.FormValue
		if inputs == nil {
			inputs = map[string]string{}
		}
		return core.SubmitForm{Reply: reply, Name: useCase, Inputs: inputs}, nil
	default:
		return nil, fmt.Errorf("unknown card action %q", action)
	}
}
```

- [ ] **Step 6: Update decode + render + dispatch tests**

(a) In `internal/platform/lark/decode_test.go`, replace `TestDecodeMessageAlwaysListsUseCases` and update the action tests:

```go
func TestDecodeMessageListsClusters(t *testing.T) {
	in := decodeMessage("oc_chat", "om_user_msg")
	lc, ok := in.(core.ListClusters)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if lc.Reply.ChatID != "oc_chat" || lc.Reply.InReplyTo != "om_user_msg" {
		t.Fatalf("reply = %+v", lc.Reply)
	}
}

func TestDecodeCardActionPickCluster(t *testing.T) {
	raw := []byte(`{
		"action": {"value": {"action":"pick_cluster","cluster":"prod"}},
		"context": {"open_chat_id":"oc_1","open_message_id":"om_card"}
	}`)
	in, err := decodeCardAction(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	pc, ok := in.(core.PickCluster)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if pc.Reply.Cluster != "prod" || pc.Reply.MessageID != "om_card" {
		t.Fatalf("pickcluster = %+v", pc)
	}
}

func TestDecodeCardActionPick(t *testing.T) {
	raw := []byte(`{
		"action": {"value": {"action":"pick","cluster":"prod","usecase":"pod-crashloop"}},
		"context": {"open_chat_id":"oc_1","open_message_id":"om_card"}
	}`)
	in, err := decodeCardAction(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	pick, ok := in.(core.PickUseCase)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if pick.Name != "pod-crashloop" || pick.Reply.Cluster != "prod" || pick.Reply.MessageID != "om_card" {
		t.Fatalf("pick = %+v", pick)
	}
}

func TestDecodeCardActionRun(t *testing.T) {
	raw := []byte(`{
		"action": {
			"value": {"action":"run","cluster":"prod","usecase":"pod-crashloop"},
			"form_value": {"namespace":"payments","pod":"api-xyz"}
		},
		"context": {"open_chat_id":"oc_1","open_message_id":"om_card"}
	}`)
	in, err := decodeCardAction(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	sub, ok := in.(core.SubmitForm)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if sub.Name != "pod-crashloop" || sub.Reply.Cluster != "prod" || sub.Inputs["namespace"] != "payments" {
		t.Fatalf("submit = %+v", sub)
	}
}
```
(`TestDecodeCardActionUnknown` is unchanged.)

(b) In `internal/platform/lark/render_test.go`, add a cluster-picker render test (place after `TestRenderPickerReplies`):

```go
func TestRenderClusterPickerReplies(t *testing.T) {
	f := &fakeSender{}
	r := &Renderer{S: f}
	err := r.RenderClusterPicker(context.Background(),
		core.Reply{ChatID: "oc", InReplyTo: "om_user"}, []core.Cluster{{Name: "prod"}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.replyToMsgID != "om_user" {
		t.Fatalf("reply to = %q", f.replyToMsgID)
	}
	if !strings.Contains(f.replyCard, `"action":"pick_cluster"`) {
		t.Errorf("cluster picker not sent: %s", f.replyCard)
	}
}
```

(c) In `internal/platform/lark/dispatch_test.go`, update `TestCardActionSemaphoreCapsRuns` to use a registry and a cluster-bearing reply:

```go
	a := &Adapter{
		Core:          &core.Core{Clusters: regOf("c1", bk), R: &captureRenderer{}},
		R:             &Renderer{S: &fakeSender{}},
		RunTimeout:    5 * time.Second,
		MaxConcurrent: 1,
	}
	submit := core.SubmitForm{Reply: core.Reply{MessageID: "card1", Cluster: "c1"}, Name: "uc", Inputs: map[string]string{}}
```

and add this helper at the bottom of `dispatch_test.go`:

```go
// regOf builds a one-cluster registry for tests.
func regOf(name string, k core.KatoClient) *core.Registry {
	r := core.NewRegistry()
	r.Add(core.Cluster{Name: name}, k)
	return r
}
```

- [ ] **Step 7: Run the whole lark package test suite**

Run: `go test ./internal/platform/lark/`
Expected: PASS (all card/decode/render/dispatch tests green). `go build ./...` still fails at `cmd` — expected, fixed in Task 5. **Do not commit.**

---

## Task 4: Config — load clusters from a YAML file

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `go.mod`, `go.sum` (add `gopkg.in/yaml.v3`)

This package is independent of `core`, so it builds and tests green on its own regardless of Tasks 2–3.

- [ ] **Step 1: Add the YAML dependency**

Run: `go get gopkg.in/yaml.v3@v3.0.1`
Expected: `go.mod` now lists `gopkg.in/yaml.v3 v3.0.1` and `go.sum` is updated.

- [ ] **Step 2: Rewrite the config tests**

Replace the entire contents of `internal/config/config_test.go` with:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeClusters writes a clusters YAML file to a temp dir and returns its path.
func writeClusters(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clusters.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoClusters = `clusters:
  - name: prod
    url: http://kato.prod.svc:8080
    label: Production
  - name: staging
    url: http://kato.staging.svc:8080
`

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_x")
	t.Setenv("LARK_APP_SECRET", "secret_x")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cfg.Clusters) != 2 {
		t.Fatalf("clusters = %+v", cfg.Clusters)
	}
	if cfg.Clusters[0].Name != "prod" || cfg.Clusters[0].URL != "http://kato.prod.svc:8080" || cfg.Clusters[0].Label != "Production" {
		t.Errorf("cluster[0] = %+v", cfg.Clusters[0])
	}
	if cfg.Clusters[1].Name != "staging" || cfg.Clusters[1].Label != "" {
		t.Errorf("cluster[1] = %+v", cfg.Clusters[1])
	}
	if cfg.KatoRunTimeout != 360*time.Second {
		t.Errorf("timeout = %v", cfg.KatoRunTimeout)
	}
	if cfg.HealthAddr != ":8080" {
		t.Errorf("health = %q", cfg.HealthAddr)
	}
	if cfg.MaxConcurrentRuns != 4 {
		t.Errorf("maxConcurrentRuns = %d, want 4", cfg.MaxConcurrentRuns)
	}
	if cfg.LarkBaseURL != "https://open.larksuite.com" {
		t.Errorf("larkBaseURL = %q", cfg.LarkBaseURL)
	}
}

func TestLoadMaxConcurrentRuns(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))

	t.Setenv("MAX_CONCURRENT_RUNS", "8")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.MaxConcurrentRuns != 8 {
		t.Errorf("maxConcurrentRuns = %d, want 8", cfg.MaxConcurrentRuns)
	}

	for _, bad := range []string{"0", "-1", "two"} {
		t.Setenv("MAX_CONCURRENT_RUNS", bad)
		if _, err := Load(); err == nil {
			t.Errorf("MAX_CONCURRENT_RUNS=%q should error", bad)
		}
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LARK_APP_ID/SECRET unset")
	}
}

func TestLoadBadTimeout(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))
	t.Setenv("KATO_RUN_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error on bad duration")
	}
}

func TestLoadClustersValidation(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")

	cases := []struct {
		name string
		body string
	}{
		{"empty list", "clusters: []\n"},
		{"missing url", "clusters:\n  - name: prod\n"},
		{"empty name", "clusters:\n  - name: \"\"\n    url: http://x\n"},
		{"duplicate name", "clusters:\n  - name: prod\n    url: http://a\n  - name: prod\n    url: http://b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, tc.body))
			if _, err := Load(); err == nil {
				t.Errorf("%s: expected a validation error", tc.name)
			}
		})
	}
}

func TestLoadClustersFileMissing(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when the clusters file is missing")
	}
}
```

- [ ] **Step 3: Run the config tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL — compile errors (`cfg.Clusters` undefined; `KatoBaseURL` referenced nowhere now).

- [ ] **Step 4: Rewrite `config.go`**

Replace the entire contents of `internal/config/config.go` with:

```go
// Package config loads kato-bot configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ClusterConfig is one configured kato backend (name → URL, with an optional label).
type ClusterConfig struct {
	Name  string
	URL   string
	Label string
}

// Config is the resolved runtime configuration.
type Config struct {
	LarkAppID         string
	LarkAppSecret     string
	Clusters          []ClusterConfig
	KatoRunTimeout    time.Duration
	HealthAddr        string
	LogLevel          string
	MaxConcurrentRuns int
	LarkBaseURL       string
}

// Load reads config from env, applying defaults. LARK_APP_ID and LARK_APP_SECRET are
// required; the clusters file (KATO_CLUSTERS_FILE, default /etc/kato-bot/clusters.yaml)
// must exist, parse, and list at least one valid cluster. KATO_RUN_TIMEOUT must parse as a
// Go duration and MAX_CONCURRENT_RUNS as a positive int when set.
func Load() (Config, error) {
	cfg := Config{
		LarkAppID:         os.Getenv("LARK_APP_ID"),
		LarkAppSecret:     os.Getenv("LARK_APP_SECRET"),
		HealthAddr:        envOr("HEALTH_ADDR", ":8080"),
		LogLevel:          envOr("LOG_LEVEL", "info"),
		KatoRunTimeout:    360 * time.Second,
		MaxConcurrentRuns: 4,
		// Open-platform base URL. Lark international: https://open.larksuite.com;
		// Feishu (China): https://open.feishu.cn.
		LarkBaseURL: envOr("LARK_BASE_URL", "https://open.larksuite.com"),
	}
	if strings.TrimSpace(cfg.LarkAppID) == "" || strings.TrimSpace(cfg.LarkAppSecret) == "" {
		return Config{}, fmt.Errorf("LARK_APP_ID and LARK_APP_SECRET are required")
	}

	clusters, err := loadClusters(envOr("KATO_CLUSTERS_FILE", "/etc/kato-bot/clusters.yaml"))
	if err != nil {
		return Config{}, err
	}
	cfg.Clusters = clusters

	if v := os.Getenv("KATO_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KATO_RUN_TIMEOUT: %w", err)
		}
		cfg.KatoRunTimeout = d
	}
	if v := os.Getenv("MAX_CONCURRENT_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("MAX_CONCURRENT_RUNS: must be a positive integer, got %q", v)
		}
		cfg.MaxConcurrentRuns = n
	}
	return cfg, nil
}

// clustersFile mirrors the YAML shape of the clusters config file.
type clustersFile struct {
	Clusters []struct {
		Name  string `yaml:"name"`
		URL   string `yaml:"url"`
		Label string `yaml:"label"`
	} `yaml:"clusters"`
}

// loadClusters reads and validates the clusters YAML file: it must contain at least one
// cluster, each with a unique non-empty name and a non-empty url.
func loadClusters(path string) ([]ClusterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read clusters file %s: %w", path, err)
	}
	var f clustersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse clusters file %s: %w", path, err)
	}
	if len(f.Clusters) == 0 {
		return nil, fmt.Errorf("clusters file %s: at least one cluster is required", path)
	}
	seen := make(map[string]bool, len(f.Clusters))
	out := make([]ClusterConfig, 0, len(f.Clusters))
	for i, c := range f.Clusters {
		name := strings.TrimSpace(c.Name)
		url := strings.TrimSpace(c.URL)
		if name == "" {
			return nil, fmt.Errorf("clusters file %s: cluster #%d has an empty name", path, i+1)
		}
		if url == "" {
			return nil, fmt.Errorf("clusters file %s: cluster %q has an empty url", path, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("clusters file %s: duplicate cluster name %q", path, name)
		}
		seen[name] = true
		out = append(out, ClusterConfig{Name: name, URL: url, Label: strings.TrimSpace(c.Label)})
	}
	return out, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 5: Run the config tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS. **Do not commit.**

---

## Task 5: Wire the registry in `main.go` (restores full green)

**Files:**
- Modify: `cmd/kato-bot/main.go`

- [ ] **Step 1: Update `main.go` to build the registry**

In `cmd/kato-bot/main.go`, replace the client/core construction block. Replace:

```go
	katoClient := kato.New(cfg.KatoBaseURL, cfg.KatoRunTimeout)
	renderer := lark.NewSender(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkBaseURL)
	c := &core.Core{Kato: katoClient, R: renderer}
```

with:

```go
	renderer := lark.NewSender(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkBaseURL)

	registry := core.NewRegistry()
	names := make([]string, 0, len(cfg.Clusters))
	for _, cl := range cfg.Clusters {
		registry.Add(core.Cluster{Name: cl.Name, Label: cl.Label}, kato.New(cl.URL, cfg.KatoRunTimeout))
		names = append(names, cl.Name)
	}
	c := &core.Core{Clusters: registry, R: renderer}
```

Then update the startup log line. Replace:

```go
	log.Printf("kato-bot connecting to Lark; kato at %s (run timeout %s, domain %s)",
		cfg.KatoBaseURL, cfg.KatoRunTimeout, cfg.LarkBaseURL)
```

with:

```go
	log.Printf("kato-bot connecting to Lark; clusters=[%s] (run timeout %s, domain %s)",
		strings.Join(names, ", "), cfg.KatoRunTimeout, cfg.LarkBaseURL)
```

Add `"strings"` to the import block in `cmd/kato-bot/main.go` (alphabetically among the stdlib imports: after `"os/signal"` add it in the right place — the stdlib group becomes `context`, `log`, `net/http`, `os/signal`, `strings`, `syscall`).

- [ ] **Step 2: Verify the whole module builds and all tests pass (full green restored)**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS for every package:
```
ok  	github.com/zufardhiyaulhaq/kato-bot/internal/config
ok  	github.com/zufardhiyaulhaq/kato-bot/internal/core
ok  	github.com/zufardhiyaulhaq/kato-bot/internal/kato
ok  	github.com/zufardhiyaulhaq/kato-bot/internal/platform/lark
```
**Do not commit.**

---

## Task 6: Helm chart + README

**Files:**
- Modify: `charts/kato-bot/values.yaml`
- Create: `charts/kato-bot/templates/clusters-configmap.yaml`
- Modify: `charts/kato-bot/templates/deployment.yaml`
- Modify: `charts/kato-bot/README.md.gotmpl` + regenerate `charts/kato-bot/README.md`

- [ ] **Step 1: Update `values.yaml`**

In `charts/kato-bot/values.yaml`, remove the `katoBaseUrl` line and add a `clusters` list. Replace:

```yaml
# -- kato REST API base URL (in-cluster Service DNS).
katoBaseUrl: http://kato.kato.svc:8080
```

with:

```yaml
# -- List of kato clusters the bot can target. Each entry needs a unique name and the
# in-cluster (or reachable) kato REST URL; label is the optional picker button text.
clusters:
  - name: default
    url: http://kato.kato.svc:8080
    # label: Default
```

- [ ] **Step 2: Create the clusters ConfigMap template**

Create `charts/kato-bot/templates/clusters-configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "kato-bot.name" . }}-clusters
  labels:
    {{- include "kato-bot.labels" . | nindent 4 }}
data:
  clusters.yaml: |
    clusters:
    {{- range .Values.clusters }}
      - name: {{ .name | quote }}
        url: {{ .url | quote }}
        {{- if .label }}
        label: {{ .label | quote }}
        {{- end }}
    {{- end }}
```

- [ ] **Step 3: Update `deployment.yaml`**

In `charts/kato-bot/templates/deployment.yaml`:

(a) Add a checksum annotation so the Deployment rolls when the clusters list changes. Change:

```yaml
    metadata:
      labels:
        {{- include "kato-bot.labels" . | nindent 8 }}
```

to:

```yaml
    metadata:
      labels:
        {{- include "kato-bot.labels" . | nindent 8 }}
      annotations:
        checksum/clusters: {{ include (print $.Template.BasePath "/clusters-configmap.yaml") . | sha256sum }}
```

(b) Replace the `KATO_BASE_URL` env entry. Change:

```yaml
            - name: KATO_BASE_URL
              value: {{ .Values.katoBaseUrl | quote }}
```

to:

```yaml
            - name: KATO_CLUSTERS_FILE
              value: /etc/kato-bot/clusters.yaml
```

(c) Add a `volumeMounts` block to the container (immediately after the `env:` list, before `ports:`):

```yaml
          volumeMounts:
            - name: clusters
              mountPath: /etc/kato-bot
              readOnly: true
```

(d) Add a `volumes` block to the pod spec. Change:

```yaml
      containers:
        - name: kato-bot
```

so that immediately above `containers:` the pod spec gains:

```yaml
      volumes:
        - name: clusters
          configMap:
            name: {{ include "kato-bot.name" . }}-clusters
      containers:
        - name: kato-bot
```

- [ ] **Step 4: Render the chart to verify it is valid**

Run:
```bash
helm lint charts/kato-bot --set lark.appId=x --set lark.appSecret=y
helm template t charts/kato-bot --set lark.appId=x --set lark.appSecret=y \
  --set 'clusters[0].name=prod' --set 'clusters[0].url=http://kato.prod.svc:8080' \
  --set 'clusters[1].name=staging' --set 'clusters[1].url=http://kato.staging.svc:8080'
```
Expected: `helm lint` reports `0 chart(s) failed`; the template output contains a `ConfigMap` named `kato-bot-clusters` whose `clusters.yaml` lists both `prod` and `staging`, a `KATO_CLUSTERS_FILE` env of `/etc/kato-bot/clusters.yaml`, a `clusters` volume + volumeMount, the `checksum/clusters` annotation, and **no** `KATO_BASE_URL`.

- [ ] **Step 5: Update the README template and regenerate**

In `charts/kato-bot/README.md.gotmpl`:

(a) Update the "How it works" numbered list — replace step 1 and add the cluster step. Replace:

```
1. Message the bot → it shows a card listing kato UseCases. In a **direct message** any
   text works; in a **group** @mention the bot (e.g. `@kato start`).
2. Pick one → the card becomes a form of that UseCase's inputs.
3. Submit → the card shows "running…", then the LLM summary kato produced.
```

with:

```
1. Message the bot → it shows a card listing the configured **clusters**. In a **direct
   message** any text works; in a **group** @mention the bot (e.g. `@kato start`).
2. Pick a cluster → the card lists that cluster's kato UseCases.
3. Pick a UseCase → the card becomes a form of that UseCase's inputs.
4. Submit → the card shows "running…", then the LLM summary kato produced.
```

(b) Update the env table — replace the `KATO_BASE_URL` row. Replace:

```
| `KATO_BASE_URL` | `http://kato.kato.svc:8080` | kato REST base URL |
```

with:

```
| `KATO_CLUSTERS_FILE` | `/etc/kato-bot/clusters.yaml` | path to the YAML file listing clusters (name → kato URL); at least one required |
```

(c) Add a short note after the "How it works" diagram block, before the Configuration section:

```
Clusters are configured via the chart's `clusters:` list (rendered into a ConfigMap the
bot reads). The bot must be able to reach each cluster's kato URL over the network —
establishing that reachability (peering, a central management cluster, or per-cluster kato
exposure) is the operator's responsibility.
```

(d) Regenerate `README.md`:

Run: `helm-docs --chart-search-root charts/kato-bot`
Expected: `charts/kato-bot/README.md` is regenerated; its values table now shows `clusters` and no `katoBaseUrl`.

- [ ] **Step 6: Final verification**

Run: `go build ./... && go test ./... -race && helm lint charts/kato-bot --set lark.appId=x --set lark.appSecret=y`
Expected: all Go packages PASS; `helm lint` reports 0 failures. **Do not commit** — leave everything in the working tree for the user to review.

---

## Self-Review (completed)

**1. Spec coverage:**
- Topology T1 / central bot → Task 5 wiring + Task 6 chart. ✅
- Cluster config = YAML file via ConfigMap → Task 4 (`loadClusters`) + Task 6 (ConfigMap, mount). ✅
- Always show cluster picker → Task 2 (`ListClusters` is the message trigger) + Task 3 (`decodeMessage` → `ListClusters`). ✅
- Remove `KATO_BASE_URL`, clusters required (fail fast) → Task 4 (`loadClusters` errors on empty) + Task 6 (values/deployment). ✅
- Cluster rides in `Reply` → Task 2 (`Reply.Cluster`) + Task 3 (decode fills it, builders read it). ✅
- `Registry` replaces `Core.Kato` → Tasks 1, 2, 5. ✅
- `RenderClusterPicker` on the port → Task 2 (interface) + Task 3 (impls). ✅
- New intents `ListClusters`/`PickCluster` → Task 2. ✅
- Unknown-cluster error (stale cards) → Task 2 (`unknownClusterMsg`) + test `TestHandleUnknownClusterRendersError`. ✅
- Testing: core flow/registry → Tasks 1–2; config parse+validation → Task 4; lark cards/decode/threading → Task 3. ✅
- ConfigMap checksum rollout → Task 6 Step 3. ✅

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code. ✅

**3. Type consistency:** `Registry` (`NewRegistry`/`Add`/`List`/`Get`), `Cluster{Name,Label}`, `Reply.Cluster`, `Core.Clusters`, intents `ListClusters`/`PickCluster`/`PickUseCase`/`SubmitForm`, `RenderClusterPicker`, builders `buildClusterPickerCard(clusters)` / `buildPickerCard(cluster, ucs)` / `buildFormCard(cluster, c, prefill, formErr)` / `buildResultCard(cluster, useCase, res)`, config `ClusterConfig{Name,URL,Label}` / `loadClusters` / `KATO_CLUSTERS_FILE` — all consistent across tasks. ✅
