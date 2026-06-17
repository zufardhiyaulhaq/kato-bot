# kato-bot: ChatOps front-end for kato

{% raw %}
**Status:** Approved (design)

**Goal:** Build a Go service, `kato-bot`, that lets users run kato troubleshooting
UseCases from chat. It is a thin **ChatOps adapter** over kato's existing REST API:
a chat user picks a UseCase, fills in its declared inputs via an interactive form,
and the bot calls kato's synchronous `POST /run` and posts the resulting LLM summary
back into the chat. v1 ships the **Lark** platform end-to-end; the core is structured
so **Slack** and **Telegram** adapters can be added later without reworking it.

---

## Background

kato is a Kubernetes troubleshooting operator. It exposes a REST API (no auth,
designed for internal/in-cluster reach):

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/usecases` | list use cases (`{usecases:[{name,description,inputs,ready}]}`) |
| `GET /api/v1/usecases/{name}` | one use case's contract (`{name,description,inputs:[{name,required,...}],ready}`) |
| `POST /api/v1/usecases/{name}/run` | execute, body `{"inputs":{...}}`, returns `{run,phase,summary,warning,steps}` |
| `GET /api/v1/runs/{name}` | a past run |

Key facts that shape this design:

- **`POST /run` is synchronous and slow.** kato runs every step *and* calls the LLM
  before responding — tens of seconds. The bot must not block a chat-platform
  callback on it.
- **kato has no authentication.** The bot becomes the reach path to kato. Access is
  governed entirely by **Lark group membership** — whoever the bot is invited to chat
  with can use it. This is acceptable because kato is read-only (`get/list/watch`).
- **kato validates inputs.** Missing/invalid inputs return `400` with an
  `InputError` message; a UseCase that failed validation is `ready:false` and `/run`
  returns `422`; the concurrency cap returns `429`.

---

## Decisions (from brainstorming)

1. **Invocation UX:** interactive **pick + form** — the bot lists UseCases, the user
   selects one, a form collects its declared inputs, then it runs. (Not slash+args,
   not natural language.)
2. **First platform:** **Lark**, built end-to-end. Slack + Telegram are later adapters
   on the same core.
3. **Transport / deployment:** **WebSocket long-connection, in-cluster, single replica.**
   The bot dials OUT to Lark (no ingress, no public URL, no TLS to manage) and reaches
   kato over internal Service DNS (`http://kato.kato.svc:8080`).
4. **Access control:** **none.** Open to anyone who can message the bot in Lark. No
   allowlist, no per-user or per-usecase RBAC. Lark membership is the only boundary.
5. **No server-side session state.** The selected UseCase rides inside the card's
   action `value`; Lark returns all form field values on submit. Single replica.
6. **Fast-ack + async result.** A card-action callback never blocks on kato: it
   repaints the card to "running…" synchronously, and a goroutine performs the slow
   `POST /run` and then patches the card with the summary.

---

## Architecture

Ports-and-adapters. A platform-agnostic **core** owns all logic (discover UseCases →
render a picker → collect inputs → call kato → render the result). Thin per-platform
**adapters** own only event-decoding and rendering. Lark is the first adapter.

```
kato-bot (Go, single binary, single replica, in-cluster)

 cmd/kato-bot/main.go     wire config → kato client → core → Lark adapter → run

 internal/kato/           kato REST client: ListUseCases / GetUseCase / Run / GetRun
                          (typed against kato's JSON), maps 400/422/429/5xx to errors

 internal/core/           platform-agnostic orchestration + state machine.
                          Defines the two ports below; imports no platform packages.

 internal/platform/lark/  Lark adapter: larkws long-connection event client
                          (im.message.receive_v1 + card.action.trigger), card
                          builders, message send/patch. Implements core.Renderer.

 internal/config/         env config (LARK_APP_ID/SECRET, KATO_BASE_URL, timeouts)

 charts/kato-bot/         Helm chart: Deployment (1 replica) + Secret + values
```

### Ports (in `internal/core`, no platform imports)

