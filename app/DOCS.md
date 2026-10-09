# Home-Mandate

Home-Mandate stands between AI agents and Home Assistant. An agent gets a **mandate**: which
devices it may read or control, where a human has to approve first, and what it may never
do. Agents connect over MCP with OAuth and never see a Home Assistant token. Approval
requests go to your phone through the Companion app, an emergency stop blocks every agent
at once, and every decision is recorded in an audit log that shows if it was changed
afterwards.

This is an experimental release candidate. Keep backups, and please report what does not
work.

The full documentation is in the repository:
[installation on Home Assistant OS](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/install-ha-os.md),
[using Home-Mandate](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/usage.md),
[connecting agents](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/agents.md),
[troubleshooting](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/troubleshooting.md).

## Requirements

- Home Assistant OS on `amd64` or `aarch64`, Home Assistant 2026.9.0 or later.
  Home Assistant Supervised is not supported.
- The Companion app on the phone of everyone who answers approval requests, signed in with
  their own Home Assistant account.
- For agents: a TLS certificate in `/ssl` (for example from the Let's Encrypt or DuckDNS
  app) for a name that points to your Home Assistant host.

## Installation

1. **Settings → Apps → App store → ⋮ → Repositories**, add
   `https://github.com/home-mandate/ha-home-mandate`.
2. Install **Home-Mandate**, set the options below, and start it.
3. Turn on **Show in sidebar** and open Home-Mandate from the sidebar. Only Home Assistant
   administrators get in; every request is checked again.

The log should show `connected to home assistant`, `MCP endpoint listening` (with
`"tls":true` once the certificate is found) and `home-mandate started`.

## Options

| Option | Meaning |
|---|---|
| `tls_certfile`, `tls_keyfile` | Certificate chain and key, as file names in `/ssl` (default `fullchain.pem`, `privkey.pem`), for the MCP endpoint (TLS 1.3). Must cover the host of `public_url`, or the app does not start. Without the files, the endpoint answers on localhost only. Renewed files are taken over without a restart. |
| `public_url` | Where agents and browsers reach Home-Mandate. Connected directly, with the port: `https://hm.example.org:8765`; behind a reverse proxy that passes TLS through, the proxy's address without a port: `https://hm.example.org`. Without it, no agent can be admitted. |
| `ha_browser_url` | Home Assistant as your browser reaches it, for example `https://ha.example.org:8123`. Required with `public_url`: admitting an agent signs you in through Home Assistant. If Home Assistant serves its own certificate (for example DuckDNS), use the name that certificate is issued for. Home-Mandate asks the Supervisor at start on which port and with which TLS setting Home Assistant serves; nothing else to set. |
| `approval_timeout_seconds` | How long an action waits for an approval, 30 to 600 seconds (default 120). A mandate may only shorten it. |
| `log_level` | `debug`, `info`, `warning` or `error`. |

## First steps

1. **Settings → Approvers:** add the people who answer approval requests and their phones
   (Companion app). Turn on **Critical requests too** only for iPhones and iPads: they ask
   for unlocking before a button counts. Use **Send test**. Without approvers, every action
   that needs approval is denied.
2. **Settings → Critical devices:** mark doors, gates and anything else that needs an
   approval for every action.
3. **Mandates → Templates:** three built-in templates are ready (Read only; Light and
   climate; Voice assistant (cautious)). Load one into the editor and save it under a new
   name to adapt it.
4. **Agents → Add agent:** enter the shown MCP endpoint address in your agent, sign in
   through Home Assistant, check the agent and pick a template; it becomes the agent's
   mandate. Agents without a browser use a pairing code instead.

## Reaching it from outside your home

The UI is in Home Assistant's sidebar and reachable wherever Home Assistant is, Home
Assistant Cloud included.

Agents in the cloud (claude.ai, the Claude apps) connect to the MCP endpoint on port 8765.
Home Assistant Cloud does not carry it. You need a certificate in `/ssl` for a name that
points to your home, and that port reachable from the internet: a port forward, or a
reverse proxy that **passes TLS through**. A proxy that ends TLS itself (such as the NGINX
SSL proxy app) cannot be used for Home-Mandate in this app yet. See
[Reaching Home-Mandate from outside](https://github.com/home-mandate/ha-home-mandate/blob/main/docs/deploy/reverse-proxy.md).

Many internet connections have no public IPv4 address of their own (carrier-grade NAT,
DS-Lite). In our experience claude.ai connects over IPv4 only and cannot reach such a home.

## Backups

Home Assistant backups include Home-Mandate's data: mandates, agents, the audit log and the
key that signs its checkpoints. The app is stopped for the few seconds of the backup, so
that its database is copied consistently; agents get no answer in that time.

## Updates

Let Home Assistant back up the app before you update. Database changes are applied at the
first start of the new version; going back to an older version needs that backup. Open
approval requests end with the restart.

## Uninstall

Uninstalling removes the app's data (mandates, agents, audit log). Take a backup first if
you may want it back, and remove the MCP server entries in your agents.

## Security

- The UI answers only Home Assistant's Supervisor and only administrators.
- Agents get short-lived tokens bound to Home-Mandate; an emergency stop or revoking an
  agent takes effect on the next request.
- The decision is made by fixed rules, never by a language model; the default is deny.
- The image is signed with cosign; the release notes show how to verify it.

Report security issues privately: see
[SECURITY.md](https://github.com/home-mandate/ha-home-mandate/blob/main/SECURITY.md).

## License

AGPL-3.0-or-later. The licenses of all included components are in the UI under
**Settings → About**.
