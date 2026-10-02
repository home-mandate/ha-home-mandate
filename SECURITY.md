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

Status as of 2026-10-02; "planned" rows are not protection yet.

| Attacker / scenario | Countermeasure | Status |
|---|---|---|
| **Manipulated agent** (prompt injection via website, e-mail, document) tries to open a door | Decision outside the model; locks default to `ask`; the agent's reason is marked as unverified in approval requests; rate limit against loops | planned (weeks 2–3); evaluation rules in `mandate-spec` done |
| Agent tries to extend its own permissions | Administrative functions are not reachable via MCP; mandates can only be changed through the UI by HA admins | planned (weeks 2–4) |
| Agent explores devices outside its mandate | Unreadable devices are neither listed nor mentioned in error messages | planned (week 2) |
| Stolen agent token | Access tokens 10 minutes; refresh rotation with reuse detection; immediate revocation; emergency stop | planned (week 3) |
| Attacker on the LAN eavesdrops | TLS 1.3 with hybrid post-quantum key agreement; no plaintext outside `localhost` | HA client: done (TLS 1.3, plaintext only to loopback/Supervisor); MCP listener: planned (week 4) |
| Forged approval (event from elsewhere) | 128-bit nonce, single use; `context.user_id` must be an approver; iOS requires unlocking | planned (week 3) |
| Someone who already holds an HA admin token | Outside our control: they could control devices directly anyway. Documentation advises never giving agents HA tokens directly | – |
| Home-Mandate's HA user is an admin (container mode, needed for approval answers) | Dedicated user; fixed allowlist of WebSocket commands and event types in `internal/ha`, enforced before sending and covered by negative tests | done |
| Compromised supply chain | Pinned dependencies and base images, lockfile with integrity hashes, no install scripts, 7-day minimum release age, `govulncheck` and `pnpm audit` in CI; reproducible builds, signed images, SBOM | pinning and scanning: done; signing and SBOM: planned (week 5) |
| Database handed out (backup, support) | Database owner-only (0600); tokens only as hashes; HA credentials encrypted with a separate key | file permissions: done; hashing and encryption: planned (weeks 3–4) |

### Deliberately not covered in v0.1
- Compromised Home Assistant host (then everything is lost)
- Attacker with root on the host
- Cloud scenarios (come with Plus, separate threat model)

## Rules for contributions

- Never write tokens, nonces, HA credentials or complete mandates to the log.
- Every new function that triggers actions goes through the PDP. No shortcuts.
- The default is `deny`. Whoever allows something must do so explicitly.
- New dependencies only with a justification in the pull request.
