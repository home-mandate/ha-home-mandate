# Home-Mandate

Mandates for AI agents in Home Assistant: every agent gets its own identity and clear limits.
Actions are allowed, sent to your phone for confirmation, or forbidden, and every request is
logged.

**Status:** in development.

## Repository layout

| Path | Contents |
|---|---|
| `docs/ARCHITECTURE.md` | Architecture v0.1: gateway, local UI, flows, decisions |
| `docs/TESTING.md` | Test strategy: unit, negative, fuzzing, E2E, UI, i18n, coverage thresholds |
| `SECURITY.md` | Reporting vulnerabilities, threat model |
| `app/config.yaml` | Draft of the Home Assistant app configuration |
| `Dockerfile` | Multi-stage build: UI (Vite) → Go binary with embedded UI |

Planned code structure: `cmd/home-mandate` (gateway), `cmd/relay` (cloud relay),
`internal/…` (see architecture), `web/` (local UI, Svelte + Vite), `e2e/`.

## Development

Requirements: Go 1.27.1, Node 24 LTS with corepack (`corepack enable pnpm`).

```bash
make check                  # vet, staticcheck, race tests, coverage per package, govulncheck, actionlint
make web-install web-check  # UI: lint, svelte-check, Vitest, i18n checks, build, pnpm audit
make web-e2e                # Playwright in German and English under a random Ingress path
make webui build            # binary with the embedded UI (without webui: a placeholder page)
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
| `HM_TLS_CERT`, `HM_TLS_KEY` | Certificate for the MCP endpoint and the UI (TLS 1.3); without it, MCP listens on localhost only. Renewed files are taken over without a restart; the certificate must cover the host of `HM_PUBLIC_URL` |
| `HM_MCP_ADDR` | Listen address of the MCP endpoint, default `:8765` with TLS, `127.0.0.1:8765` without |
| `HM_PDP_ADDR` | Optional loopback address for the AuthZEN evaluation endpoint, for other gateways on the same host |
| `HM_PUBLIC_URL` | Origin agents and browsers reach Home-Mandate at, e.g. `https://hm.example.org:8765` (`http://` only for `localhost`). Without it, OAuth is off and no agent can be admitted |
| `HM_HA_BROWSER_URL` | Home Assistant as the human's browser reaches it, for signing in; default: the origin of `HM_HA_URL` |
| `HM_APPROVAL_TIMEOUT` | Upper limit in seconds for waiting for an approval, 30–600, default 120; a mandate may only shorten it |
| `HM_LOG_LEVEL` | `debug`, `info`, `warning` or `error` |
| `HM_INGRESS_ADDR` | Optional listen address of the UI, e.g. `:8099`, for a proxy that does what Home Assistant's Supervisor does (signs people in, sets `X-Remote-User-Id`, removes client copies of it), and for the E2E tests. Not needed for the UI in direct mode (below) |
| `HM_INGRESS_PROXY` | Required with `HM_INGRESS_ADDR`: the one IP address of that proxy. Requests from any other address get nothing; the user must be a Home Assistant administrator |

Agents connect to `https://<host>:8765/mcp` with an OAuth access token.

`docs/deploy/compose.yaml` is an example next to Home Assistant Container on the same host
(`make image` builds the image). The certificate must be valid for the host of
`HM_PUBLIC_URL`; Home-Mandate looks at the files once a minute and takes a renewed pair over
without a restart.

## The local UI

In app mode (Home Assistant OS) the UI is in Home Assistant's sidebar through Ingress. Every
Home Assistant user can open Ingress panels, so Home-Mandate checks each request itself: it
must come from the Supervisor, and the user must be a Home Assistant administrator at that
moment (asked every 30 seconds; if Home Assistant cannot answer, nobody is let in). See
`docs/ARCHITECTURE.md`, sections 8 and 12.

In container mode with a certificate and `HM_PUBLIC_URL`, the UI is at
`https://<host>:8765/ui/` (direct mode). You sign in with your Home Assistant account;
only administrators get in, and the check is repeated on every request. A session ends
after 30 minutes without use, after 12 hours, on sign-out and with every restart. Without
a certificate there is no UI in container mode, only the command line.

## Admitting agents

Agents find everything through `https://<host>:8765/.well-known/oauth-protected-resource/mcp`
(RFC 9728). There is no open registration; a human of the household admits every agent. The
human signs in with their Home Assistant account (administrators only), gives the agent a
name and picks a mandate template.

- **Agents with a browser** use Authorization Code with PKCE. They identify themselves with a
  Client ID Metadata Document: an `https://` URL on a public address that names the agent's
  redirect URIs.
- **Agents without a browser** use a pairing code (Device Authorization Grant): the agent
  shows a code like `BCDF-GHJK`, the human opens `https://<host>:8765/pair`, signs in and
  enters it. Five wrong codes lock the session, thirty within ten minutes lock pairing for
  everyone for ten minutes.

