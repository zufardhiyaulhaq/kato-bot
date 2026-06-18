# Cluster + Inputs Context in Lark Cards Implementation Plan
{% raw %}

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **DO NOT COMMIT.** Standing user instruction: never run `git add`/`git commit`. Each task ends with a **Verify** step (run the build + tests) instead of a commit. All changes stay in the working tree.

**Goal:** Show the targeted cluster on the use-case picker, form, running, and result cards, and echo the inputs on the running and result cards, so a user always knows where a troubleshoot ran and with what inputs.

**Architecture:** Pure display change inside `internal/platform/lark`. One shared `contextLines(cluster, inputs)` helper renders a `☰ Cluster: …` line (always) plus a `☰ Inputs: …` line (only when inputs are non-empty). Four card builders call it; two builders gain a parameter, and their callers in `render.go` and `cardaction.go` are updated. No `core` package or engine changes.

**Tech Stack:** Go 1.24.8 (asdf), standard `testing`, Lark Card JSON 2.0 (built as `map[string]any`). Module `github.com/zufardhiyaulhaq/kato-bot`. Run all commands from the kato-bot repo root: `/Users/zufardhiyaulhaq/Documents/personal/github/kato-bot`.

**Why additive ordering:** Task 1 adds a new helper (no signature change). Task 2 prepends lines to two builders that already have `cluster` (no signature change). Tasks 3 and 4 change one builder signature each and update that builder's call sites and tests in the same task, so `go build ./...` and `go test ./...` stay green after every task.

---

## File Structure

- `internal/platform/lark/cards.go` — card builders. Add `contextLines` helper; edit `buildPickerCard`, `buildFormCard`, `buildRunningCard`, `buildResultCard`.
- `internal/platform/lark/render.go` — `Renderer` methods. Edit `RenderRunning`, `RenderResult` call sites.
- `internal/platform/lark/cardaction.go` — `captureRenderer` test double. Edit `RenderRunning`, `RenderResult` call sites.
- `internal/platform/lark/cards_test.go` — tests. Add `TestContextLines`; edit picker/form/running/result tests; add no-input tests.

No new files.

---

### Task 1: `contextLines` shared helper

**Files:**
- Modify: `internal/platform/lark/cards.go` (add helper near `kvLine`, around line 159)
- Test: `internal/platform/lark/cards_test.go` (add `TestContextLines`)

- [ ] **Step 1: Write the failing test**

Add to `internal/platform/lark/cards_test.go`:

```go
func TestContextLines(t *testing.T) {
	// nil inputs: cluster line only, no Inputs line.
	only := contextLines("cluster-a", nil)
	if len(only) != 1 {
		t.Fatalf("nil inputs: want 1 element, got %d", len(only))
	}
	js := jsonStr(only)
	if !strings.Contains(js, "Cluster: cluster-a") {
		t.Errorf("missing cluster line: %s", js)
	}
	if strings.Contains(js, "Inputs:") {
		t.Errorf("nil inputs must not render an Inputs line: %s", js)
	}

	// empty map behaves like nil.
	if len(contextLines("c", map[string]string{})) != 1 {
		t.Error("empty inputs map must not render an Inputs line")
	}

	// non-empty inputs: cluster line + inputs line.
	both := contextLines("cluster-a", map[string]string{"namespace": "prod"})
	if len(both) != 2 {
		t.Fatalf("with inputs: want 2 elements, got %d", len(both))
	}
	js2 := jsonStr(both)
	if !strings.Contains(js2, "Cluster: cluster-a") || !strings.Contains(js2, "namespace=prod") {
		t.Errorf("with inputs must render cluster + inputs: %s", js2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/platform/lark/ -run TestContextLines -v`
Expected: FAIL — compile error `undefined: contextLines`.

- [ ] **Step 3: Write minimal implementation**

In `internal/platform/lark/cards.go`, add this helper immediately after `kvLine` (after the `}` that closes `kvLine`, around line 159):

