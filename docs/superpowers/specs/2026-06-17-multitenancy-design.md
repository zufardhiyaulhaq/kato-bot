# kato-bot: multi-cluster (multitenancy) support

**Status:** Approved (design)

**Goal:** Let one kato-bot orchestrate **multiple Kubernetes clusters**. Today the bot
talks to a single kato (`KATO_BASE_URL`). This design replaces that single endpoint with
a **configured list of clusters** (name → kato URL) and adds a **cluster-selection step**
to the chat flow: a user first picks a cluster, then picks a UseCase, fills the form, and
runs — exactly as today, but against the chosen cluster's kato.

---

## Background

kato-bot is a thin ChatOps adapter over kato's REST API. The current flow is:

```
message → use-case picker → form → running → result
```

The flow is **stateless between card clicks**: each card button's `value` carries
everything the next step needs (`action` + `usecase`), and the bot patches the same card
via Lark's `open_message_id`. There is no server-side session. (See the v1 design,
`2026-06-16-kato-bot-design.md`.)

The new flow adds one step at the front:

```
message → cluster picker → use-case picker → form → running → result
```

---

## Topology decision (from brainstorming)

The deciding constraint is how Lark delivers events to multiple instances of the same
app. Per Feishu's docs, the long-connection mode is **cluster mode, not broadcast**: each
event is delivered to **one random connection** among all instances sharing an app id,
with **no way to route by content**. (Max 50 connections per app.)

This rules out "one bot per cluster under a shared identity, where Lark routes
`cluster=prod` events to the prod instance" — Lark offers no such routing hook, and the
initial cluster-picker message would land on an instance that only knows its own cluster.

**Chosen topology — T1: a single central bot with a multi-cluster config.** One
deployment, one WebSocket connection, one bot identity. The bot reaches each cluster's
kato over the network and shows an in-card cluster picker.

### Precondition (operator responsibility, out of scope for kato-bot)

T1 requires that the **single bot deployment can reach every cluster's kato endpoint**
over the network — via VPC/network peering, running the bot in a central management
cluster, or exposing each cluster's kato through an internal LB/ingress. kato-bot only
needs a reachable URL per cluster; establishing that reachability is the operator's
responsibility and is not solved by this design.

---

## Decisions (from brainstorming)

1. **Topology:** T1 — one central bot, multi-cluster config (above).
2. **Cluster config format:** a **YAML list** of clusters, surfaced as a Helm `clusters:`
   value, rendered into a **ConfigMap file** the bot reads (not a single env var).
3. **UX:** **always show the cluster picker first**, even when only one cluster is
   configured. One consistent code path; no special-casing.
4. **Backward compatibility:** **remove** `KATO_BASE_URL` / `katoBaseUrl` entirely. The
   clusters list is **required** — the bot fails fast at startup if it is empty.
5. **State threading:** the chosen cluster rides inside the card's action `value` (like
   `usecase` does today). No new server-side state; single replica unchanged.

---

## Architecture

The chosen cluster is threaded through the flow inside the existing **`core.Reply`**
struct — the opaque per-interaction context already passed to every intent and every
`Render*` call. This keeps signature churn minimal: `PickUseCase` and `SubmitForm` are
unchanged in shape, and every card builder can read `r.Cluster` to embed it in the next
button's `value`.

Core's single `Kato KatoClient` becomes a **`Registry`** of clients keyed by cluster name.

### Data flow

```
message            → ListClusters  → cluster picker     (registry.List, no kato call)
click cluster      → PickCluster   → use-case picker    (registry.Get(cluster) → ListUseCases)
click "Select"     → PickUseCase   → form               (registry.Get → GetUseCase)
submit             → SubmitForm    → running → result   (registry.Get → Run, async)
```

The async fast-ack + patch behavior for runs is unchanged; only *which* client runs it
is now resolved from the registry by cluster name.

### Error handling

- **Empty clusters list at startup** → config load fails, process exits with a clear
  message ("at least one cluster is required").
- **Unknown cluster name in a card value** (e.g. a stale card after a cluster was removed
  from config) → `registry.Get` misses → the handler renders an error card
  ("unknown cluster `X` — start over"). This is handled uniformly in every cluster-aware
  handler.
- Existing kato error mapping (`friendlyKatoError`, 400→form, 404/422/429/5xx) is
  unchanged and now applies per resolved client.

---

## Components

### `internal/core`

- **`Reply`** gains `Cluster string` (opaque to core; adapters fill it from the card
  value). Empty for the initial `ListClusters` step.
- **`Cluster`** new value type: `{ Name string; Label string }`. `Label` is the
  human-facing button text; defaults to `Name` when unset.
- **`Registry`** new concrete, dependency-free type (uses only core types):
  - `List() []Cluster` — clusters in config order (stable).
  - `Get(name string) (KatoClient, bool)` — resolve a client by cluster name.
  - Constructed by the wiring layer; holds `map[string]KatoClient` + an ordered
    `[]Cluster`.
