# Reaching Home-Mandate from outside: behind your reverse proxy

Agents in the cloud (claude.ai, the Claude apps) and your phone away from home need to
reach Home-Mandate from the internet. Home-Mandate does not bring a proxy of its own; it
runs behind the one you already use for Home Assistant: Traefik, nginx, Nginx Proxy
Manager or Caddy. This page shows both ways and the configuration for each proxy.

All names and addresses here are placeholders: `hm.example.org` for Home-Mandate,
`ha.example.org` for Home Assistant, `192.0.2.x` for addresses in your network.

## Which way?

| | A. The proxy ends TLS (recommended) | B. The proxy passes TLS through |
|---|---|---|
| Works with | Traefik, nginx, Nginx Proxy Manager, Caddy | Traefik (nginx only with the `stream` module, Caddy only with the `layer4` plugin) |
| Certificate | The proxy's, as for your other services | Home-Mandate's own (`HM_TLS_CERT`, `HM_TLS_KEY`) |
| Home-Mandate setting | `HM_PROXY` | none beyond the usual |
| Between proxy and Home-Mandate | plaintext HTTP | TLS 1.3 end to end |
| Limits per sender (sign-in, pairing) | per client, from the proxy's `X-Forwarded-For` | all clients share them (Home-Mandate sees only the proxy) |

Take A unless you want TLS to end inside Home-Mandate.

## What every proxy must do

- Forward **every path** of `hm.example.org` to Home-Mandate, not only `/mcp`: the
  OAuth endpoints (`/.well-known/…`, `/oauth/…`, `/pair`) and the UI (`/ui/`) are on
  the same host.
- Pass **WebSocket** upgrades (the UI's live updates under `/ui/api/events`).
- Allow responses to take **at least the approval timeout plus 30 seconds**: an action
  that needs an approval waits for it (default 120 s, so 150 s; with
  `HM_APPROVAL_TIMEOUT=600` it is 630 s). Traefik and Caddy do not cut responses by
  default; **nginx does after 60 s**.
- Use the **public name without a port** in `HM_PUBLIC_URL`, exactly as browsers and
  agents type it (`https://hm.example.org`).
- Let the browser reach **Home Assistant from outside** too: signing in to the UI sends
  the browser to Home Assistant, so `HM_HA_BROWSER_URL` is Home Assistant's public
  address (`https://ha.example.org`), not a LAN address.

## Way A: the proxy ends TLS

### Home-Mandate

| Variable | Value |
|---|---|
| `HM_PROXY` | The **one** IP address the proxy connects from (no range, no host name) |
| `HM_PUBLIC_URL` | `https://hm.example.org` (must be https) |
| `HM_MCP_ADDR` | Where the proxy connects to; default `:8765` |
| `HM_HA_BROWSER_URL` | `https://ha.example.org` |

With `HM_PROXY` set, Home-Mandate:

- serves **only** that address. Every other sender gets an empty 403, so nobody can
  skip the proxy and talk plaintext to Home-Mandate;
- takes the sender for its per-sender limits and the log from the **last** entry of
  `X-Forwarded-For`, the one your proxy appended; earlier entries are ignored, a client
  can write anything there. The sender never decides about access. Your proxy must
  therefore append the client's address or overwrite the header with it; Traefik,
  Caddy, Nginx Proxy Manager and the nginx snippet below do;
- serves the UI under `https://hm.example.org/ui/` with its own sign-in through Home
  Assistant (administrators only), as with a certificate of its own.

`HM_TLS_CERT` and `HM_TLS_KEY` can stay set if your proxy talks TLS to Home-Mandate
(`https://` upstream); then the certificate must cover `hm.example.org`.

### Which address is `HM_PROXY`?

The address the proxy's connections **arrive from** at Home-Mandate:

| Setup | `HM_PROXY` | `HM_MCP_ADDR` |
|---|---|---|
| Proxy installed directly on the host, Home-Mandate in the host network | `127.0.0.1` | `127.0.0.1:8765` |
| Proxy and Home-Mandate as containers in the same Docker network | the proxy's fixed address in that network (see below) | `:8765`, no published port |
| Proxy in a Docker network, Home-Mandate in the host network | the proxy container's fixed address (it connects from there) | the host's LAN address, e.g. `192.0.2.10:8765` |
| Proxy on another machine in your network | that machine's LAN address | the host's LAN address, e.g. `192.0.2.10:8765` |

Container addresses change on every restart unless you fix them. With Compose:

```yaml
networks:
  proxy:
    ipam:
      config:
        - subnet: 172.31.250.0/24

services:
  traefik: # or nginx, caddy, npm
    networks:
      proxy:
        ipv4_address: 172.31.250.2

  home-mandate:
    networks: [proxy]
    environment:
      HM_PROXY: 172.31.250.2
      HM_MCP_ADDR: :8765
      HM_PUBLIC_URL: https://hm.example.org
      HM_HA_BROWSER_URL: https://ha.example.org
      # … the other variables from docs/deploy/compose.yaml
```

> **`HM_PROXY` must be an address only the proxy connects from.** Never publish
> Home-Mandate's port (`ports: "8765:8765"`) in way A: Docker then hands every
> connection, from the proxy and from anyone else, to Home-Mandate from the same bridge
> address (e.g. `172.17.0.1`). Set as `HM_PROXY`, that address would let every client in
> your network or the internet past the check, in plaintext, and let it choose its own
> sender. Use a Docker network shared with the proxy (no published port), the host
> network, or a firewall that admits the port from the proxy alone.

If the address is wrong, every request gets a 403 and Home-Mandate logs
`request from outside the proxy refused` with the address it saw (at most once a
minute). That is the value for `HM_PROXY` only if the proxy connects directly, without
a published port or NAT in between.

If your proxy runs in Docker and clients reach it over IPv6, check that its network has
IPv6 enabled (`docker network create --ipv6 …`, or `enable_ipv6: true` in Compose).
Otherwise Docker's `docker-proxy` accepts IPv6 connections on the published port and
hands them on from the bridge's own address, so the proxy logs every IPv6 client as, for
example, `172.18.0.1`. All of them then share one sender, in Home-Mandate and in Home
Assistant's IP ban alike.

If another proxy or a CDN stands in front of yours, the last `X-Forwarded-For` entry is
that one's address, and all clients share one sender and its limits: a single client can
then use up the pairing and sign-in limits for everyone. Let your proxy take the client
address from the one in front (Traefik `forwardedHeaders.trustedIPs`, nginx
`set_real_ip_from` with `real_ip_header`, Caddy `trusted_proxies`), listing only the
address of that front proxy, never a client network.

When the proxy runs on another machine, the traffic between both crosses your network
in plaintext, including access tokens. Do that only in a network you trust, and keep
Home-Mandate's port closed to everything else (firewall); Home-Mandate itself answers
only the proxy.

### Traefik (v3)

File provider, e.g. `dynamic/home-mandate.yml`:

```yaml
http:
  routers:
    home-mandate:
      rule: "Host(`hm.example.org`)"
      entryPoints: [websecure]
      service: home-mandate
      tls:
        certResolver: letsencrypt # your resolver
        options: modern
  services:
    home-mandate:
      loadBalancer:
        servers:
          - url: "http://192.0.2.10:8765" # HM_MCP_ADDR as the proxy reaches it

tls:
  options:
    modern:
      minVersion: VersionTLS13
```

Or as labels on the Home-Mandate container (same Docker network as Traefik):

```yaml
    labels:
      - traefik.enable=true
      - traefik.http.routers.home-mandate.rule=Host(`hm.example.org`)
      - traefik.http.routers.home-mandate.entrypoints=websecure
      - traefik.http.routers.home-mandate.tls.certresolver=letsencrypt
      - traefik.http.services.home-mandate.loadbalancer.server.port=8765
```

Traefik appends the client to `X-Forwarded-For` and passes WebSockets without further
settings. Do not enable `forwardedHeaders.insecure` on the entry point, and list in
`forwardedHeaders.trustedIPs` only a proxy or CDN in front of Traefik.

### nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name hm.example.org;

    ssl_certificate     /etc/letsencrypt/live/hm.example.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/hm.example.org/privkey.pem;
    ssl_protocols       TLSv1.3;

    location / {
        proxy_pass http://192.0.2.10:8765; # HM_MCP_ADDR as nginx reaches it
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        $connection_upgrade;
        # An action may wait for an approval: approval timeout + 30 s at least.
        proxy_read_timeout 660s;
        proxy_send_timeout 660s;
        client_max_body_size 1m;
    }
}