```go
// contextLines renders the shared "which cluster / what inputs" block used by the picker,
// form, running, and result cards. The cluster line is always present; the inputs line is
// included only when inputs is non-empty (a nil or empty map renders no inputs line).
func contextLines(cluster string, inputs map[string]string) []any {
	lines := []any{markdown("☰ Cluster: " + cluster)}
	if len(inputs) > 0 {
		lines = append(lines, markdown("☰ Inputs: "+kvLine(inputs)))
	}
	return lines
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/platform/lark/ -run TestContextLines -v`
Expected: PASS.

- [ ] **Step 5: Verify (no commit)**

Run: `go build ./... && go test ./...`
Expected: build OK; all packages `ok`. Leave changes uncommitted.

---

### Task 2: Cluster line on the use-case picker (card 2) and form (card 3)

**Files:**
- Modify: `internal/platform/lark/cards.go` (`buildPickerCard` ~line 67, `buildFormCard` ~line 90)
- Test: `internal/platform/lark/cards_test.go` (`TestBuildPickerCard`, `TestBuildFormCard`)

These builders already receive `cluster`; no signature change. We prepend `contextLines(cluster, nil)`.

- [ ] **Step 1: Write the failing assertions**

In `TestBuildPickerCard`, add after the existing `"cluster":"prod"` action check (after line 61):

```go
	if !strings.Contains(card, "Cluster: prod") {
		t.Error("picker must show the cluster context line")
	}
```

In `TestBuildFormCard`, add after the existing `"cluster":"prod"` action check (after line 83):

```go
	if !strings.Contains(card, "Cluster: prod") {
		t.Error("form must show the cluster context line")
	}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/lark/ -run 'TestBuildPickerCard|TestBuildFormCard' -v`
Expected: FAIL — `picker must show the cluster context line` / `form must show the cluster context line` (the display line does not exist yet; only the JSON action value `"cluster":"prod"` does).

- [ ] **Step 3: Implement — picker card**

In `buildPickerCard`, insert the context line right after the header element. Change:

```go
func buildPickerCard(cluster string, ucs []core.UseCase) string {
	elements := []any{markdown("🔧 **kato** — pick a troubleshooting flow")}
	for _, uc := range ucs {
```

to:

```go
func buildPickerCard(cluster string, ucs []core.UseCase) string {
	elements := []any{markdown("🔧 **kato** — pick a troubleshooting flow")}
	elements = append(elements, contextLines(cluster, nil)...)
	for _, uc := range ucs {
```

- [ ] **Step 4: Implement — form card, no-input branch**

In `buildFormCard`, the `len(c.Inputs) == 0` branch. Change:

```go
		elems = append(elems,
			markdown(fmt.Sprintf("🔧 **%s**\n%s", c.Name, c.Description)),
			markdown("_No inputs required — click Run._"),
			button2("▶ Run troubleshoot", runValue),
		)
		return card2(c.Name, elems)
```

to:

```go
		elems = append(elems, markdown(fmt.Sprintf("🔧 **%s**\n%s", c.Name, c.Description)))
		elems = append(elems, contextLines(cluster, nil)...)
		elems = append(elems,
			markdown("_No inputs required — click Run._"),
			button2("▶ Run troubleshoot", runValue),
		)
		return card2(c.Name, elems)
```

- [ ] **Step 5: Implement — form card, form branch**

In `buildFormCard`, after the description line is appended to `formElems`. Change:

```go
	formElems = append(formElems, markdown(fmt.Sprintf("🔧 **%s**\n%s", c.Name, c.Description)))
	for _, in := range c.Inputs {
```

to:

```go
	formElems = append(formElems, markdown(fmt.Sprintf("🔧 **%s**\n%s", c.Name, c.Description)))
	formElems = append(formElems, contextLines(cluster, nil)...)
	for _, in := range c.Inputs {
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/platform/lark/ -run 'TestBuildPickerCard|TestBuildFormCard' -v`
Expected: PASS.

- [ ] **Step 7: Verify (no commit)**

Run: `go build ./... && go test ./...`
Expected: build OK; all packages `ok`. Leave changes uncommitted.

---

### Task 3: Cluster + inputs on the running card (card 4)

