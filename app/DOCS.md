# Home-Mandate

Home-Mandate stands between AI agents and Home Assistant. An agent gets a **mandate**: which
devices it may read or control, where a human has to approve first, and what it may never
do. Agents connect over MCP with OAuth and never see a Home Assistant token. Every decision
is recorded in an audit log that shows if it was changed afterwards.

This is the first, experimental version.

## Installation

1. Settings → Apps → App store → ⋮ → **Repositories**, add
   `https://github.com/home-mandate/ha-home-mandate`.
2. Install **Home-Mandate** and start it.
3. Open it from the sidebar. Only Home Assistant administrators get in; every request is
   checked again.

## First steps

1. **Approvers:** choose who answers approval requests, and on which phones (Companion app).
   Without approvers, every action that needs approval is denied.
2. **Templates:** three base templates are ready (read only; lights and climate; cautious
   voice assistant). Load one into the editor to adapt it.
3. **Admit an agent:** needs the options below. An administrator signs in through Home
   Assistant, names the agent and picks a template; it becomes the agent's mandate.

## Options

| Option | Meaning |
|---|---|
| `tls_certfile`, `tls_keyfile` | Certificate and key in `/ssl` for the MCP endpoint (TLS 1.3). Must cover the host of `public_url`. Without them, the endpoint answers on localhost only. Renewed files are taken over without a restart. |
| `public_url` | Where agents and browsers reach Home-Mandate, e.g. `https://hm.example.org:8765`. Without it, no agent can be admitted. |
| `ha_browser_url` | Home Assistant as your browser reaches it, e.g. `https://ha.example.org:8123`. Needed with `public_url`. |
| `approval_timeout_seconds` | How long an action waits for an approval, 30–600 seconds. A mandate may only shorten it. |
| `log_level` | `debug`, `info`, `warning` or `error`. |

## Reaching it from outside your home

The UI is in Home Assistant's sidebar and reachable wherever Home Assistant is, Home
Assistant Cloud included.

Agents in the cloud (claude.ai, the Claude apps) connect to the MCP endpoint, port 8765.
Home Assistant Cloud does not carry it. You need a certificate in `/ssl` for a name that
points to your home, and that port reachable from the internet, for example through a
reverse proxy that passes TLS through. See
[Reaching Home-Mandate from outside](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/deploy/reverse-proxy.md).
A proxy that ends TLS itself (`HM_PROXY`) is not available in this app yet.

Many internet connections have no public IPv4 address of their own (carrier NAT, DS-Lite).
Services that connect over IPv4 only, claude.ai among them, cannot reach such a home.

## Backups

Home Assistant backups include Home-Mandate's data. The app is stopped for the few seconds
of the backup, so that its database is copied consistently. Agents get no answer in that
time.

## Security

- The UI answers only Home Assistant's Supervisor and only administrators.
- Agents get short-lived tokens bound to Home-Mandate; an emergency stop or revoking an
  agent takes effect on the next request.
- The image is signed; see the release notes for how to verify it.

Report security issues privately: see
[SECURITY.md](https://github.com/home-mandate/ha-home-mandate/blob/main/SECURITY.md).

## License

AGPL-3.0-or-later. The licenses of all included components are in the UI under
**Settings → About**.