# Once, in the http block:
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

### Nginx Proxy Manager

Add a proxy host:

- **Details:** domain `hm.example.org`, scheme `http`, forward host and port as in
  `HM_MCP_ADDR`, **Websockets Support** on.
- **SSL:** request a certificate, **Force SSL** on.
- **Advanced**, custom configuration:

  ```nginx
  proxy_read_timeout 660s;
  proxy_send_timeout 660s;
  ```

`HM_PROXY` is the address of the Nginx Proxy Manager container (fix it as shown above).

### Caddy

```caddyfile
hm.example.org {
    tls {
        protocols tls1.3
    }
    reverse_proxy 192.0.2.10:8765
}
```

Caddy gets the certificate, passes WebSockets, sets `X-Forwarded-For` and does not cut
long responses. List in `trusted_proxies` only a proxy or CDN in front of Caddy, never
client networks.

## Way B: the proxy passes TLS through (Traefik)

Home-Mandate keeps TLS 1.3 with its own certificate (`HM_TLS_CERT`, `HM_TLS_KEY`, which
must cover `hm.example.org`); Traefik only routes by the name in the TLS handshake.
`HM_PROXY` stays unset, `HM_PUBLIC_URL` is `https://hm.example.org` without a port.

```yaml
tcp:
  routers:
    home-mandate:
      rule: "HostSNI(`hm.example.org`)"
      entryPoints: [websecure]
      service: home-mandate
      tls:
        passthrough: true
  services:
    home-mandate:
      loadBalancer:
        servers:
          - address: "192.0.2.10:8765"
```

Home-Mandate then sees every client as the proxy's address, so the limits for signing in
and pairing apply to all clients together.

## App mode (Home Assistant OS)

In the sidebar of Home Assistant the UI is already reachable wherever Home Assistant is,
through Ingress. For agents from outside, set a certificate from `/ssl` and `public_url`
in the app's options and pass TLS through as in way B; `HM_PROXY` is not available in
app mode yet.

## Check

1. `https://hm.example.org/.well-known/oauth-authorization-server` shows
   `"issuer": "https://hm.example.org"`.
2. `https://hm.example.org/ui/` sends you to Home Assistant's sign-in and back.
3. The UI shows `https://hm.example.org/mcp` as the address for agents.
4. Home-Mandate's port answers nobody but the proxy (way A: empty 403 from any other
   machine).

## What is reachable from the internet

Everything on `hm.example.org`: the MCP endpoint (only with an access token), the OAuth
endpoints (only a Home Assistant administrator, signed in through Home Assistant, admits
an agent), the UI (only Home Assistant administrators, signed in through Home Assistant). The proxy sees all
traffic in plaintext, access tokens included; run it on a machine you trust. See
`SECURITY.md` for the threat model.