```go
// Reply addresses where to send/update a card. Opaque to core; the adapter fills it
// from the platform event (Lark: ChatID, plus MessageID once a card exists).
type Reply struct {
    ChatID    string
    MessageID string // empty until the first card is sent; set when updating
}

type UseCase   struct{ Name, Description string; Ready bool }
type InputDecl struct{ Name string; Required bool }
type Contract  struct{ Name, Description string; Inputs []InputDecl }
type RunResult struct{ Run, Phase, Summary, Warning string; Err error }

// Renderer = outbound port. The Lark adapter implements it.
type Renderer interface {
    RenderPicker(ctx context.Context, r Reply, ucs []UseCase) error
    RenderForm(ctx context.Context, r Reply, c Contract, prefill map[string]string, formErr string) error
    RenderRunning(ctx context.Context, r Reply, useCase string, inputs map[string]string) error
    RenderResult(ctx context.Context, r Reply, useCase string, inputs map[string]string, res RunResult) error
}

// Intent = inbound. The adapter decodes raw platform events into one of these.
type Intent interface{ isIntent() }
type ListUseCases struct{ Reply Reply }
type PickUseCase  struct{ Reply Reply; Name string }
type SubmitForm   struct{ Reply Reply; Name string; Inputs map[string]string }

// KatoClient is the kato REST surface the core depends on (interface for testing).
type KatoClient interface {
    ListUseCases(ctx context.Context) ([]UseCase, error)
    GetUseCase(ctx context.Context, name string) (Contract, error)
    Run(ctx context.Context, name string, inputs map[string]string) (RunResult, error)
}

type Core struct{ Kato KatoClient; R Renderer }

// Handle does all synchronous work and returns a non-nil `deferred` ONLY for the
// slow path (a validated SubmitForm): the caller (adapter) runs `deferred` in a
// goroutine after returning its platform callback. deferred is nil for every other
// intent. This keeps the core synchronous and unit-testable (a test calls deferred
// directly) while the adapter owns goroutine spawning and the fast-ack contract.
func (c *Core) Handle(ctx context.Context, in Intent) (deferred func(context.Context) error, err error)
```

### `Core.Handle` — the state machine

- `ListUseCases` → `Kato.ListUseCases()` → `R.RenderPicker`; returns `deferred=nil`.
- `PickUseCase{name}` → `Kato.GetUseCase(name)` → `R.RenderForm(prefill=nil, formErr="")`;
  returns `deferred=nil`.
- `SubmitForm{name, inputs}` → validate that every `required` input is non-empty.
  - missing → `R.RenderForm(c, prefill=inputs, formErr="<which fields>")`; `deferred=nil`.
  - ok → `R.RenderRunning(...)` synchronously, then returns a non-nil `deferred` that
    runs `Kato.Run(name, inputs)` → `R.RenderResult(...)`. The adapter calls
    `Handle`, returns its platform callback (the "running" repaint is already drawn),
    then `go deferred(bgCtx)` with `bgCtx = context.WithTimeout(KATO_RUN_TIMEOUT)`.

Error mapping for `RenderResult`: `Kato.Run` returns a `RunResult` with `Err` set on
transport/5xx/timeout/429; the renderer shows a friendly message + "Run again". kato
`400` InputError on submit is surfaced as a form error where possible.

---

## User flow (Lark, v1)

0. **Entry.** User triggers the bot in a group/DM via `@kato-bot` or the keyword
   `kato`. Decoded to `ListUseCases`.
1. **Picker card.** A card listing each UseCase (name + description + `Select` button).
   Dynamic — whatever exists in the cluster. Each button: `value:{action:"pick",usecase:NAME}`.
2. **Form card.** On `Select`, the same card repaints into a form: one input element
   per declared input, required ones flagged `*`, with `Back` and `Run` buttons.
   `Run` button: `value:{action:"run",usecase:NAME}`; Lark delivers the field values
   in `action.form_value` on submit.
3. **Running card.** On `Run`, after required-input validation passes, the card
   immediately repaints to "⏳ Running NAME… (up to ~30s)". This repaint is the
   synchronous callback response.
4. **Result card.** When `POST /run` returns, the card is patched: headline = `phase`
   (✅ Completed / ❌ Failed) + `warning` if any; body = the LLM `summary` as markdown;
   footer = the `run` name (for audit) + a `Run again` button (jumps to step 2 for the
   same UseCase).

**Edge cases surfaced in the card:** kato `400` (bad inputs) → form error; `422`
(usecase not Ready) → "failed validation in cluster"; `429` → "kato is busy, try
again"; transport/5xx/timeout → "couldn't reach kato"; each with `Run again`.

