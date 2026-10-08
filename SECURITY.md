# Security

## Reporting vulnerabilities

Please do **not** use public issues. Report to: security@home-mandate.com
(until that address is set up: privately via GitHub Security Advisories of this repository).
We acknowledge within 72 hours and disclose in a coordinated manner.

## Threat model v0.1

### Assets
- Physical security of the home (locks, gates, alarm system)
- Privacy (cameras, presence, habits)
- Home-Mandate's access to Home Assistant (effectively full access)
- Agent tokens and mandates

### Attackers and countermeasures

"Planned" rows are not protection yet.

| Attacker / scenario | Countermeasure | Status |
|---|---|---|
| **Manipulated agent** (prompt injection via website, e-mail, document) tries to open a door | Decision outside the model; locks default to `ask`; the agent's reason is sanitized and marked as unverified in approval requests, which also show the service data; at most 2 pending approval requests per agent; rate limit against loops | done |
| Agent tries to extend its own permissions | Administrative functions are not reachable via MCP (the API is served only on the Ingress listener); mandates can only be changed through the UI by HA admins | done |
| Agent explores devices outside its mandate | Unreadable devices are neither listed nor mentioned in error messages | planned |
| Stolen agent token | Access tokens 10 minutes, bound to the MCP resource; refresh tokens bound to the agent's OAuth client, rotated, reuse revokes the whole family; agent status, revocation and emergency stop read on every request and again after an approval | done |
| Agent admitted without a human | No open registration; every admission needs an HA administrator signed in through HA, a consent with CSRF token and same origin, PKCE S256 or a pairing code (34 bits, 10 minutes, locks after wrong codes); client metadata fetched only from public addresses (SSRF) | done |
| Attacker on the LAN eavesdrops | TLS 1.3 with hybrid post-quantum key agreement; no plaintext outside `localhost` unless the operator puts a TLS-ending reverse proxy in front (`HM_PROXY`), whose leg to Home-Mandate they secure themselves | done: HA client (TLS 1.3, plaintext only to loopback/Supervisor); MCP listener and the UI in direct mode (TLS 1.3, renewed certificates taken over without a restart, a certificate that does not cover the public host is refused) |
| Someone skips the reverse proxy (`HM_PROXY`) or forges the sender | Only the one configured proxy address is served, every other peer gets an empty 403; the sender is the last `X-Forwarded-For` entry, the one the proxy appended, and serves per-sender limits and the log only, never access; without `HM_PROXY` no forwarding header is read | done |
| Forged approval (event from elsewhere) | 128-bit nonce, single use, kept as hash; `context.user_id` must be an approver of the mandate, never Home-Mandate's own HA user; an answer from anyone else denies and warns the approvers; iOS requires unlocking | done |
| Someone who already holds an HA admin token | Outside our control: they could control devices directly anyway. Documentation advises never giving agents HA tokens directly | – |
| Home-Mandate's HA user is an admin (container mode, needed for approval answers) | Dedicated user; fixed allowlist of WebSocket commands and event types in `internal/ha`, enforced before sending and covered by negative tests | done |
| Someone who can write the data directory rewrites the audit log | Hash chain; signed checkpoints every 15 minutes and after every shortening of the log (`home-mandate audit verify` reports how far the log is anchored); once a day the position and digest of the log go to the devices of the approvers, where they cannot be taken back; `home-mandate audit key` exports the public key and log ID to keep elsewhere | done; the key lies in the data directory by default, which protects a copied database but not against someone who reads the whole directory: set `HM_AUDIT_KEY_FILE` to a place outside, and run Home-Mandate under its own user or in a container when agents run on the same machine. Whoever can write the database can also change mandates; a remedy for that needs a key outside the device (planned with the cloud) |
| Compromised supply chain | Pinned dependencies and base images, lockfile with integrity hashes, no install scripts, 7-day minimum release age, `govulncheck` and `pnpm audit` in CI; reproducible builds, signed images, SBOM | pinning and scanning: done; signing and SBOM: planned |
| Database handed out (backup, support) | Database owner-only (0600); tokens only as hashes; the HA credentials are never stored (app mode: `SUPERVISOR_TOKEN`; container mode: environment or a 0600 file), a test checks the data directory for them | done |
| Someone else in the household, or a page on another site, uses the local UI | Only requests from the Supervisor (172.30.32.2), only the HA user the Supervisor names, only if an administrator now (checked with HA, 30 s, fail closed); writes need `Sec-Fetch-Site: same-origin` and a per-user HMAC CSRF token, the event stream the token as its first message; strict CSP without inline code; request limits per user | done |
| Someone on the LAN or the internet uses the UI in direct mode (container mode without Ingress) | Sign-in only through Home Assistant and only for administrators (checked again on every request, 30 s, fail closed); session cookie `__Host-`, `Secure`, `HttpOnly`, `SameSite=Strict`, kept as a hash, new at every sign-in, 30 minutes idle and 12 hours at most, at most 5 per user; the same CSRF token and `Sec-Fetch-Site` rule as behind Ingress; the event stream only from the public URL; no header of the Supervisor or of Home Assistant is trusted; no framing | done |

### Deliberately not covered in v0.1
- Script running in Home Assistant's own origin: Home Assistant serves its frontend, custom
  cards and every Ingress panel of every app under one origin. Code that runs there (an XSS
  in another app's panel, a malicious custom card) is "same-origin" for Home-Mandate's UI,
  can read the session's CSRF token and act with the rights of the signed-in administrator,
  `confirm_critical` included. Home-Mandate cannot separate itself from that origin; it
  keeps its own pages free of injected code (CSP) and logs every change with its actor.
- Compromised Home Assistant host (then everything is lost)
- Attacker with root on the host
- Cloud scenarios (separate threat model)

## Rules for contributions

- Never write tokens, nonces, HA credentials or complete mandates to the log.
- Every new function that triggers actions goes through the PDP. No shortcuts.
- The default is `deny`. Whoever allows something must do so explicitly.
- New dependencies only with a justification in the pull request.
