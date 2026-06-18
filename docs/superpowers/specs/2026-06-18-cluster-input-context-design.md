# Cluster + Inputs Context in Lark Cards
{% raw %}

**Date:** 2026-06-18
**Status:** Approved

## Problem

kato-bot targets multiple kato clusters, but the Lark cards never show which cluster a
troubleshoot is running against, and the result card does not echo the inputs that were
used. A user who runs the same use case across clusters cannot tell, from the result card
alone, where it ran or with what inputs.

Today, across the five-card flow:

| # | Card | Has cluster in scope | Has inputs in scope | Shows cluster | Shows inputs |
|---|------|:---:|:---:|:---:|:---:|
| 1 | Cluster picker | — (being chosen) | no | n/a | n/a |
| 2 | Use-case picker | yes | no | **no** | n/a |
| 3 | Form (fill inputs) | yes | being typed | **no** | (being typed) |
| 4 | Running… | yes | yes | **no** | yes |
| 5 | Result | yes | yes | **no** | **no** |

## Goal

Keep the targeted cluster visible from use-case selection through the final result, and
echo the inputs on the running and result cards, so a user always knows *where* a
troubleshoot ran and *what* it ran with. Display-only; no behavioral change.

## Decisions

1. **Cluster identifier: name only.** Show the cluster's unique `name` (e.g. `cluster-a`).
   It is already available at render time on every `Reply` (`r.Cluster`) and as the
   `cluster` param of the picker/form builders. No label threading.
2. **Scope: cards 2, 3, 4, 5.** The cluster line is added to the use-case picker, the
   form, the running card, and the result card, so the cluster stays visible continuously
   and never disappears mid-flow. The inputs line is added to the running and result cards.
3. **Error branch included.** The result card's error branch (`res.Err != nil`) also shows
   the cluster + inputs lines — that is exactly when "where/what" matters most.
4. **Empty inputs: omit the inputs line.** When a use case has no inputs, render no inputs
   line (rather than `Inputs: (none)`), to avoid a noise/blank line.
5. **No core/engine changes.** `core.go` already passes `inputs` into `RenderResult`, and
   `r.Cluster` is already on every `Reply`. The `core.Renderer` interface signatures in
   `core/types.go` are unchanged. This is purely a `internal/platform/lark` display change.

## Architecture

All changes live in `internal/platform/lark`. A single shared helper renders the context
lines so every card stays identical, and four card builders call it. Two builder
signatures gain a parameter; their callers (`render.go` and the `captureRenderer` test
double in `cardaction.go`) are updated.

## Component 1 — shared `contextLines` helper

**File:** `internal/platform/lark/cards.go`

Add a helper that returns the markdown element(s) for the context block:

- Always emits a cluster line: `☰ Cluster: <name>`.
- When `inputs` is non-empty, also emits an inputs line built from the existing `kvLine`:
  `☰ Inputs: k=v  k=v`.
- When `inputs` is empty (or nil), emits only the cluster line.

Signature (illustrative): `func contextLines(cluster string, inputs map[string]string) []any`.
For cards that have no inputs in scope (use-case picker, form), callers pass `nil` inputs,
which yields just the cluster line. Keeping all formatting in this one helper guarantees
the four cards render the context identically.

## Component 2 — per-card wiring

**File:** `internal/platform/lark/cards.go`

| Card | Function | Change |
|---|---|---|
| 2 use-case picker | `buildPickerCard(cluster, ucs)` | already has `cluster`; prepend `contextLines(cluster, nil)` after the header line |
| 3 form | `buildFormCard(cluster, c, prefill, formErr)` | already has `cluster`; prepend `contextLines(cluster, nil)` (above the form/no-input button) |
| 4 running | `buildRunningCard(useCase, inputs)` | **add `cluster string` param**; render `contextLines(cluster, inputs)` (replaces today's bare `kvLine` line) |
| 5 result | `buildResultCard(cluster, useCase, res)` | **add `inputs map[string]string` param**; render `contextLines(cluster, inputs)` on **both** the success and error branches |

The running card today shows a bare `kvLine(inputs)` line and no cluster; it is replaced by
the `contextLines` block so it matches the other cards (cluster + inputs).

## Component 3 — call-site updates

**Files:** `internal/platform/lark/render.go`, `internal/platform/lark/cardaction.go`

- `render.go` `RenderRunning(...)` → `buildRunningCard(r.Cluster, useCase, inputs)`.
- `render.go` `RenderResult(...)` → `buildResultCard(r.Cluster, useCase, inputs, res)`.
- `cardaction.go` `captureRenderer.RenderRunning` → `buildRunningCard(rep.Cluster, uc, in)`
  (the capture renderer currently drops the cluster; it has `rep core.Reply` available).
- `cardaction.go` `captureRenderer.RenderResult` → `buildResultCard(rep.Cluster, uc, in, res)`.

No change to `core.Renderer` interface methods — they already carry `r`/`inputs`.

## Data flow

```
Reply{Cluster: "cluster-a", ...}  +  inputs{namespace:prod, pod:api}
        │
        ├─ RenderRunning → buildRunningCard("cluster-a", "pod-crashloop", inputs)
        │                     → contextLines("cluster-a", inputs)
        └─ RenderResult  → buildResultCard("cluster-a", "pod-crashloop", inputs, res)
                              → contextLines("cluster-a", inputs)  (success or error branch)
```

Rendered result (success):

```
✅ pod-crashloop — Completed
☰ Cluster: cluster-a
☰ Inputs: namespace=prod  pod=api-xyz
──────────
📋 Summary
...
```

## Error handling

No new error paths. The helper is pure string assembly over already-validated data. The
result card's existing error branch gains the same two context lines before the error
message, so a failed run still reports its cluster and inputs.

## Testing

**File:** `internal/platform/lark/cards_test.go`

- Use-case picker (card 2) contains `Cluster: <name>`.
- Form card (card 3) contains `Cluster: <name>`.
- Running card (card 4) contains both `Cluster: <name>` and the inputs (`k=v`).
- Result card success branch (card 5) contains `Cluster: <name>` and the inputs.
- Result card error branch (`res.Err != nil`) contains `Cluster: <name>` and the inputs.
- No-input use case: running/result cards contain the cluster line but **no** inputs line.

## Out of scope / YAGNI

- No cluster label (name only).
- No cluster line on the cluster-picker card (card 1) — the cluster is being chosen there.
- No core/engine changes; no `core.Renderer` signature changes.
- No change to the inputs ordering (map iteration order; display-only, as today).

## Constraint

Do not commit. All changes — including this spec — stay in the working tree (standing user
instruction).
{% endraw %}