**Out of v1 (fast-follows):** rendering per-step raw `steps[]` behind an expander;
natural-language invocation; Slack + Telegram adapters.

---

## Lark adapter specifics

Uses the official `github.com/larksuite/oapi-sdk-go/v3`.

- **Event client:** `larkws` long-connection (dial-out). Dispatcher handles:
  - `im.message.receive_v1` → if the text triggers the bot (`kato` / mention) → `ListUseCases`.
  - `card.action.trigger` → read `action.value.action`: `"pick"` → `PickUseCase`;
    `"run"` → `SubmitForm` (inputs from `action.form_value`). The callback's
    synchronous return carries the card update for the repaint.
- **Renderer:** builds card JSON. Picker = section + button per UseCase. Form = a Lark
  **form container** with `input` elements (one per `InputDecl`) + Back/Run buttons.
  Running/Result = message patch via `im.v1.message.patch` using `Reply.MessageID`.
- **Reply addressing:** the first card (from `ListUseCases`) is sent with
  `im.v1.message.reply` to the user's message; the returned `message_id` is captured
  into `Reply.MessageID` and threaded through the card `value` so later steps patch the
  same message and the card morphs in place.

---

## Concurrency & fast-ack contract

- The Lark `card.action.trigger` handler must return quickly (well under the platform
  callback timeout). It calls `Core.Handle`, which draws the "running" repaint
  synchronously and returns a non-nil `deferred`; the handler returns its callback
  response immediately, then runs `go deferred(bgCtx)`.
- That `deferred` is the slow `Kato.Run` + `RenderResult`, run with a
  `context.WithTimeout(KATO_RUN_TIMEOUT)` independent of the callback context.
- Single replica ⇒ no cross-instance coordination. A bounded worker/semaphore caps
  in-flight runs so a flood of clicks can't exhaust resources; over the cap, the card
  repaints to "kato is busy, try again" (mirrors kato's own `429`).

---

## Config (`internal/config`, env)

| var | default | meaning |
|---|---|---|
| `LARK_APP_ID` | — (required) | Lark app id |
| `LARK_APP_SECRET` | — (required) | Lark app secret (from a k8s Secret) |
| `KATO_BASE_URL` | `http://kato.kato.svc:8080` | kato REST base URL |
| `KATO_RUN_TIMEOUT` | `60s` | client timeout for `POST /run` |
| `LOG_LEVEL` | `info` | log verbosity |

---

## Testing strategy

Mirrors kato's conventions: table tests, `httptest`, no network or live cluster.

- `internal/kato` — client tested against an `httptest` server returning kato's JSON
  shapes, including `400`/`422`/`429`/`5xx` → asserts typed error mapping.
- `internal/core` — `Handle` tested with a **fake Renderer** (records calls) and a
  **fake KatoClient**: asserts the picker→form→running→result sequence, required-input
  validation (missing field repaints the form with an error, no run), and error
  mapping. This is where the real logic coverage lives, platform-independent.
- `internal/platform/lark` — card-builder functions tested by asserting produced card
  JSON; event-decoder tested by feeding sample `card.action.trigger` / message payloads
  and asserting the emitted `Intent`.

---

## Non-goals (explicit)

- **Auth / RBAC / allowlists** — none; Lark membership is the boundary.
- **Slack & Telegram adapters** — designed for, not built in v1.
- **Natural-language invocation** — pick+form only.
- **Rendering raw step outputs** — summary-first; `steps[]` expander is a fast-follow.
- **Multi-replica / webhook ingress** — single replica, dial-out only.
- **Persisting bot-side state** — stateless; kato already persists every `Run`.

---

## Implementation notes

- New repo `github.com/zufardhiyaulhaq/kato-bot` (currently an empty git repo).
- The core depends only on the `KatoClient`, `Renderer`, and `Intent` types — no Lark
  imports — so adding Slack/Telegram means a new `internal/platform/<x>` implementing
  `Renderer` + decoding events into `Intent`, with zero core changes.
- Helm chart deploys a single-replica Deployment with the Lark secret mounted; no
  Service/Ingress needed (dial-out only). A `/healthz` for liveness is optional since
  there is no inbound traffic; if added, it's a tiny `http` listener for k8s probes.
{% endraw %}