# kato-bot

Lark chat adapter for kato troubleshooting flows

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square) [![made with Go](https://img.shields.io/badge/made%20with-Go-brightgreen)](http://golang.org) [![Github main branch build](https://img.shields.io/github/actions/workflow/status/zufardhiyaulhaq/kato-bot/main.yml?branch=main)](https://github.com/zufardhiyaulhaq/kato-bot/actions/workflows/main.yml) [![GitHub issues](https://img.shields.io/github/issues/zufardhiyaulhaq/kato-bot)](https://github.com/zufardhiyaulhaq/kato-bot/issues) [![GitHub pull requests](https://img.shields.io/github/issues-pr/zufardhiyaulhaq/kato-bot)](https://github.com/zufardhiyaulhaq/kato-bot/pulls)

> A Lark chat adapter for [kato](https://github.com/zufardhiyaulhaq/kato). Invite the bot
> to a Lark group, message it, pick a troubleshooting UseCase, fill in the inputs, and it
> runs kato and posts the summary back — all over a WebSocket long-connection (no ingress).

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

The container is configured entirely through environment variables (the Helm values
below set them on the Deployment):

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

## Lark app setup

- Enable bot capability; add the bot to a group.
- Grant scopes: `im:message`, `im:message:send_as_bot` (send/reply/patch messages).
- Event subscription: **Use long connection (WebSocket)**; subscribe to
  `im.message.receive_v1` and enable card callbacks (`card.action.trigger`) over the
  long connection.

## Installing

Install from the chart sources in this repo:

```console
helm install kato-bot charts/kato-bot -n kato \
  --set lark.appId=$LARK_APP_ID \
  --set lark.appSecret=$LARK_APP_SECRET
```

To use a pre-existing Secret instead of letting the chart create one, create a Secret
with `LARK_APP_ID` and `LARK_APP_SECRET` keys and point the chart at it — `appId`/`appSecret`
are then not required:

```console
kubectl -n kato create secret generic my-lark-creds \
  --from-literal=LARK_APP_ID=$LARK_APP_ID \
  --from-literal=LARK_APP_SECRET=$LARK_APP_SECRET

helm install kato-bot charts/kato-bot -n kato --set lark.existingSecret=my-lark-creds
```

Or from the packaged chart repository:

```console
helm repo add kato-bot https://zufardhiyaulhaq.com/kato-bot/charts/releases/
helm install my-kato-bot kato-bot/kato-bot --values values.yaml
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/zufardhiyaulhaq/kato-bot"` | Container image repository. |
| image.tag | string | `""` | Image tag. Defaults to the chart appVersion when empty. |
| katoBaseUrl | string | `"http://kato.kato.svc:8080"` | kato REST API base URL (in-cluster Service DNS). |
| katoRunTimeout | string | `"60s"` | Per-run client timeout for kato's synchronous POST /run (Go duration). |
| lark.appId | string | `""` | Lark app id. Required unless lark.existingSecret is set. |
| lark.appSecret | string | `""` | Lark app secret. Required unless lark.existingSecret is set. |
| lark.existingSecret | string | `""` | Name of a pre-existing Secret holding LARK_APP_ID and LARK_APP_SECRET. When set, the chart references it and does NOT create its own Secret (appId/appSecret ignored). |
| logLevel | string | `"info"` | Lark SDK log verbosity (debug / info / warn / error). |
| maxConcurrentRuns | int | `4` | Max in-flight kato runs before new submits get a "kato is busy" card. |
| nodeSelector | object | `{}` | Node selector for pod scheduling. |
| resources | object | `{}` | Pod resource requests and limits. |
| tolerations | list | `[]` | Tolerations for pod scheduling. |

see example files [here](https://github.com/zufardhiyaulhaq/kato-bot/blob/main/charts/kato-bot/values.yaml)

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