- **`Core`**: `Kato KatoClient` → `Clusters *Registry`.
- **Intents**: add `ListClusters{Reply}` and `PickCluster{Reply}` (cluster is in
  `Reply.Cluster`). `PickUseCase{Reply, Name}` and `SubmitForm{Reply, Name, Inputs}` are
  unchanged in shape. The old `ListUseCases` intent is replaced by `ListClusters` as the
  message trigger; listing use cases now happens inside `PickCluster`.
- **`Renderer`** interface gains `RenderClusterPicker(ctx, r, []Cluster)`.
- **`Core.Handle`** branches:
  - `ListClusters` → `RenderClusterPicker(r, c.Clusters.List())` (no kato call).
  - `PickCluster` → resolve client; `ListUseCases` → `RenderPicker`.
  - `PickUseCase` → resolve client; `GetUseCase` → `RenderForm`.
  - `SubmitForm` → resolve client; validate; `RenderRunning` + deferred `Run` →
    `RenderResult`.
  - Resolution failure (unknown cluster) → `RenderError`.

### `internal/platform/lark`

- **`cards.go`**:
  - new `buildClusterPickerCard(clusters []core.Cluster)` — one button per cluster,
    `value: {action:"pick_cluster", cluster:<name>}`, text = `Label`.
  - `buildPickerCard`, `buildFormCard`, `buildResultCard` take the cluster and embed it in
    every `pick` / `run` / run-again `value` (`{action:"pick", cluster, usecase}`,
    `{action:"run", cluster, usecase}`).
- **`decode.go`**:
  - extract `cluster` from `action.value` into `Reply.Cluster` for every action.
  - handle the new `pick_cluster` action → `PickCluster`.
- **`dispatch.go`**: `decodeMessage` now produces `ListClusters` (was `ListUseCases`).
- **`cardaction.go`**: the transient Core built per card action uses
  `Clusters: a.Core.Clusters` (was `Kato: a.Core.Kato`); `captureRenderer` implements the
  new `RenderClusterPicker`; `replyOf` covers the new intents.

### `internal/config`

- **Remove** `KatoBaseURL`.
- Add `KATO_CLUSTERS_FILE` (default `/etc/kato-bot/clusters.yaml`).
- Parse a YAML document into a list of `{ name, url, label? }` (adds the
  `gopkg.in/yaml.v3` dependency).
- **Validation** (fail fast, returns a `config.Load` error):
  - file readable and parses as YAML;
  - at least one cluster;
  - each `name` non-empty and **unique**;
  - each `url` non-empty.
- `Config` exposes the parsed `[]ClusterConfig{ Name, URL, Label }`.

### Wiring — `cmd/kato-bot/main.go`

- Build one `kato.New(cluster.URL, cfg.KatoRunTimeout)` per configured cluster.
- Assemble a `core.Registry` (cluster `{Name, Label}` → client, in config order).
- `Core{Clusters: registry, R: renderer}`.
- Startup log lists the configured cluster names.

### Helm chart — `charts/kato-bot`

- Add a `clusters:` list to `values.yaml`:
  ```yaml
  clusters:
    - name: prod
      url: http://kato.kato.svc:8080
      label: Production        # optional; defaults to name
    - name: staging
      url: http://kato.staging.svc:8080
  ```
- New **ConfigMap** template rendering `clusters.yaml` from `.Values.clusters`.
- **Deployment**: mount the ConfigMap as a volume at `/etc/kato-bot`; set
  `KATO_CLUSTERS_FILE=/etc/kato-bot/clusters.yaml`; **remove** the `KATO_BASE_URL` env and
  the `katoBaseUrl` value.
- A checksum annotation on the clusters ConfigMap so the Deployment rolls when the list
  changes.
- Update `README.md.gotmpl` (+ regenerated `README.md`): document the cluster picker step,
  the `clusters:` value, and the removal of `katoBaseUrl`.

---

## Testing

- **core**:
  - `ListClusters` renders the cluster picker from the registry (no kato call).
  - `PickCluster` resolves the right client and lists that cluster's use cases.
  - unknown cluster → `RenderError`.
  - `PickUseCase` / `SubmitForm` resolve the correct client (a two-cluster registry with
    distinguishable fake clients proves routing).
  - `Registry` unit tests: `List` order is config order; `Get` hit/miss.
- **config**:
  - parse a valid clusters YAML file;
  - empty list → error; duplicate names → error; missing url → error; unreadable/invalid
    file → error.
- **lark**:
  - `buildClusterPickerCard` emits one `pick_cluster` button per cluster with the cluster
    name in `value` and `Label` as text;
  - `decode` maps `pick_cluster` → `PickCluster` and fills `Reply.Cluster`;
  - cluster is threaded into `pick` and `run` button values by the picker/form/result
    builders.

---

## Out of scope (YAGNI)

- Cross-cluster network reachability (operator precondition, above).
- Per-cluster or per-user access control / RBAC — access is still governed solely by Lark
  membership.
- Per-cluster credentials/auth to kato (kato is unauthenticated, as today).
- Skipping the picker for a single cluster (explicitly decided against — always show it).
- Dynamic/hot reload of the clusters list (a config change rolls the Deployment via the
  ConfigMap checksum).
- Federated/broker topologies (T3) and separate-identity bots (T2).
