# Troubleshooting

Problems and their fixes, ordered by when they appear: at start, when connecting an agent,
when signing in, with approvals, and while running.

## Where to look

- **Home Assistant OS app:** the app's **Log** tab. Set the option `log_level` to `debug`
  for more detail.
- **Container mode:** `docker compose logs home-mandate` (or `podman logs home-mandate`).
  Set `HM_LOG_LEVEL=debug` for more detail.
- Errors in the configuration are printed as one line starting with `home-mandate:` before
  the gateway starts; after that the log is JSON, one object per line with `level`, `msg`
  and details.
- The **audit log** in the UI shows every refused request with its reason; the UI's
  banners show what currently needs attention.

Home-Mandate never writes tokens, codes or passwords to its log.

## Start-up errors

Home-Mandate checks its configuration strictly and stops rather than run insecurely. The
message names the fix in most cases.

### Files and permissions (container mode)

| Message (shortened) | Cause | Fix |
|---|---|---|
| `store: insecure permissions: /data is owned by uid 0, but Home-Mandate runs as uid 65532; fix: chown -R 65532:65532 /data …` | The data directory belongs to another user, typically because Docker created it for root or an earlier container ran as root. | On the host: `sudo chown -R 65532:65532 ./data` (the directory mounted at `/data`), then start again. Rootless Podman: `podman unshare chown -R 65532:65532 data`. |
| `store: insecure permissions: /data is writable by group or others; fix: chmod go-w /data` | Others could replace the database. | `sudo chmod 700 ./data` |
| `store: insecure permissions: /data/home-mandate.db has mode 644, want 600; fix: chmod 600 …` | A database file readable by others, for example after a copy. | `sudo chmod 600` on the named file. |
| `… permission denied; Home-Mandate runs as uid 65532 (gid 65532): make the file readable by this user, e.g. chown 65532:65532 on the host file …` | The token file, certificate or key cannot be read by Home-Mandate's user. | `sudo chown 65532:65532` on the named host file (token, `privkey.pem`), mode `600`. Make your ACME client's renewal keep that owner. |
| `config: invalid configuration: HM_HA_TOKEN_FILE is readable by group or others; make it readable by its owner only (chmod 600)` | The token file is too open. Whoever reads it controls your home. | `sudo chmod 600 ha-token` |
| `config: invalid configuration: HM_HA_TOKEN_FILE is not a regular file` | For example a directory, because Docker created the missing file as a directory. | Create the file before the first start, then recreate the container. |
| `config: invalid configuration: HM_HA_TOKEN or HM_HA_TOKEN_FILE is required` / `… not both` | No token, an empty file, or both variables. | Set exactly one of them. |
| `audit checkpoint key …: mode … lets others access it; it must be 0600` / `not owned by the user Home-Mandate runs as` / `not a regular file` | The checkpoint key file (in the data directory or at `HM_AUDIT_KEY_FILE`) is too open, belongs to someone else or is a symbolic link (for example a mounted secret). | `chown 65532:65532` and `chmod 600` on it; mount a regular file, not a link. With `HM_AUDIT_KEY_FILE`, the first start needs its directory writable by Home-Mandate's user, to create the key. |
| `audit checkpoint key … is missing although the audit log has checkpoints: restore the key; a new one could not verify them` | The database was restored or moved without its key. | Restore the key from the same backup. If it is lost for good, the old log cannot be verified any more; see [the audit log is broken](#the-audit-log-is-broken). |
| `running as root; container mode needs no privileges …` (warning) | The container runs as root. | Set `user: "65532:65532"` and hand the data directory over (above). |

### Configuration

| Message (shortened) | Fix |
|---|---|
| `HM_HA_URL must be a ws:// or wss:// URL` | Use `ws://127.0.0.1:8123/api/websocket` (Home Assistant on the same host, host network) or `wss://…/api/websocket`. |
| `home assistant: plaintext connection outside localhost` (in `cannot start`) | `ws://` to anything but loopback is refused: on your network, Home-Mandate's token would travel readable. Run Home-Mandate in the host network and use `127.0.0.1`, or connect with `wss://`. |
| `public URL: plaintext http only for localhost; use https` | `HM_PUBLIC_URL` / `public_url` must be `https://` unless it is `localhost`. |
| `public URL: path not allowed` (or query, fragment, user info) | Give the origin only: `https://hm.example.org:8765`, no `/mcp`. |
| `without TLS or HM_PROXY the MCP endpoint may only listen on loopback` | Set a certificate (`HM_TLS_CERT`, `HM_TLS_KEY`) or `HM_PROXY`, or listen on `127.0.0.1`. |
| `HM_TLS_CERT and HM_TLS_KEY must both be absolute paths or both be unset` | Set both, as paths inside the container. |
| `HM_PROXY must be one IP address` | One address, no range, no host name ([reverse-proxy.md](deploy/reverse-proxy.md#which-address-is-hm_proxy)). |
| `HM_PROXY needs an https HM_PUBLIC_URL, the address the proxy serves` | Set `HM_PUBLIC_URL=https://hm.example.org`. |
| `approval timeout must be between 30s and 10m0s` | `HM_APPROVAL_TIMEOUT` / `approval_timeout_seconds`: 30 to 600. |
| `log level must be debug, info, warning or error` | Fix `HM_LOG_LEVEL` / `log_level`. |
| `HM_INGRESS_ADDR needs HM_INGRESS_PROXY …` / `HM_INGRESS_PROXY without HM_INGRESS_ADDR` | Set both or neither; normally neither. |
| `HM_PDP_ADDR must be a loopback address` | Use `127.0.0.1:<port>` or leave it unset. |
| `public_url needs ha_browser_url for the sign-in of humans` (app) | Set the option `ha_browser_url`. |
| `TLS file names must be plain file names in /ssl` (app) | `tls_certfile` / `tls_keyfile` without a path, for example `fullchain.pem`. |
| `cannot ask the Supervisor how Home Assistant is reached: …` (app) | With `public_url` set, the app asks the Supervisor for Home Assistant's port and TLS at start. Restart the app; if it persists, check the Supervisor's health under Settings → System. |
| `home assistant speaks TLS: ha_browser_url must name the host its certificate is for` (app) | Home Assistant serves its own TLS; set `ha_browser_url` to `https://<the name in its certificate>:<port>`. |

### Certificate

| Message (shortened) | Fix |
|---|---|
| `load TLS certificate: tlscert: certificate does not cover the public host: …` | The certificate is not issued for the host of the public URL. Use a certificate for that name, or correct the public URL. |
| `load TLS certificate: tlscert: load certificate and key: …` | Files missing, unreadable, or certificate and key do not belong together. |
| `no TLS certificate in /ssl, MCP endpoint only on localhost` (app, warning) | No file `/ssl/<tls_certfile>`. Install the Let's Encrypt or DuckDNS app, or put the files there, then restart. |
| UI banner "Renewed certificate not taken over" | The renewed files do not belong together, are not valid now, or do not cover the public host. The previous certificate stays in use; fix the files, Home-Mandate retries every minute. |

### Home Assistant connection

| Message | Fix |
|---|---|
| `Home Assistant rejected the access token` (Home-Mandate exits) | The token was deleted, belongs to a deleted user, or is wrong. Create a new long-lived token as the Home-Mandate user ([install-container.md](install-container.md#1-a-home-assistant-user-for-home-mandate)) and write it to the token file. |
| `home assistant subscription failed` with `"event_type":"mobile_app_notification_action"` | Home-Mandate's Home Assistant user is not an administrator, so answers to approval requests cannot be received. Make it an administrator and restart. |
| `home assistant connection lost` with `retry_in` | Home Assistant restarts or is unreachable; Home-Mandate reconnects by itself. Meanwhile every request is refused. |
| `the Supervisor is not at the address the UI trusts, the UI stays locked` (app) | The Supervisor's internal address changed. Home-Mandate does not follow such a change blindly; report it as an issue. |

### Database and audit log

| Message | Fix |
|---|---|
| `store: database is newer than this binary` | You started an older version on a database a newer one has already updated. Use the newer version, or restore the backup taken before the update. |
| `store: applied migration was changed` | The database does not match this binary's history of updates. Restore a backup and report it. |
| `audit log is broken, not starting` with `broken_at` | See [the audit log is broken](#the-audit-log-is-broken). |
| `audit log beginning deleted without a verified checkpoint` (error, Home-Mandate starts) | See below. |

## The audit log is broken

Home-Mandate refuses to start when the hash chain of its audit log is broken or the log has
been shortened in a way no checkpoint covers, because entries may have been changed. The
UI shows "Audit log broken from entry no. N" when it finds this while running.

1. Do not delete anything. Take a copy of the data directory for later analysis.
2. Look at what is wrong: `home-mandate audit verify` prints where the chain breaks
   (`audit log broken at seq …`) or that the beginning was deleted without a verified
   checkpoint (`first_seq`, `truncation`). `home-mandate audit export` writes the whole log
   as JSON Lines.
3. Compare with the daily checkpoint notifications on the approvers' phones ("Home-Mandate:
   state of the audit log"): they name the position and digest of the log at that time.
4. Restore the data directory from a backup taken before the problem, if you have one
   ([app](install-ha-os.md#backups-and-restore), [container](install-container.md#backup-and-restore)).

"Beginning deleted without a verified checkpoint" alone (Home-Mandate starts and logs an
error) means the oldest entries are gone and no checkpoint vouches for it. Either someone
deleted them, or the log was shortened before it had any checkpoint. Treat it as a warning
that the earlier history cannot be proven.

## The clock is behind

UI banner "Clock of the host is wrong": the host's clock shows an earlier time than the
newest audit entry. Validity periods and time windows cannot be trusted then, so every
request is refused (agents get `unavailable`) until the clock is right.

- Check the host's time synchronization (NTP); on a Linux host with systemd,
  `timedatectl` shows whether the clock is synchronized.
- If the clock **ran ahead** by mistake and was then corrected, the newest entries carry
  future times. `home-mandate audit accept-clock` sets them aside for this check, so that
  requests are decided again. It prints up to which entry and which time.
- The daily cleanup of old entries waits while the clock is behind (`audit log retention
  postponed: the clock lies behind the newest entry`) and tries again the next day.

## An agent cannot connect

1. **Is OAuth on?** The log must not say `no public URL configured`. Without a public URL,
   no agent can be admitted.
2. **Is the address right?** Open `<public URL>/.well-known/oauth-authorization-server` in
   a browser on the agent's machine. It must load without a certificate warning and show
   `"issuer"` equal to your public URL. The agent's MCP address is `<public URL>/mcp`,
   exactly as shown under Settings → MCP endpoint.
3. **Certificate:** issued for the public URL's host by a certificate authority the agent
   trusts. Self-signed certificates are rejected by most clients.
4. **Reachability:** from outside your network, check the port forward or proxy route.
   Home Assistant Cloud does not carry the MCP endpoint. Services on the internet such as
   claude.ai may need IPv4 ([agents.md](agents.md#claudeai-and-the-claude-apps)).
5. **Behind `HM_PROXY`:** every request gets an empty `403` and the log says `request from
   outside the proxy refused` with the address it saw (at most once a minute): `HM_PROXY`
   is not the address the proxy connects from. See
   [reverse-proxy.md](deploy/reverse-proxy.md#which-address-is-hm_proxy). Never publish the
   port in that setup to "fix" it.
6. **The consent page says "The agent could not be identified, or its return address is
   not allowed":** the client's metadata document could not be fetched (it must be on a
   public address, port 443, at most 5 KB), or the client returns to an address its
   document does not list. Clients that only support dynamic registration cannot connect
   directly; use `mcp-remote` or a pairing code ([agents.md](agents.md)).
7. **Pairing code:** "The code is wrong, expired or already used" — codes are valid for 10
   minutes; let the agent show a new one. After too many wrong codes pairing is locked for
   ten minutes.
8. **Approval takes long and the agent gives up, or nginx answers 504:** an agent's call
   waits up to 45 seconds (`approval_wait_seconds` / `HM_APPROVAL_WAIT`), then gets a
   pending result and asks again with `approval_status`. Clients cut calls off: Claude
   Desktop after about 60 seconds, so keep the wait below that. The proxy must allow the
   wait plus 40 seconds (85 s by default) ([reverse-proxy.md](deploy/reverse-proxy.md#what-every-proxy-must-do)). If
   the agent says the action is done although it was pending, its model ignored the
   result's text; answering *Allow* still executes it once.
9. **After an emergency stop or revocation**, the agent's tokens are gone. After an
   emergency stop, let the agent sign in again and choose **Reconnect** for its existing
   entry on the sign-in or pairing page: it keeps its mandate. If no agent is offered there,
   the agent signed in with another OAuth client than before (another `client_id`, or a
   pairing code instead of the browser) or still has valid access; admit it as a new agent
   and revoke and remove the old entry. After a revocation, admit it again.
10. **An agent shows "Not signed in"**: it has no valid access, after an emergency stop or
   because it was not used for 30 days. It works again after it signs in and you reconnect
   it; if you admitted it anew instead, remove the old entry (**Revoke access** with
   **Also remove the agent and its mandates from the lists**).
11. **Removed an agent or mandate by mistake**: a removed one stays revoked; **Show removed**
   shows it read-only. Admit the agent again, or give it a new mandate.

## Signing in fails

Signing in (to the UI in container mode, and to admit an agent in both modes) goes through
Home Assistant.

- **The browser cannot open Home Assistant's login page:** `ha_browser_url` /
  `HM_HA_BROWSER_URL` is not an address that browser reaches. In container mode the
  default is the origin of `HM_HA_URL`, typically `http://127.0.0.1:8123`, which works only
  on the host itself. Set it to Home Assistant's address in your network, or its public
  address when you sign in from outside.
- **"Signing in with Home Assistant failed" / "Signing in did not work":** Home-Mandate
  could not exchange the sign-in with Home Assistant. Check the log. In the app with Home
  Assistant's own TLS, `ha_browser_url` must use the name in Home Assistant's certificate.
  In container mode Home-Mandate exchanges it at the origin of `HM_HA_URL`; with `wss://`
  its certificate must be trusted (`HM_HA_CA_FILE` for a self-signed one).
- **"This account is not a Home Assistant administrator" / "Only Home Assistant
  administrators may admit agents":** sign in with an administrator account; sign out of
  Home Assistant first if the browser remembered another one.
- **"Too many sign-ins right now" / "Too many sign-ins are in progress":** the sign-in
  limits are reached; wait a minute. Behind a proxy that does not pass the client's
  address, all clients share one limit ([reverse-proxy.md](deploy/reverse-proxy.md)).
- **"The sign-in has expired":** start again from the agent or the UI.
- **Home Assistant refuses you with a ban:** if `ip_ban_enabled` is on in Home Assistant's
  `http:` configuration, failed logins can ban the address Home Assistant sees, and behind
  a proxy that can be the proxy's address for everyone. Remove the entry from
  `ip_bans.yaml` in Home Assistant's configuration directory and restart Home Assistant;
  make your proxy pass the client's address to Home Assistant.

## Approvals do not arrive

Work through these in order. **Settings → Approvers → Send test** checks the path to one
device without an agent.

1. **Is the person an approver of this mandate?** A mandate keeps the approvers it was
   created with; people set up later are not added. Open the mandate and check
   **Approvers** (and, for *Ask first* rules with their own approvers, the rule).
2. **Does the person have a device?** "No phone with the Home Assistant app. Can't receive
   approvals." means no device is chosen. Only Companion app devices (`notify.mobile_app_…`)
   can be chosen; Home Assistant lists a phone only after the Companion app registered it.
3. **Critical requests** (unlocking, opening a gate, disarming the alarm, marked devices)
   go only to devices with **Critical requests too**. The section says "Critical requests
   reach nobody and are declined" when no device has it.
4. **The phone:** notifications for the Companion app allowed, no focus or do-not-disturb
   mode suppressing them, battery optimisation not killing the app (Android). The test
   notification must arrive.
5. **The account on the phone:** the Companion app must be signed in with the approver's
   own Home Assistant account. An answer from another account is discarded, the request is
   denied ("Invalid response discarded"), and the approvers get "Warning: unauthorised
   answer".
6. **The buttons do nothing:** answers arrive through Home Assistant's
   `mobile_app_notification_action` event. In container mode the Home-Mandate user must be
   an administrator (see the log message `home assistant subscription failed`).
7. **Too late:** after the approval timeout the request is declined; a button pressed
   afterwards has no effect. Raise the mandate's timeout, up to the
   installation's limit (`approval_timeout_seconds` / `HM_APPROVAL_TIMEOUT`, at most 600
   seconds).
8. **Cooldown:** after a refusal or timeout the agent may not ask again for the same device
   for a while, and nobody is notified (agent error `approval_cooldown`).
9. **Home Assistant was disconnected:** while it is unreachable, approvals cannot be
   delivered and affected requests are declined.
10. **Asked once, although the agent called twice; or "already executed":** repeated
    calls of an agent for the same device and action are tied to the open request, and
    the same call shortly after an execution is not executed again
    ([ARCHITECTURE.md](ARCHITECTURE.md), section 7, repeated requests). A confirmed
    action that ends `already_in_state` or `state_changed` was not executed because the
    device was no longer in the state shown with the request.
11. **Home-Mandate was restarted** (app update, crash, reboot) while a request waited: the
    request ended, nothing was executed, and the notification on the phone is replaced by
    "Approval request ended" once Home Assistant is connected again; an answer given in
    between does not count. The agent may ask again. The audit log shows the request as
    "Ended by a restart, not executed" (`cancelled`, `cause: interrupted`; the log message
    `approval requests ended by the restart` counts them). If the restart came while a
    confirmed action was being executed, the notification says "Please check: <device>"
    and the log shows `outcome_unknown`: Home-Mandate does not know whether Home Assistant
    carried it out and never repeats it on its own.
12. **The old notification stays on the phone** after a request ended: Home-Mandate asks
    the Companion App to remove it (`clear_notification`), which needs app version 2021.5
    or later on iOS and may need the app to have been used recently. Pressing its buttons
    has no effect.

## Agents get "unavailable"

`unavailable` means Home-Mandate cannot decide reliably right now, so it decides nothing.
The audit entry names the cause:

| Cause in the audit log | Meaning | What to do |
|---|---|---|
| `ha_unavailable` | Home Assistant is disconnected, or the device directory is being refreshed after a registry change (usually well under a second; a directory older than 30 minutes is not used either). | Wait; check the overview for "Home Assistant unreachable". |
| `timezone_unknown` | Home Assistant's configuration (time zone) has not been read since the connection was made. Home-Mandate retries every 1, 2, 4 … seconds up to every minute. | Wait; check the log for `cannot read the Home Assistant configuration`. |
| `service_user_unknown` | Home-Mandate's own Home Assistant user is not known yet. | As above, `cannot read Home-Mandate's own Home Assistant user`. |
| `clock_behind` | The host clock is behind the audit log. | [The clock is behind](#the-clock-is-behind). |

A banner "Too many renames from Home Assistant wait to be stored" also refuses every
request until the renames can be stored; check that the data directory is writable and the
disk not full.

## Rate limits

- **Per agent:** the mandate's rate limit counts every request (lists and reads included)
  over the last hour; beyond it the agent gets `rate_limited`. An agent without a usable
  mandate has 60 requests per hour. The agent's page shows "… of … actions in the last
  hour". Raise the mandate's **Rate limit** if the agent needs more. The counts survive a
  restart.
- **Sign-in, pairing and token endpoints:** per sender (an IPv6 sender counts as its /64)
  and for everyone together, per minute. Beyond, `429 Too Many Requests` with
  `Retry-After` in seconds, or a page "Too many sign-ins …". Wait that long. Behind a
  reverse proxy that does not pass the client address, or with TLS passthrough, all
  clients share one sender.
- **UI:** 600 requests per minute per person; beyond, requests are refused for the rest
  of that minute.

## The UI

- **"No access"** (app): you are not a Home Assistant administrator. Approvers who are not
  administrators answer on their phone.
- **Container mode, no UI at `/ui/`:** the UI needs an `https://` `HM_PUBLIC_URL` and either
  a certificate or `HM_PROXY`. The log says `UI in direct mode` when it is on.
- **"Home Assistant unreachable" banner:** all requests are declined until the connection
  is back; nothing is replayed later.
- **"No TLS certificate" banner:** agents outside your home network cannot connect
  securely. As an app, put `fullchain.pem` and `privkey.pem` (or the files named in
  `tls_certfile` and `tls_keyfile`) into `/ssl`. As a container, set `HM_TLS_CERT` and
  `HM_TLS_KEY`, or run Home-Mandate behind a reverse proxy with `HM_PROXY`
  ([install-container.md](install-container.md)); behind `HM_PROXY` the banner does not
  appear.
