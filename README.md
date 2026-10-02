# Home-Mandate

Mandates for AI agents in Home Assistant: every agent gets its own identity and clear limits.
Actions are allowed, sent to your phone for confirmation, or forbidden, and every request is
logged.

**Status:** in development, release v0.1 planned for 2026-10-31.

## Repository layout

| Path | Contents |
|---|---|
| `docs/ARCHITECTURE.md` | Architecture v0.1: gateway, local UI, flows, decisions |
| `docs/TASKS-v0.1.md` | Weekly plan up to the release, cut lines |
| `docs/TESTING.md` | Test strategy: unit, negative, fuzzing, E2E, UI, i18n, coverage thresholds |
| `SECURITY.md` | Reporting vulnerabilities, threat model |
| `app/config.yaml` | Draft of the Home Assistant app configuration |
| `Dockerfile` | Multi-stage build: UI (Vite) → Go binary with embedded UI |

Planned code structure: `cmd/home-mandate` (gateway), `cmd/relay` (cloud relay, from 2027),
`internal/…` (see architecture), `web/` (local UI, Svelte + Vite), `e2e/`.

## Development

Requirements: Go 1.27.1, Node 24 LTS with corepack (`corepack enable pnpm`).

```bash
make check                  # vet, staticcheck, race tests, coverage per package, govulncheck, actionlint
make web-install web-check  # UI: lint, svelte-check, Vitest, i18n checks, build, pnpm audit
make web-e2e                # Playwright in German and English under a random Ingress path
```

Checks run against the `mandate-spec` version pinned in `go.mod`. To develop against a
local checkout, create an untracked `go.work` (`go work init . ../mandate-spec`) and pass
`GOWORK=$PWD/go.work` to `make`.

UI dependencies are installed from the lockfile without install scripts and only in
versions published at least seven days ago (`web/pnpm-workspace.yaml`). Playwright's
browsers are not npm packages: `pnpm exec playwright install chromium` downloads them
from Playwright's CDN, for tests only.

## Running in container mode

| Variable | Meaning |
|---|---|
| `HM_HA_URL` | WebSocket API of Home Assistant: `ws://localhost:8123/api/websocket` or `wss://…` (plaintext only to localhost) |
| `HM_HA_TOKEN` or `HM_HA_TOKEN_FILE` | Long-lived token of Home-Mandate's own Home Assistant user |
| `HM_HA_CA_FILE` | Optional PEM file with a CA to trust for `wss://` (self-signed Home Assistant certificate) |
| `HM_DATA_DIR` | Data directory, default `/data` |
| `HM_TLS_CERT`, `HM_TLS_KEY` | Certificate for the MCP endpoint (TLS 1.3); without it, MCP listens on localhost only |
| `HM_MCP_ADDR` | Listen address of the MCP endpoint, default `:8765` with TLS, `127.0.0.1:8765` without |
| `HM_PDP_ADDR` | Optional loopback address for the AuthZEN evaluation endpoint, for other gateways on the same host |
| `HM_LOG_LEVEL` | `debug`, `info`, `warning` or `error` |

Agents connect to `https://<host>:8765/mcp` with a bearer token.

## Administration until the UI exists

The administration commands work on the local database only; they are not reachable over
the network and need no Home Assistant credentials, only `HM_DATA_DIR`. Run them inside the container, e.g. `docker exec -i home-mandate /home-mandate …`.

```bash
home-mandate household                       # principal to use in mandates
home-mandate agent add --name "Voice assistant" [--days 30]   # prints the token once
home-mandate agent list | revoke CLIENT_ID
home-mandate mandate import mandate.json     # or - for stdin; validated against mandate-spec
home-mandate mandate list | revoke ID
home-mandate audit verify | export           # hash chain check, JSON Lines export
```

## Limits of the current development version

- Actions that need a human confirmation (`ask`) are refused until approval requests
  exist (planned for week 3).
- Camera snapshots and `set` on entities of category `other` are evaluated and logged but
  not executed: Home Assistant offers no safe way to perform them for one entity.
- Agents get their tokens from the administration command; OAuth and pairing codes follow.

## Home Assistant permissions

In container mode, Home-Mandate uses a dedicated Home Assistant user with **admin rights**.
The only reason is that Home Assistant allows subscribing to the
`mobile_app_notification_action` event, which carries the answers to approval requests, only
for admins. Home-Mandate sends only a fixed, allowlisted set of WebSocket commands; see
`docs/ARCHITECTURE.md`, section 11.

## Related repositories

| Repository | Contents | License |
|---|---|---|
| `mandate-spec` | Vendor-neutral specification, schema, conformance cases, reference evaluation, test tool | CC BY 4.0 / Apache 2.0 |
| `home-mandate` (this one) | Gateway, local UI, Home Assistant app, relay | AGPL-3.0 |

Home-Mandate embeds the reference evaluation from `mandate-spec` as a Go module and must pass
all conformance cases.
