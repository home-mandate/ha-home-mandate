# Home-Mandate in container mode

This guide runs Home-Mandate as a container next to **Home Assistant Container**, with
Docker Compose or Podman Compose: the Home Assistant user it needs, the files and their
permissions, how to expose it, every environment variable, updates, backups and removal.
For Home Assistant OS, use the app instead: [install-ha-os.md](install-ha-os.md).

All names and addresses below are placeholders: `hm.example.org` for Home-Mandate,
`ha.example.org` for Home Assistant, `192.0.2.10` for the host in your network.

## Requirements

- **Home Assistant Container** (the end-to-end tests run against Home Assistant 2026.9.4).
- **Docker with Compose v2, or Podman with podman-compose**, on `amd64` or `arm64`.
- **The Home Assistant Companion app** on the phone of everyone who should answer approval
  requests, signed in with that person's own Home Assistant account.
- For the UI and for agents on other machines: a **TLS certificate** for the name agents
  use (for example from your ACME client), or a **reverse proxy** you already run.

## Overview of the steps

1. [Create a dedicated Home Assistant user and its token](#1-a-home-assistant-user-for-home-mandate)
2. [Prepare the directory: token file, data directory, certificate](#2-files-and-permissions)
3. [Choose how agents and browsers reach Home-Mandate](#3-choose-how-to-expose-it)
4. [Write the Compose file](#4-the-compose-file)
5. [Start and check the log](#5-first-start)
6. [Open the UI and sign in](#6-open-the-ui)

## 1. A Home Assistant user for Home-Mandate

Home-Mandate talks to Home Assistant's WebSocket API with the long-lived access token of
**its own Home Assistant user**. Do not use your own account: a dedicated user shows up as
the actor of everything Home-Mandate does in Home Assistant, can be removed on its own,
and is never asked to approve anything (Home-Mandate leaves its own user out).

**Why administrator rights.** Home-Mandate receives the answers to approval requests
through Home Assistant's `mobile_app_notification_action` event, and Home Assistant lets
only administrators subscribe to that event. Everything else would work without admin
rights. The proof of who approved (the user in the event's context) has to come from Home
Assistant directly, which is why Home-Mandate does not use a workaround such as an
automation that forwards answers. To keep the risk small, Home-Mandate sends only a fixed
list of WebSocket commands and refuses every other one before it is sent; the UI shows
that list under Settings → Home Assistant connection → View fixed command list.

Create the user:

1. In Home Assistant, go to **Settings → People** and add a person, for example named
   `Home-Mandate`, and allow it to log in.
2. Give it a username and a long random password (you need the password once, in step 4).
3. Make it an **administrator**.
4. Recommended: allow it to log in only from the local network (Home Assistant's option
   for local access only), if Home-Mandate reaches Home Assistant over loopback or your
   local network. Home Assistant then refuses this user's credentials from the internet,
   so a leaked token is of less use there.

Create its token, signed in **as that user** (for example in a private browser window):

1. Sign in to Home Assistant as `Home-Mandate`.
2. Open the profile (the user name at the bottom of the sidebar), tab **Security**, section
   **Long-lived access tokens**, and create a token named `Home-Mandate`.
3. Copy the token; Home Assistant shows it only once. Sign out.

If the user lacks administrator rights, Home-Mandate starts, but the log shows `home
assistant subscription failed` for `mobile_app_notification_action`, and approvals cannot
be answered on a phone.

## 2. Files and permissions

Home-Mandate runs **without root**, as UID and GID `65532` in the examples (any user
other than root works). It needs no privileges: port 8765 is no privileged port, and it
writes only to its data directory. Started as root in container mode, it logs a warning.

Work in a directory of your choice, for example `/opt/home-mandate`:

```
/opt/home-mandate/
├── compose.yaml
├── ha-token          # the long-lived token, one line
├── data/             # Home-Mandate's database and audit key
└── certs/            # fullchain.pem and privkey.pem (only with an own certificate)
```

### The token file

Write the token into `ha-token` (only the token; surrounding spaces and line breaks are
ignored), then make it readable by Home-Mandate's user alone:

```sh
sudo chown 65532:65532 ha-token
sudo chmod 600 ha-token
```

Home-Mandate refuses to start if the token file can be read by group or others, or is not
a regular file. Why: whoever reads this token controls your whole home.

Home-Mandate never stores the token. You can also pass it in the variable `HM_HA_TOKEN`;
Home-Mandate removes it from its environment once read, but it stays visible in the
container's configuration (`docker inspect`). Prefer the file.

### Run without root

The data directory must belong to Home-Mandate's user and must not be writable by group or
others. Create it **before the first start**; Docker would create a missing one for root,
and Home-Mandate would then refuse to start:

```sh
mkdir -p data
sudo chown -R 65532:65532 data
sudo chmod 700 data
```

The certificate's private key must be readable by that user too:

```sh
sudo chown 65532:65532 certs/privkey.pem
sudo chmod 600 certs/privkey.pem
```

Let your ACME client hand a renewed key to that user as well, with a deploy hook that
runs the same `chown`, or a group the user is in and mode `0640`.

If a file or directory has the wrong owner, Home-Mandate stops and names the fix, for
example:

```
home-mandate: store: insecure permissions: /data is owned by uid 0, but Home-Mandate runs as uid 65532; fix: chown -R 65532:65532 /data (in a container: the host directory mounted there)
```

Run the `chown` on the host directory that is mounted at `/data` (here `./data`).

**Rootless Podman.** The container's UID 65532 is a subordinate UID on the host. Hand the
directory over with `podman unshare chown -R 65532:65532 data` (and the same for
`ha-token` and the key), or run the container with
`--userns=keep-id:uid=65532,gid=65532`, so that your own files appear as the container
user's.

**Upgrading from a container that ran as root:** stop it, run
`sudo chown -R 65532:65532 data`, set the user and start again.

## 3. Choose how to expose it

Home-Mandate serves its MCP endpoint, the OAuth pages and, in direct mode, the UI on one
listener (port 8765 by default). It never serves plaintext beyond the local machine unless
you name a reverse proxy that ends TLS, because agents' access tokens travel over this
connection.

| | A. Localhost only | B. Own certificate | C. Behind your reverse proxy |
|---|---|---|---|
| Choose when | You try it out, and agents run on the same machine | Agents in your network, or from outside through a port forward or a TLS passthrough | You already run Traefik, nginx, Nginx Proxy Manager or Caddy for Home Assistant |
| TLS | none, loopback only | Home-Mandate's own, TLS 1.3 | ends at your proxy |
| Settings | no `HM_TLS_*`, no `HM_PROXY` | `HM_TLS_CERT`, `HM_TLS_KEY` | `HM_PROXY` |
| `HM_PUBLIC_URL` | `http://localhost:8765` (or empty) | `https://hm.example.org:8765` (with the port agents use) | `https://hm.example.org` (the proxy's address, no port) |
| UI | none; command line only | `https://hm.example.org:8765/ui/` | `https://hm.example.org/ui/` |

**A. Localhost only.** Without a certificate and without `HM_PROXY`, the MCP endpoint
listens on `127.0.0.1:8765` and refuses any other listen address. There is no UI (it needs
`https://`); you manage Home-Mandate with the [command line](usage.md#command-line).
Agents on the same machine can be admitted with `HM_PUBLIC_URL=http://localhost:8765`,
if a browser on that machine can reach Home Assistant for the sign-in. This variant is not
covered by the end-to-end tests.

**B. Own certificate.** Home-Mandate ends TLS 1.3 itself with the certificate in
`HM_TLS_CERT` and `HM_TLS_KEY`. The certificate must cover the host of `HM_PUBLIC_URL`, or
Home-Mandate does not start. Renewed files are taken over without a restart (checked once
a minute); from 14 days before expiry the UI warns. To reach it from outside, forward the
port on your router or let a reverse proxy pass TLS through
([way B in reverse-proxy.md](deploy/reverse-proxy.md#way-b-the-proxy-passes-tls-through-traefik));
behind a passthrough proxy `HM_PUBLIC_URL` is the proxy's address without a port. This is
the variant of the example below and of the end-to-end tests.

**C. Behind your reverse proxy.** Your proxy ends TLS and talks plain HTTP to
Home-Mandate. `HM_PROXY` names the **one** IP address the proxy connects from; every other
sender gets an empty 403, so nobody can bypass the proxy. Read
[docs/deploy/reverse-proxy.md](deploy/reverse-proxy.md) before you set it: publishing
Home-Mandate's port with Docker in this setup would let everyone past the check. The
`HM_PROXY` handling is covered by unit tests, not yet by the end-to-end suite.

In every variant, the browser of the administrator who signs in must reach Home Assistant
at `HM_HA_BROWSER_URL`, and must reach Home-Mandate at `HM_PUBLIC_URL`.

## 4. The Compose file

This is [deploy/compose.yaml](deploy/compose.yaml), variant B: Home Assistant runs on
the same host in the host network, Home-Mandate too, and listens on the host's LAN
address with its own certificate.

```yaml
services:
  home-mandate:
    # Released images: ghcr.io/home-mandate/ha-home-mandate:<version>, signed (see Updates).
    image: ghcr.io/home-mandate/ha-home-mandate:0.1.0-rc.2
    container_name: home-mandate
    restart: unless-stopped
    user: "65532:65532"           # owns ./data; port 8765 needs no privilege
    network_mode: host            # reaches Home Assistant on 127.0.0.1:8123
    read_only: true               # writes only to /data
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    environment:
      # Home Assistant's WebSocket API. Plaintext ws:// is allowed to loopback only.
      HM_HA_URL: ws://127.0.0.1:8123/api/websocket
      # The long-lived token of Home-Mandate's own Home Assistant user.
      HM_HA_TOKEN_FILE: /run/secrets/ha-token
      HM_DATA_DIR: /data
      # Own certificate (variant B); must cover the host of HM_PUBLIC_URL.
      HM_TLS_CERT: /certs/fullchain.pem
      HM_TLS_KEY: /certs/privkey.pem
      # Listen on one address of this host only; agents and browsers connect here.
      HM_MCP_ADDR: 192.0.2.10:8765
      # Where agents and browsers reach Home-Mandate: with the port, as they connect
      # directly. Behind a reverse proxy: the proxy's address without a port.
      HM_PUBLIC_URL: https://hm.example.org:8765
      # Home Assistant as your browser reaches it, for signing in.
      HM_HA_BROWSER_URL: http://192.0.2.10:8123
      HM_LOG_LEVEL: info
    volumes:
      - ./data:/data                               # 65532:65532, mode 0700
      - ./certs:/certs:ro                          # privkey.pem readable by 65532
      - ./ha-token:/run/secrets/ha-token:ro        # 65532, mode 0600
```

Adapt it:

- `hm.example.org` must resolve to the host (in your network, and from outside if agents
  connect from there), and the certificate must be issued for it.
- `HM_MCP_ADDR`: the host's LAN address, or `:8765` for all addresses.
- If Home Assistant does **not** run in the host network, Home-Mandate cannot reach it on
  loopback, and plaintext to any other address is refused. Use `wss://` then, for example
  `wss://ha.example.org/api/websocket` (Home Assistant with TLS or behind your proxy; TLS
  1.3 is required), and `HM_HA_CA_FILE` if its certificate is self-signed.
- With your own image built by `make image`, use `image: home-mandate:dev`.
- For variant C, follow [deploy/reverse-proxy.md](deploy/reverse-proxy.md): no
  `network_mode: host` and no published port, a fixed address for the proxy, `HM_PROXY`,
  `HM_PUBLIC_URL: https://hm.example.org`.

## Environment variables

Home-Mandate reads only these variables. A value it cannot use stops the start with a
message naming the variable. `HM_HA_URL` and a token are required; everything else is
optional.

| Variable | Default | Meaning |
|---|---|---|
| `HM_HA_URL` | – (required) | Home Assistant's WebSocket API, `ws://…/api/websocket` or `wss://…/api/websocket`. `ws://` only to loopback (`127.0.0.1`, `::1`, `localhost`). `wss://` needs TLS 1.3. The origin of this URL is also where Home-Mandate exchanges sign-ins with Home Assistant. |
| `HM_HA_TOKEN_FILE` | – | File with the long-lived token of Home-Mandate's Home Assistant user. Must be a regular file, not readable by group or others (`chmod 600`), readable by Home-Mandate's user. Preferred over `HM_HA_TOKEN`. |
| `HM_HA_TOKEN` | – | The token itself. Removed from the environment once read, but visible in the container configuration. Set either this or `HM_HA_TOKEN_FILE`, not both. |
| `HM_HA_CA_FILE` | system CAs | Absolute path of a PEM file with the CA certificates to trust for `wss://` instead of the system's, for a Home Assistant with a self-signed certificate. |
| `HM_DATA_DIR` | `/data` | Absolute path of the data directory (database `home-mandate.db`, audit key). Owned by Home-Mandate's user, not writable by group or others. |
| `HM_AUDIT_KEY_FILE` | `<HM_DATA_DIR>/audit-checkpoint.key` | Where the key that signs the audit checkpoints lives, see [Backup and restore](#backup-and-restore). Created by the first start only. Must be a regular file (no symbolic link) owned by Home-Mandate's user, without access for group or others. |
| `HM_TLS_CERT`, `HM_TLS_KEY` | – | Absolute paths of the certificate chain and its key (PEM) for the MCP endpoint and the UI, TLS 1.3. Both or neither. The certificate must cover the host of `HM_PUBLIC_URL`. Renewed files are taken over without a restart. |
| `HM_MCP_ADDR` | `:8765` with TLS or `HM_PROXY`, otherwise `127.0.0.1:8765` | Listen address (`host:port`) of the MCP endpoint, the OAuth pages and the UI. Without TLS and without `HM_PROXY` only a loopback address is accepted. |
| `HM_PROXY` | – | The one IP address (no range, no host name) of a reverse proxy that ends TLS in front of Home-Mandate. Only that address is served; plaintext is then allowed beyond loopback; the sender for limits and the log is the last `X-Forwarded-For` entry. Needs an `https://` `HM_PUBLIC_URL`. See [reverse-proxy.md](deploy/reverse-proxy.md). |
| `HM_PUBLIC_URL` | – | The origin (scheme, host, port; no path) agents and browsers reach Home-Mandate at. `http://` only for `localhost` or loopback addresses. With an own certificate it carries the port agents use (`https://hm.example.org:8765`), behind a proxy it is the proxy's address (`https://hm.example.org`). Without it, OAuth is off and no agent can be admitted; with `https://` and a certificate or `HM_PROXY`, the UI is served under `/ui/` (direct mode). |
| `HM_HA_BROWSER_URL` | origin of `HM_HA_URL` (`ws` → `http`, `wss` → `https`) | Home Assistant as the human's browser reaches it, for signing in. The default (`http://127.0.0.1:8123` in the example) works only for a browser on the same host, so set it. |
| `HM_APPROVAL_TIMEOUT` | `120` | Upper limit in seconds for waiting for an answer to an approval request, 30 to 600. A mandate may only shorten it. |
| `HM_LOG_LEVEL` | `info` | `debug`, `info`, `warning` or `error`. |
| `HM_PDP_ADDR` | – | Advanced. Loopback address (for example `127.0.0.1:8181`) for the AuthZEN evaluation endpoint (`POST /access/v1/evaluation`), for other gateways on the same host. Leave it unset otherwise. |
| `HM_INGRESS_ADDR` | – | Advanced, not needed for the UI. Opens the UI's Ingress listener (for example `:8099`) for a proxy of your own that does what Home Assistant's Supervisor does: signs people in, sets `X-Remote-User-Id` and removes client copies of it. Used by the end-to-end tests. |
| `HM_INGRESS_PROXY` | – | Required with `HM_INGRESS_ADDR`: the one IP address of that proxy. Requests from any other address get nothing, and the user must be a Home Assistant administrator. |

Do not set `SUPERVISOR_TOKEN`: it switches Home-Mandate into app mode, which reads
`/data/options.json` instead of these variables.

## 5. First start

```sh
docker compose up -d        # or: podman-compose up -d
docker compose logs -f home-mandate
```

Configuration errors are printed as one line starting with `home-mandate:` and the
container stops. After that, the log is JSON, one line per event. A good start shows,
among others:

```
{"level":"INFO","msg":"MCP endpoint listening","addr":"192.0.2.10:8765","tls":true,"path":"/mcp"}
{"level":"INFO","msg":"UI in direct mode","url":"https://hm.example.org:8765/ui/"}
{"level":"INFO","msg":"home-mandate started","version":"0.1.0-rc.2","mode":"container","household":"…"}
{"level":"INFO","msg":"connected to home assistant"}
```

(Shortened; every line also has a `time`.) Without `HM_PUBLIC_URL` you see `no public URL
configured: OAuth is off, agents cannot be admitted`. Problems and their fixes are in
[troubleshooting.md](troubleshooting.md#start-up-errors).

To check from another machine: `https://hm.example.org:8765/.well-known/oauth-authorization-server`
shows a JSON document with `"issuer": "https://hm.example.org:8765"`.

## 6. Open the UI

Open `<HM_PUBLIC_URL>/ui/`, for example `https://hm.example.org:8765/ui/`, and choose
**Sign in with Home Assistant**. Your browser goes to Home Assistant at
`HM_HA_BROWSER_URL`; sign in with **your own** administrator account (not the Home-Mandate
user) and you come back to the UI.

- Only Home Assistant administrators get a session; the check is repeated on every
  request.
- A session ends after 30 minutes without use, after 12 hours, on **Sign out**, and with
  every restart of Home-Mandate. At most 5 sessions per person.
- Home Assistant does not need to reach Home-Mandate for the sign-in; Home-Mandate
  exchanges the result with Home Assistant at the origin of `HM_HA_URL`.

Continue with [usage.md](usage.md#first-steps) and [agents.md](agents.md).

## Command line in the container

The administration commands work on the local database only and need no Home Assistant
credentials. Run them in the running container:

```sh
docker exec home-mandate /home-mandate emergency-stop status
docker exec -i home-mandate /home-mandate mandate import - < mandate.json
```

All commands: [usage.md](usage.md#command-line).

## Updates

Released images are `ghcr.io/home-mandate/ha-home-mandate:<version>` for `linux/amd64` and
`linux/arm64`, for example `0.1.0-rc.2`. There is no `latest` tag; you choose the version.

1. Read the [changelog](../app/CHANGELOG.md) and the release notes.
2. Optionally verify the image's signature (the release notes show the exact command):

   ```sh
   cosign verify ghcr.io/home-mandate/ha-home-mandate:0.1.0-rc.2 \
     --certificate-identity https://github.com/home-mandate/ha-home-mandate/.github/workflows/release.yml@refs/tags/v0.1.0-rc.2 \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```

3. [Back up](#backup-and-restore) the data directory.
4. Optionally check that the new version accepts your stored mandates and templates. Run
   it against a **copy** of the data directory, because opening the database with a new
   version updates it:

   ```sh
   sudo cp -a data data-check
   docker run --rm --user 65532:65532 --read-only --network none \
     -v "$PWD/data-check:/data" -e HM_DATA_DIR=/data \
     ghcr.io/home-mandate/ha-home-mandate:NEW_VERSION mandate check
   sudo rm -r data-check
   ```

   `ok` means nothing to do; otherwise it lists each mandate or template the new version
   rejects. An invalid mandate denies every request of its agent. With `HM_AUDIT_KEY_FILE`
   outside the data directory, mount that key file too and set the variable.
5. Change the image tag in `compose.yaml`, then `docker compose pull` and
   `docker compose up -d`.
6. Check the log. Database changes are applied automatically at the first start. Open
   approval requests do not survive the restart.

Going back to an older version needs the backup: an older version refuses a database a
newer one has changed (`database is newer than this binary`).

## Backup and restore

Everything Home-Mandate keeps is in the data directory: `home-mandate.db` (with its
`-wal` and `-shm` files while it runs) and `audit-checkpoint.key`, unless
`HM_AUDIT_KEY_FILE` puts the key elsewhere. The token file is not needed in a backup; you
can create a new token.

Back up while the container is stopped, so that the database is copied consistently (the
image has no shell or database tool for an online copy):

```sh
docker compose stop home-mandate
sudo tar -C /opt/home-mandate -czpf home-mandate-$(date +%F).tar.gz data
docker compose start home-mandate
```

Agents get no answer during those seconds.

Restore:

```sh
docker compose stop home-mandate
sudo rm -r data
sudo tar -C /opt/home-mandate -xzpf home-mandate-2026-10-01.tar.gz
sudo chown -R 65532:65532 data && sudo chmod 700 data
docker compose start home-mandate
```

After a restore, mandates, agents and the audit log are as they were at the time of the
backup; agents admitted later must be admitted again.

**The checkpoint key.** The audit log is hash-chained, and Home-Mandate signs a checkpoint
of it every 15 minutes with this key. The key must be restored together with the database:
if the log has checkpoints and the key is missing, Home-Mandate refuses to start rather
than create a new key that could not verify them. Treat backups as sensitive: they contain
the key and the token hashes.

By default the key lies next to the database. That protects a copy of the database alone,
not someone who can read the whole directory and then rewrite the log with valid
signatures. `HM_AUDIT_KEY_FILE` puts it elsewhere (for example a separate mount); then back
it up separately. Also keep the public key and log ID outside the host:
`docker exec home-mandate /home-mandate audit key` prints both.

## Uninstall

1. Optionally trigger the emergency stop, so that no agent acts while you remove it.
2. `docker compose down`.
3. Delete the data directory (or keep a backup), the token file and, if only Home-Mandate
   used it, the certificate copy.
4. In Home Assistant, delete the `Home-Mandate` person and user under **Settings →
   People**. That also invalidates its long-lived token.
5. Remove the route from your reverse proxy, the port forward and the DNS name if you
   created them, and the MCP server entries in your agents.
