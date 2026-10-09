# Home-Mandate as a Home Assistant OS app

This guide installs Home-Mandate as an app (formerly "add-on") on Home Assistant OS, sets
it up for agents, and covers updates, backups and removal. For Home Assistant Container,
see [install-container.md](install-container.md) instead.

All names and addresses below are placeholders: `hm.example.org` for Home-Mandate,
`ha.example.org` for Home Assistant, `192.0.2.x` for addresses in your network.

## Requirements

- **Home Assistant OS** on `amd64` or `aarch64` (for example a Raspberry Pi 4 or 5 with a
  64-bit system, a Home Assistant Green or Yellow, or a virtual machine).
  Home Assistant Supervised is not supported or tested. Home Assistant Container and Core
  have no app store; use [container mode](install-container.md) with Home Assistant
  Container.
- **Home Assistant 2026.9.0 or later** (the app declares this minimum).
- **The Home Assistant Companion app** on the phone of everyone who should answer approval
  requests, signed in with that person's own Home Assistant account.
- For agents outside your Home Assistant host, which is nearly every agent: a **TLS
  certificate** in `/ssl` for a name that points to your Home Assistant host, for example
  from the Let's Encrypt or DuckDNS app. Without it, Home-Mandate's MCP endpoint answers on
  localhost only (see [The certificate](#the-certificate)).

This is an experimental release candidate. The app is first tested on real Home Assistant
OS installations with this release; please report what does not work.

## Install

1. In Home Assistant, go to **Settings → Apps → App store**, open the menu **⋮** in the top
   right corner, choose **Repositories** and add
   `https://github.com/home-mandate/ha-home-mandate`.
2. Find **Home-Mandate** in the app store (reload the page if it does not show up yet) and
   choose **Install**.
3. Before you start it, look at the **Configuration** tab (see [Options](#options)). The
   defaults are enough to try the UI; admitting agents needs `public_url` and
   `ha_browser_url`.
4. **Start** the app and look at its **Log** tab. A good start logs, as JSON lines, among
   others:
   - `connected to home assistant`
   - `MCP endpoint listening` with `"tls":true` and `"addr":":8765"` once a certificate is
     there, or `"tls":false` on `127.0.0.1:8765` without one
   - `UI listening for Ingress`
   - `home-mandate started` with the version and `"mode":"app"`

   Without `public_url` it also warns `no public URL configured: OAuth is off, agents
   cannot be admitted`. That is expected until you set it.
5. Turn on **Show in sidebar** and open **Home-Mandate** from the sidebar.

Only Home Assistant administrators get into the UI. Home Assistant's Ingress lets every
signed-in user reach an app's panel, so Home-Mandate checks every request itself: it must
come from the Supervisor, and the user must be an administrator at that moment. Others see
"No access".

Continue with [usage.md](usage.md#first-steps) to set up approvers and a mandate, then
[agents.md](agents.md) to connect an agent.

## Options

The **Configuration** tab of the app has these options. A change needs a restart of the
app; invalid values stop the start with a message in the log.

| Option | Shown as | Default | When you need it |
|---|---|---|---|
| `tls_certfile` | TLS certificate | `fullchain.pem` | File name in `/ssl` of the certificate chain for the MCP endpoint. Must cover the host of `public_url`. Plain file name only, no path. |
| `tls_keyfile` | TLS private key | `privkey.pem` | File name in `/ssl` of the certificate's private key. |
| `public_url` | Public URL | empty | To admit agents. The address agents and browsers reach Home-Mandate at, see below. Without it, OAuth is off and no agent can be admitted. |
| `ha_browser_url` | Home Assistant URL for your browser | empty | Required together with `public_url`. Home Assistant as your browser reaches it, see below. |
| `approval_timeout_seconds` | Approval timeout | `120` | How long an action waits for an answer to an approval request, 30 to 600 seconds. A mandate may only shorten it. |
| `log_level` | Log level | `info` | `debug`, `info`, `warning` or `error`. |

### `public_url`

The origin (scheme, host and port, nothing else) agents and your browser use to reach
Home-Mandate's MCP endpoint. It becomes the OAuth issuer and the address of the MCP
endpoint, `<public_url>/mcp`, so it must be exactly what agents type.

- Agents connect to the app directly (port forward or local network): include the port,
  `https://hm.example.org:8765`.
- Agents connect through a reverse proxy that passes TLS through on port 443: the proxy's
  address without a port, `https://hm.example.org`.

It must be `https://` (plain `http://` is accepted for `localhost` only). The certificate
must cover its host name; otherwise Home-Mandate does not start.

### `ha_browser_url`

To admit an agent, an administrator signs in with their Home Assistant account. Home-Mandate
sends the browser to Home Assistant for that, so it needs Home Assistant's address **as
your browser reaches it**, for example `https://ha.example.org:8123` or
`http://192.0.2.10:8123`. If you admit agents from away from home, use an address that
works from there.

Behind the scenes Home-Mandate exchanges the sign-in with Home Assistant over the
Supervisor's internal network. At every start it asks the Supervisor on which port and
with or without TLS Home Assistant serves (this is what the app's `hassio_api` permission
is for, read-only information). So a Home Assistant on another port than 8123, or with its
own certificate (`ssl_certificate` in the `http:` configuration, for example DuckDNS),
needs nothing extra, with one rule:

- **If Home Assistant serves TLS itself**, the host of `ha_browser_url` must be the name
  its certificate is issued for (`https://ha.example.org:8123`, not an IP address).
  Home-Mandate checks Home Assistant's certificate against that name with the public
  certificate authorities; a self-signed certificate does not work in the app.

Honest status: the case of Home Assistant serving its own TLS is covered by unit tests
only so far, not yet by a test on a real installation.

## The certificate

Agents only connect over HTTPS, and Home-Mandate never serves plaintext beyond the local
machine: anyone in your network could otherwise read the agents' access tokens. The app
reads its certificate from Home Assistant's `/ssl` folder (read-only).

- The Let's Encrypt and DuckDNS apps write `fullchain.pem` and `privkey.pem` there, which
  are the defaults of the options.
- The certificate must be issued for the host of `public_url`. A certificate that does not
  cover it stops the start (`certificate does not cover the public host`).
- Without a certificate file, the app starts anyway, logs `no TLS certificate in /ssl, MCP
  endpoint only on localhost`, and no agent outside the Home Assistant host can connect.
  The UI then shows "No TLS certificate".
- Renewed files are taken over without a restart: Home-Mandate looks at them once a minute
  and switches when the new pair belongs together, is valid and covers the host. From 14
  days before expiry the UI warns.

## Reaching Home-Mandate from outside

The UI is in Home Assistant's sidebar and reachable wherever Home Assistant is, Home
Assistant Cloud included.

Agents talk to the **MCP endpoint on port 8765**, which is not part of Home Assistant's
web server. Home Assistant Cloud does not carry it. You have these options:

| Way | `public_url` | What you set up |
|---|---|---|
| Local network only | `https://hm.example.org:8765` | A DNS name that points to your Home Assistant host in your network, and a certificate for it. |
| Port forward | `https://hm.example.org:8765` | Forward TCP 8765 on your router to the Home Assistant host; the name resolves to your public address. |
| Reverse proxy that **passes TLS through** | `https://hm.example.org` | The proxy routes by name (TLS SNI) to port 8765 and leaves TLS to Home-Mandate, see [way B in reverse-proxy.md](deploy/reverse-proxy.md#way-b-the-proxy-passes-tls-through-traefik). |

A reverse proxy that **ends TLS** itself (such as the NGINX SSL proxy app or most proxy
setups) is not possible in the app yet: the setting for it (`HM_PROXY`) exists only in
container mode.

You can change the host port of 8765 in the app's **Network** settings; then `public_url`
carries that port.

Signing in to admit an agent also sends the browser to Home Assistant at
`ha_browser_url`, so admitting from outside needs Home Assistant reachable from outside
as well.

**IPv4 and claude.ai.** Many internet connections have no public IPv4 address of their own
(carrier-grade NAT, DS-Lite). In our experience, claude.ai and the Claude apps connect to
custom connectors over IPv4 only, so they cannot reach such a home; agents that run in your
network or reach you over IPv6 are not affected. See [agents.md](agents.md#claudeai-and-the-claude-apps).

## Security of the app

- The UI answers only the Supervisor and only Home Assistant administrators.
- The app runs as root inside its container, like all official apps, because the
  Supervisor creates `/data`, the options file and the key in `/ssl` for root only. The
  image contains nothing but the binary and CA certificates (no shell, no package
  manager), and Home-Mandate writes only to `/data`.
- An AppArmor profile limits the app to what it needs: its binary, `/data`, reading `/ssl`,
  network sockets and a few system files.
- The image is signed with cosign; the release notes on GitHub show how to verify it.

## Backups and restore

Home Assistant backups include Home-Mandate's data: the database with mandates, agents,
tokens (as hashes) and the audit log, and the key that signs the audit checkpoints. The
app is stopped for the few seconds of the backup (a "cold" backup), so that the database
is copied consistently; agents get no answer during that time.

To restore, use Home Assistant's restore of a full backup or of the Home-Mandate app from
a partial backup. After a restore:

- Mandates, agents and the audit log are as they were at the time of the backup. Agents
  admitted later are gone and must be admitted again; tokens issued later no longer work.
- The audit log continues from the restored state; `Audit log → Verify now` should show
  "Audit log complete and unaltered".
- Restore the database and the key together (a Home Assistant backup does). A log with
  checkpoints and without its key refuses to start.

## Updates

Home Assistant shows an update for Home-Mandate when a new version is published.

1. Read the changelog shown in the update dialog ([app/CHANGELOG.md](../app/CHANGELOG.md)).
2. Let Home Assistant create a backup of the app before the update (offered in the update
   dialog).
3. Update. Database changes are applied automatically at the first start of the new
   version. Going back to an older version needs the backup: an older version refuses a
   database that a newer one has changed (`database is newer than this binary`).
4. Check the log and the overview page. Open approval requests do not survive the restart.

## Uninstall

1. Optionally, trigger the emergency stop first, so that no agent acts while you remove it.
2. **Settings → Apps → Home-Mandate → Uninstall.** The Supervisor removes the app's data
   with it (mandates, agents, audit log, checkpoint key); create a backup first if you may
   want it back.
3. Remove the repository under **Settings → Apps → App store → ⋮ → Repositories** if you
   no longer need it.
4. Remove the agents' configuration on their side (MCP server entries, connectors); their
   tokens stop working with the app.
5. Close the port forward or proxy route for port 8765 if you made one.

Approval requests and checkpoint notifications already delivered stay on the phones; they
contain no credentials.