**Files:**
- Modify: `internal/platform/lark/cards.go` (`buildRunningCard` ~line 162)
- Modify: `internal/platform/lark/render.go` (`RenderRunning` ~line 48)
- Modify: `internal/platform/lark/cardaction.go` (`captureRenderer.RenderRunning` ~line 30)
- Test: `internal/platform/lark/cards_test.go` (`TestBuildRunningCard`; add `TestBuildRunningCardNoInputs`)

This changes `buildRunningCard`'s signature to add `cluster`. The builder, both call sites, and the test all change together so the build stays green.

- [ ] **Step 1: Update the test to the new signature and assert the cluster line**

Replace `TestBuildRunningCard` with:

```go
func TestBuildRunningCard(t *testing.T) {
	card := buildRunningCard("cluster-a", "pod-crashloop", map[string]string{"namespace": "payments"})
	if !strings.Contains(card, "Running") || !strings.Contains(card, "pod-crashloop") {
		t.Error("running card content")
	}
	if !strings.Contains(card, "Cluster: cluster-a") {
		t.Error("missing cluster line")
	}
	if !strings.Contains(card, "namespace=payments") {
		t.Error("missing inputs line")
	}
	asMap(t, card)
}

func TestBuildRunningCardNoInputs(t *testing.T) {
	card := buildRunningCard("cluster-a", "pod-crashloop", map[string]string{})
	if !strings.Contains(card, "Cluster: cluster-a") {
		t.Error("running card must show the cluster line even with no inputs")
	}
	if strings.Contains(card, "Inputs:") {
		t.Error("running card must not render an Inputs line when there are no inputs")
	}
	asMap(t, card)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/lark/ -run 'TestBuildRunningCard' -v`
Expected: FAIL — compile error `not enough arguments in call to buildRunningCard` (the test now passes 3 args; the function still takes 2).

- [ ] **Step 3: Implement the new builder**

In `internal/platform/lark/cards.go`, replace `buildRunningCard`:

```go
// buildRunningCard is the immediate ack repaint shown while kato runs.
func buildRunningCard(useCase string, inputs map[string]string) string {
	return card2("kato", []any{
		markdown(fmt.Sprintf("⏳ **Running %s…**", useCase)),
		markdown(kvLine(inputs)),
		markdown("_This can take up to ~30s while kato runs the checks and summarizes._"),
	})
}
```

with:

```go
// buildRunningCard is the immediate ack repaint shown while kato runs.
func buildRunningCard(cluster, useCase string, inputs map[string]string) string {
	elements := []any{markdown(fmt.Sprintf("⏳ **Running %s…**", useCase))}
	elements = append(elements, contextLines(cluster, inputs)...)
	elements = append(elements, markdown("_This can take up to ~30s while kato runs the checks and summarizes._"))
	return card2("kato", elements)
}
```

- [ ] **Step 4: Update the `render.go` call site**

In `internal/platform/lark/render.go`, change:

```go
func (rd *Renderer) RenderRunning(ctx context.Context, r core.Reply, useCase string, inputs map[string]string) error {
	return rd.emit(ctx, r, buildRunningCard(useCase, inputs))
}
```

to:

```go
func (rd *Renderer) RenderRunning(ctx context.Context, r core.Reply, useCase string, inputs map[string]string) error {
	return rd.emit(ctx, r, buildRunningCard(r.Cluster, useCase, inputs))
}
```

- [ ] **Step 5: Update the `cardaction.go` call site**

In `internal/platform/lark/cardaction.go`, change:

```go
func (r *captureRenderer) RenderRunning(_ context.Context, _ core.Reply, uc string, in map[string]string) error {
	r.card = buildRunningCard(uc, in)
	return nil
}
```

to:

```go
func (r *captureRenderer) RenderRunning(_ context.Context, rep core.Reply, uc string, in map[string]string) error {
	r.card = buildRunningCard(rep.Cluster, uc, in)
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/platform/lark/ -run 'TestBuildRunningCard' -v`
Expected: PASS (both `TestBuildRunningCard` and `TestBuildRunningCardNoInputs`).

- [ ] **Step 7: Verify (no commit)**

Run: `go build ./... && go test ./...`
Expected: build OK; all packages `ok`. Leave changes uncommitted.

---

### Task 4: Cluster + inputs on the result card (card 5), both branches

