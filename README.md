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