- **Local MCP clients without OAuth of their own** (Claude Desktop with a local server
  entry, other stdio clients) connect through [`mcp-remote`](https://github.com/geelen/mcp-remote),
  which runs on the same computer and signs in with Authorization Code and PKCE. Home-Mandate
  publishes a Client ID Metadata Document for it at
  `https://home-mandate.com/clients/mcp-remote.json` (redirect to
  `http://127.0.0.1:33418/oauth/callback`, which `mcp-remote` uses, or
  `http://localhost:33418/oauth/callback`; a loopback redirect may change its port but not
  its host). Example for Claude Desktop
  (`claude_desktop_config.json`, Node.js 18 or later):

  ```json
  {
    "mcpServers": {
      "home-mandate": {
        "command": "npx",
        "args": ["-y", "mcp-remote@0.14.3", "https://hm.example.org:8765/mcp", "33418",
                 "--client-metadata-url", "https://home-mandate.com/clients/mcp-remote.json"]
      }
    }
  }
  ```

  The browser opens Home-Mandate's sign-in; an administrator admits the agent as with any
  other. The tokens stay on that computer, with `mcp-remote`.

Access tokens are valid for 10 minutes, refresh tokens for 30 days; every refresh token can
be used once, and presenting a used one again revokes all tokens of that admission.

## Approval requests

Actions with the decision `ask` wait for a human. Home-Mandate sends a notification with
"Allow" and "Deny" to every device of every approver of the mandate who is set up here; the
answer must come from that approver's Home Assistant account. No answer within the timeout,
an answer from anyone else, or no reachable approver means deny. An answer from someone who
may not approve also warns the approvers. The agent's reason is shown as its claim, never as
a fact. The first answer counts.

```bash
home-mandate approver add USER_ID mobile_app_pixel_9,mobile_app_mac:no-critical [de|en]   # up to 5 devices
```

`USER_ID` is the Home Assistant user ID; it must also be listed in the mandate's
`approvers`. Without a language, the language of the Home Assistant configuration applies.
Any device with the Home Assistant Companion App counts, including the Mac app. Critical
actions (unlocking a door, disarming the alarm …) go only to devices without
`:no-critical`: an iPhone asks for unlocking before a button counts, the Mac app and Android
do not. The UI proposes `no-critical` for the Mac app.

Approvers who are Home Assistant administrators can additionally answer in the Home-Mandate
UI; this is switched on per person in the UI, for critical actions separately, because a
browser session asks for no unlocking the way a phone does.

## Administration on the command line

The administration commands work on the local database only; they are not reachable over
the network and need no Home Assistant credentials, only `HM_DATA_DIR`. Run them inside the container, e.g. `docker exec -i home-mandate /home-mandate …`.

```bash
home-mandate household                       # principal to use in mandates
home-mandate mandate template import NAME template.json   # templates humans pick when admitting
home-mandate mandate template list | remove NAME
home-mandate agent list | revoke CLIENT_ID   # revoking takes effect with the next request
home-mandate mandate import mandate.json     # or - for stdin; validated against mandate-spec
home-mandate mandate list | revoke ID
home-mandate mandate check                   # lists stored mandates and templates the evaluator rejects, e.g. after an update
home-mandate approver add USER_ID NOTIFY_SERVICE[:no-critical][,…] [de|en] | list | remove USER_ID
home-mandate emergency-stop on | off | status   # on: all tokens revoked, all agents blocked
home-mandate audit verify | export           # hash chain and checkpoint check, JSON Lines export
home-mandate audit key                       # log ID and public key of the checkpoints; keep them outside this device
```

A template is a mandate whose `id`, `principal`, `agent`, `created_by`, `created_at`,
`valid_from` and `expires` are filled in when an agent is admitted.

## Limits of the current development version

- Camera snapshots and `set` on entities of category `other` are evaluated and logged but
  not executed: Home Assistant offers no safe way to perform them for one entity.
- Reading a device with the decision `ask` is refused; only actions are confirmed by a human.
- A mandate's approval timeout is capped by `HM_APPROVAL_TIMEOUT` (at most 10 minutes),
  although the specification allows up to one hour: the agent's request waits for the answer.
- Open approval requests live in memory: after a restart they are gone and their requests
  have ended without execution.
- In app mode (Home Assistant OS), admitting agents is not available yet.
- Changes to mandate templates and approvers are local settings: the specification has no
  audit event for them, so they do not appear in the audit log.
- In container mode, the UI needs a certificate and an `https://` public URL (direct mode);
  without them, only the command line manages Home-Mandate.
- There is no test clock: time windows are tested against the real household time (E2E
  scenario 9) and at their boundaries by unit tests.

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