**Files:**
- Modify: `internal/platform/lark/cards.go` (`buildResultCard` ~line 171)
- Modify: `internal/platform/lark/render.go` (`RenderResult` ~line 52)
- Modify: `internal/platform/lark/cardaction.go` (`captureRenderer.RenderResult` ~line 34)
- Test: `internal/platform/lark/cards_test.go` (`TestBuildResultCardCompleted`, `TestBuildResultCardFailedPhase`, `TestBuildResultCardError`; add `TestBuildResultCardNoInputs`)

This changes `buildResultCard`'s signature to add `inputs map[string]string` between `useCase` and `res`. The builder, both call sites, and the three result tests all change together.

- [ ] **Step 1: Update the result tests to the new signature and assert cluster + inputs**

Replace the three existing result tests (`TestBuildResultCardCompleted`, `TestBuildResultCardFailedPhase`, `TestBuildResultCardError`) with these, and add `TestBuildResultCardNoInputs`:

```go
func TestBuildResultCardCompleted(t *testing.T) {
	card := buildResultCard("prod", "pod-crashloop", map[string]string{"namespace": "payments"}, core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Completed", Summary: "It is OOMKilled.",
	})
	if !strings.Contains(card, "It is OOMKilled.") || !strings.Contains(card, "pod-crashloop-abc") {
		t.Error("result card content")
	}
	if !strings.Contains(card, "✅") {
		t.Error("completed phase should show a green check")
	}
	if !strings.Contains(card, "Cluster: prod") {
		t.Error("result card must show the cluster line")
	}
	if !strings.Contains(card, "namespace=payments") {
		t.Error("result card must show the inputs line")
	}
	if !strings.Contains(card, `"action":"pick"`) {
		t.Error("missing Run again action")
	}
	if !strings.Contains(card, `"cluster":"prod"`) {
		t.Error("run-again action must carry the cluster")
	}
	asMap(t, card)
}

func TestBuildResultCardFailedPhase(t *testing.T) {
	card := buildResultCard("prod", "pod-crashloop", map[string]string{"namespace": "payments"}, core.RunResult{
		Run: "pod-crashloop-abc", Phase: "Failed", Summary: "step errored",
	})
	if strings.Contains(card, "✅") {
		t.Error("failed phase must not show a green check")
	}
	if !strings.Contains(card, "❌") || !strings.Contains(card, "Failed") {
		t.Error("failed phase should show a red cross and the phase")
	}
	if !strings.Contains(card, "Cluster: prod") || !strings.Contains(card, "namespace=payments") {
		t.Error("failed-phase result must show cluster + inputs")
	}
	asMap(t, card)
}

func TestBuildResultCardError(t *testing.T) {
	card := buildResultCard("prod", "uc", map[string]string{"namespace": "payments"}, core.RunResult{Err: &core.RunError{Msg: "kato is busy"}})
	if !strings.Contains(card, "kato is busy") {
		t.Error("error not shown")
	}
	if !strings.Contains(card, "Cluster: prod") {
		t.Error("error result must still show the cluster line")
	}
	if !strings.Contains(card, "namespace=payments") {
		t.Error("error result must still show the inputs line")
	}
	asMap(t, card)
}

func TestBuildResultCardNoInputs(t *testing.T) {
	card := buildResultCard("prod", "uc", map[string]string{}, core.RunResult{
		Run: "uc-1", Phase: "Completed", Summary: "ok",
	})
	if !strings.Contains(card, "Cluster: prod") {
		t.Error("result card must show the cluster line with no inputs")
	}
	if strings.Contains(card, "Inputs:") {
		t.Error("result card must not render an Inputs line when there are no inputs")
	}
	asMap(t, card)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/lark/ -run 'TestBuildResultCard' -v`
Expected: FAIL — compile error `not enough arguments in call to buildResultCard` (tests now pass 4 args; the function still takes 3).

- [ ] **Step 3: Implement the new builder**

In `internal/platform/lark/cards.go`, replace `buildResultCard`:

