# Connecting agents

How AI agents connect to Home-Mandate, how a human admits them, examples for Claude
Desktop, Claude Code and claude.ai, the tools agents see and the errors they get.

`hm.example.org` stands for your Home-Mandate host. "Public URL" means the option
`public_url` (Home Assistant OS app) or `HM_PUBLIC_URL` (container mode).

## Before you start

- A public URL is set and Home-Mandate serves TLS (its own certificate, or in container
  mode a reverse proxy with `HM_PROXY`). Without a public URL, OAuth is off and no agent
  can be admitted.
- At least one approver is set up, if the mandate you will pick has *Ask first* rules
  ([usage.md](usage.md#1-set-up-approvers)).
- You can sign in to Home Assistant as an administrator from the browser you admit the
  agent with.

## How agents connect

| What | Where |
|---|---|
| MCP endpoint (Streamable HTTP) | `<public URL>/mcp`, for example `https://hm.example.org:8765/mcp` |
| Protected resource metadata (RFC 9728) | `<public URL>/.well-known/oauth-protected-resource/mcp` |
| Authorization server metadata (RFC 8414) | `<public URL>/.well-known/oauth-authorization-server` |
| Authorization, token, device authorization | `/oauth/authorize`, `/oauth/token`, `/oauth/device_authorization` |
| Pairing page for humans | `<public URL>/pair` |

The UI shows the MCP endpoint address under Settings → MCP endpoint and under Agents →
Add agent → With browser sign-in. It is shown only when Home-Mandate has a valid TLS
certificate or runs behind a reverse proxy.

An MCP client that follows the MCP authorization specification needs only the endpoint
URL: its first request without a token gets `401` with a `WWW-Authenticate` header that
points to the resource metadata, and from there it finds the authorization server.

### What Home-Mandate expects from a client

- **OAuth 2.1 with PKCE (S256)** and the resource indicator of the MCP endpoint.
- **A Client ID Metadata Document.** The client's `client_id` is an `https://` URL on a
  public address and port 443, where a small JSON document (at most 5 KB) names the client
  and its redirect URIs. Home-Mandate fetches it to show the human who the agent is and to
  check where the sign-in returns to. Redirect URIs must be `https://`, or `http://` on a
  loopback address (any port).
- **No dynamic client registration.** Home-Mandate has no open registration endpoint; a
  client that can only register itself dynamically cannot connect directly. Use
  `mcp-remote` with a metadata document (below) or a pairing code.

Tokens: access tokens are valid for 10 minutes, refresh tokens for 30 days. Every refresh
token can be used once; presenting a used one again revokes all tokens of that admission.
Revoking the agent or the emergency stop takes effect with its next request.

## Admitting: browser sign-in or pairing code

Every agent is admitted by a Home Assistant administrator. Nobody else can admit one, and
there is no open registration.

**Browser sign-in** (Authorization Code with PKCE) for agents that can open a browser:

1. You enter the MCP endpoint address in the agent.
2. The agent opens Home-Mandate's sign-in in your browser, which sends you to Home
   Assistant (at `ha_browser_url` / `HM_HA_BROWSER_URL`). Sign in as an administrator.
3. The page **Admit an agent** shows the name the agent claims, where its identity was
   checked ("Origin checked: …"), where you return to, the **Mandate** to choose and **Who
   may approve**. Give it a name and choose **Admit**, or **Deny**.
4. The agent receives its tokens directly; Home-Mandate never shows credentials.

**Pairing code** (Device Authorization Grant) for agents without a browser, such as
scripts or local language models:

1. The agent asks Home-Mandate for a code and shows it, for example `BCDF-GHJK` (8
   letters). The code is valid for 10 minutes.
2. You enter it either in the UI (**Agents → Add agent → With a pairing code**) or at
   `<public URL>/pair` after signing in with Home Assistant.
3. Compare the client ID with what your agent shows, choose the mandate and approve.
4. The agent, which asks every 5 seconds, receives its tokens.

Limits against guessing: five wrong codes lock that session; thirty wrong codes within ten
minutes lock pairing for everyone for ten minutes. One address can have at most three
pairings waiting.

A client without a metadata document may use a plain identifier as `client_id` for the
pairing code (lowercase letters, digits, `.`, `_`, `-`, at most 64 characters). It is
then shown to the human as given by the agent and not checked.

## Claude Desktop with mcp-remote

Claude Desktop's local server entries speak MCP over standard input. The
[`mcp-remote`](https://github.com/geelen/mcp-remote) bridge runs on the same computer,
connects to Home-Mandate and does the browser sign-in. Home-Mandate publishes a Client ID
Metadata Document for it at `https://home-mandate.com/clients/mcp-remote.json`, with the
redirect `http://127.0.0.1:33418/oauth/callback` (and `http://localhost:33418/oauth/callback`).

Requirements: Node.js 18 or later on that computer. In `claude_desktop_config.json`:

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

- Replace the URL with your MCP endpoint address.
- `33418` is the local port `mcp-remote` listens on for the sign-in callback. It must match
  the redirect in the metadata document; a loopback redirect may change its port but not
  its host.
- Restart Claude Desktop. The browser opens Home-Mandate's sign-in; admit the agent as
  above. The tokens stay on that computer, with `mcp-remote`.

## Claude Code

The UI shows this example under Agents → Add agent → With browser sign-in:

```sh
claude mcp add --transport http home-mandate 'https://hm.example.org:8765/mcp'
```

Claude Code then signs in through the browser as described above. Whether a given version
of Claude Code identifies itself with a Client ID Metadata Document is up to Claude Code;
the Home-Mandate test suite does not run Claude Code itself.

## claude.ai and the Claude apps

claude.ai and the Claude apps connect to custom connectors from Anthropic's servers on the
internet, not from your network. Add a custom connector with your MCP endpoint address and
admit it in the browser as above. This needs:

- **A public address that the internet reaches**, with a certificate for its name: a port
  forward to Home-Mandate's port, or a reverse proxy
  ([deploy/reverse-proxy.md](deploy/reverse-proxy.md); in the Home Assistant OS app only
  with TLS passthrough). Home Assistant Cloud does not carry the MCP endpoint.
- **IPv4.** In our experience these connections come over IPv4 only. Many internet
  connections have no public IPv4 address of their own (carrier-grade NAT, DS-Lite, common
  with cable and fibre providers); then claude.ai cannot reach your home even if IPv6
  works. We have not verified this against Anthropic's documentation; check your provider
  and your connection before you rely on it.
- A client that identifies itself with a Client ID Metadata Document (see above). If the
  consent page says "The agent could not be identified, or its return address is not
  allowed", the client's metadata could not be fetched or does not list its return
  address.
- The sign-in sends your browser to Home Assistant at `ha_browser_url` /
  `HM_HA_BROWSER_URL`, so that address must work in the browser you admit the agent with.

Approvals: the tool call waits while an approval is pending. If a client gives up
earlier than the approval timeout, the request ends like a timeout: nothing is executed,
and the cooldown applies.

## Other MCP clients

Any MCP client works that speaks Streamable HTTP and OAuth 2.1 with PKCE and a Client ID
Metadata Document. For clients without OAuth support of their own, put `mcp-remote` in
front of them as for Claude Desktop. For your own scripts, implement the Device
Authorization Grant (RFC 8628) against `/oauth/device_authorization` and `/oauth/token`
with `resource` set to the MCP endpoint URL, and show the user code to the human.

## The tools agents see

| Tool | Input | What it does |
|---|---|---|
| `list_devices` | – | Lists the devices the agent may read (decision *allow* for `read`), with `entity_id`, name, category, area and state. |
| `get_state` | `entity_id` | Returns the state and attributes of one device. Attributes that could carry access tokens (pictures, `token=` URLs) are removed. |
| `perform_action` | `entity_id`, `action`, optional `params`, optional `reason` | Performs an action on one device. If a human must confirm, the call waits for the answer. `reason` (up to 1000 characters; the human sees the first 200) is shown to the approver as the agent's unverified claim. On success it returns `{"status": "executed"}`. |
| `list_my_permissions` | – | Lists, for each device the agent may read, the actions that are `allow` or `ask`. |

Devices the agent may not read do not exist for it: they are not listed, and every refusal
on them is `not_found`, exactly as for a device that does not exist.

Actions and their parameters (`params`):

| Category | Actions | Parameters |
|---|---|---|
| `light` | `turn_on`, `turn_off`, `set` | `set`: `brightness_pct` 0–100 and/or `color_temp_kelvin` 1000–12000 |
| `switch` | `turn_on`, `turn_off` | – |
| `climate` | `set_temperature`, `set_mode` | `temperature` (in Home Assistant's unit, required); `hvac_mode`: `off`, `heat`, `cool`, `heat_cool`, `auto`, `dry`, `fan_only` (required) |
| `cover` | `open`, `close`, `stop`, `set_position` | `position` 0–100 (required) |
| `gate` | `open`, `close` | – |
| `lock` | `lock`, `unlock`, `open` | – |
| `alarm` | `arm`, `disarm` | `arm`: `mode` `home`, `away` or `night` (default `away`) |
| `camera` | `snapshot` (evaluated and logged, not executed) | – |
| `media` | `turn_on`, `turn_off`, `play`, `pause`, `set_volume` | `volume_level` 0–1 (required) |
| `scene` | `activate` | – |
| `script` | `run` | – |
| `other` | `set` (evaluated and logged, not executed) | – |

Reading is `get_state`, not an action of `perform_action`.

## Errors agents get

Tool errors are short codes without internal details. Every refusal is in the audit log.

| Error | Meaning | What helps |
|---|---|---|
| `not_found` | The device does not exist, or the agent may not read it. | Check the mandate; the agent cannot tell the two apart by design. |
| `denied: <reason>` | The mandate denies it, for example `denied: no_match` (no rule applies), `denied: rule`, `denied: expired`, `denied: no_mandate`. | Adjust the mandate if the agent should be allowed. |
| `denied: approval_rejected`, `denied: approval_timeout`, `denied: approval_invalid` | A human declined, nobody answered in time, or someone who may not approve answered. | – |
| `denied: approval_cooldown` | The agent asked for the same device recently and was not approved; it must wait (1 minute, doubling up to 1 hour). | Wait, or answer *Allow* to a later request. |
| `denied: approval_pending` | The agent already has 2 approval requests waiting. | Answer or let them end. |
| `denied: no_approver` | Nobody can be asked: no approver with a device (for critical actions: with **Critical requests too**). | Set up approvers ([troubleshooting.md](troubleshooting.md#approvals-do-not-arrive)). |
| `denied: approval_expired`, `denied: mandate_changed` | The approval came, but its validity had ended, or the mandate changed meanwhile. | Ask again. |
| `denied: emergency_stop` | The emergency stop is active. | – |
| `denied: unauthorized` | The agent's access was revoked while it waited. | – |
| `approval_required: reading this device needs a confirmation, which v0.1 does not ask for` | Reading a device with the decision *ask*. | Allow or deny reading in the mandate. |
| `rate_limited` | The mandate's requests per hour are used up (60 per hour for an agent without a usable mandate). | Wait; raise the rate limit of the mandate. |
| `unavailable` | Home-Mandate cannot decide right now: Home Assistant disconnected, its configuration not read yet, the device directory refreshing, or the host clock behind the audit log. | Usually passes within seconds; see [troubleshooting.md](troubleshooting.md#agents-get-unavailable). |
| `invalid_params` (with the parameter) | Malformed entity ID or action, unknown or out-of-range parameter. | Fix the call. |
| `not_supported` | The action is evaluated but cannot be executed in this version (camera snapshot, `set` on `other`). | – |
| `failed` | Home Assistant reported an error or did not answer when executing. | Look at the audit entry ("Home Assistant reports: …"). |

On the HTTP level:

- `401` with `WWW-Authenticate`: no token, or the token is expired, revoked or for
  another resource. The client refreshes or signs in again; after an emergency stop or a
  revocation, an administrator must admit it again.
- `429` with `Retry-After` on the OAuth endpoints and pages: too many sign-ins, pairings or
  token requests from one address (an IPv6 address counts as its /64 network) or from
  everyone together, per minute. Wait the number of seconds in `Retry-After`.
- In container mode behind `HM_PROXY`, an empty `403` means the request did not come from
  the configured proxy address.
