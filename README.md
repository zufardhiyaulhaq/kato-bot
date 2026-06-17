# kato-bot

A Lark chat adapter for [kato](https://github.com/zufardhiyaulhaq/kato). Invite the bot
to a Lark group, message it, pick a troubleshooting UseCase, fill in the inputs, and it
runs kato and posts the summary back — all over a WebSocket long-connection (no ingress).

## How it works

```
Lark group ──ws──> kato-bot ──REST──> kato (in-cluster)
```

1. Message the bot → it shows a card listing kato UseCases.
2. Pick one → the card becomes a form of that UseCase's inputs.
3. Submit → the card shows "running…", then the LLM summary kato produced.

Access is governed entirely by Lark group membership; kato-bot adds no auth (kato is
read-only). v1 supports Lark; Slack and Telegram are planned on the same core.

## Configuration (env)

| var | default | meaning |
|---|---|---|
| `LARK_APP_ID` | (required) | Lark app id |
| `LARK_APP_SECRET` | (required) | Lark app secret |
| `KATO_BASE_URL` | `http://kato.kato.svc:8080` | kato REST base URL |
| `KATO_RUN_TIMEOUT` | `60s` | per-run client timeout |
| `LOG_LEVEL` | `info` | log verbosity (`debug`/`info`/`warn`/`error`) |
| `MAX_CONCURRENT_RUNS` | `4` | cap on in-flight kato runs before submits get a "busy" card |
| `HEALTH_ADDR` | `:8080` | address for the `/healthz` + `/readyz` probe server |

To run locally, copy `.env.example` to `.env`, fill it in, and `set -a; source .env; set +a`
before `go run ./cmd/kato-bot` (the binary reads the environment; it does not auto-load `.env`).

## Deploy

```bash
helm install kato-bot charts/kato-bot -n kato \
  --set lark.appId=$LARK_APP_ID \
  --set lark.appSecret=$LARK_APP_SECRET
```

## Lark app setup

- Enable bot capability; add the bot to a group.
- Grant scopes: `im:message`, `im:message:send_as_bot` (send/reply/patch messages).
- Event subscription: **Use long connection (WebSocket)**; subscribe to
  `im.message.receive_v1` and enable card callbacks (`card.action.trigger`) over the
  long connection.