```go
// buildResultCard renders the final summary (or a friendly error) plus a Run-again button.
func buildResultCard(cluster, useCase string, res core.RunResult) string {
	var elements []any
	if res.Err != nil {
		elements = []any{
			markdown(fmt.Sprintf("❌ **%s — could not run**", useCase)),
			markdown(res.Err.Error()),
		}
	} else {
		// Phase icon: kato reports "Completed" on success; anything else (e.g. "Failed") is not green.
		icon := "✅"
		if res.Phase != "Completed" {
			icon = "❌"
		}
		head := icon + " **" + useCase + " — " + res.Phase + "**"
		elements = []any{markdown(head)}
		if res.Warning != "" {
			elements = append(elements, markdown("⚠️ "+res.Warning))
		}
		elements = append(elements,
			map[string]any{"tag": "hr"},
			markdown("📋 **Summary**\n"+res.Summary),
			map[string]any{"tag": "hr"},
			markdown("_run: "+res.Run+"_"),
		)
	}
	elements = append(elements, button2("↻ Run again", map[string]any{"action": "pick", "cluster": cluster, "usecase": useCase}))
	return card2(useCase, elements)
}
```

with:

```go
// buildResultCard renders the final summary (or a friendly error) plus a Run-again button.
// The cluster + inputs context block appears on both the success and error branches.
func buildResultCard(cluster, useCase string, inputs map[string]string, res core.RunResult) string {
	var elements []any
	if res.Err != nil {
		elements = []any{markdown(fmt.Sprintf("❌ **%s — could not run**", useCase))}
		elements = append(elements, contextLines(cluster, inputs)...)
		elements = append(elements, markdown(res.Err.Error()))
	} else {
		// Phase icon: kato reports "Completed" on success; anything else (e.g. "Failed") is not green.
		icon := "✅"
		if res.Phase != "Completed" {
			icon = "❌"
		}
		head := icon + " **" + useCase + " — " + res.Phase + "**"
		elements = []any{markdown(head)}
		elements = append(elements, contextLines(cluster, inputs)...)
		if res.Warning != "" {
			elements = append(elements, markdown("⚠️ "+res.Warning))
		}
		elements = append(elements,
			map[string]any{"tag": "hr"},
			markdown("📋 **Summary**\n"+res.Summary),
			map[string]any{"tag": "hr"},
			markdown("_run: "+res.Run+"_"),
		)
	}
	elements = append(elements, button2("↻ Run again", map[string]any{"action": "pick", "cluster": cluster, "usecase": useCase}))
	return card2(useCase, elements)
}
```

- [ ] **Step 4: Update the `render.go` call site**

In `internal/platform/lark/render.go`, change:

```go
func (rd *Renderer) RenderResult(ctx context.Context, r core.Reply, useCase string, inputs map[string]string, res core.RunResult) error {
	return rd.emit(ctx, r, buildResultCard(r.Cluster, useCase, res))
}
```

to:

```go
func (rd *Renderer) RenderResult(ctx context.Context, r core.Reply, useCase string, inputs map[string]string, res core.RunResult) error {
	return rd.emit(ctx, r, buildResultCard(r.Cluster, useCase, inputs, res))
}
```

- [ ] **Step 5: Update the `cardaction.go` call site**

In `internal/platform/lark/cardaction.go`, change:

```go
func (r *captureRenderer) RenderResult(_ context.Context, rep core.Reply, uc string, in map[string]string, res core.RunResult) error {
	r.card = buildResultCard(rep.Cluster, uc, res)
	return nil
}
```

to:

```go
func (r *captureRenderer) RenderResult(_ context.Context, rep core.Reply, uc string, in map[string]string, res core.RunResult) error {
	r.card = buildResultCard(rep.Cluster, uc, in, res)
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/platform/lark/ -run 'TestBuildResultCard' -v`
Expected: PASS (Completed, FailedPhase, Error, NoInputs).

- [ ] **Step 7: Verify whole module (no commit)**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build OK; vet OK; all packages `ok`. Leave changes uncommitted.

---

## Final Verification (after all tasks)

- [ ] Run: `go build ./... && go vet ./... && go test ./...` from the kato-bot repo root.
  Expected: build OK; vet OK; every package `ok` (no `FAIL`).
- [ ] Confirm nothing was committed: `git status` should show modified `internal/platform/lark/*.go` files plus the spec/plan docs, with HEAD unchanged.
{% endraw %}