# Home-Mandate for Home Assistant

Home-Mandate is an authorization gateway between AI agents and Home Assistant. Every agent
gets its own identity and a **mandate**: which devices it may read or control, where a
human has to approve first, and what it may never do. Approval requests go to your phone
through the Home Assistant Companion app, an emergency stop blocks every agent at once,
and every request is recorded in a tamper-evident audit log.

**Status: experimental.** 0.1.0-rc.2 is the first published release candidate. Expect rough edges,
read the [limits of this version](#limits-of-this-version) and keep backups.

## Why

An AI agent that holds a Home Assistant token can do everything that token can: unlock the
front door, disarm the alarm, open the garage. A prompt injection in an e-mail or a web
page the agent reads is enough to make it try. Home-Mandate keeps Home Assistant's
credentials to itself and decides every request with fixed rules outside the language
model, so no agent can talk itself into more.

## How it works

1. An agent connects to Home-Mandate's MCP endpoint (MCP over HTTPS) and signs in with
   OAuth 2.1; a Home Assistant administrator admits it and picks its mandate.
2. The agent sees four tools: list devices, read a state, perform an action, list its own
   permissions.
3. For every request, Home-Mandate looks up the device in Home Assistant (category, area,
   critical or not) and evaluates the agent's mandate: **allow**, **ask** or **deny**.
4. *Allow* calls Home Assistant; *ask* sends a notification with **Allow** and **Deny** to
   the approvers' phones and waits; *deny*, a refusal, a timeout or no answer means no.
5. Every decision, approval and change is written to a hash-chained audit log with signed
   checkpoints.

## Features

- Mandates with **allow, ask, deny** per device, area, category and action, with time
  windows, weekdays, value limits (brightness, temperature, position, volume) and a rate
  limit per hour
- Critical actions (unlocking, opening a gate or garage door, disarming the alarm, running
  scripts, activating scenes, camera snapshots) need an approval even where a rule allows
  them, unless a rule explicitly allows them without approval
- Covers that may close an entrance (garage, gate, door, and covers without a class) count
  as gates; blinds, shades and windows stay covers
- Approval requests on the approvers' phones (Companion app), optionally also in the
  Home-Mandate UI; cooldown after a refusal, at most two waiting requests per agent
- Emergency stop for all agents at once, revoking single agents, both effective with the
  next request; after the stop, an administrator reconnects each agent to its existing
  entry and mandate when it signs in again
- Revoked agents and mandates can be removed from the lists (one at a time or all at once);
  the audit log keeps every entry and an old mandate version is never accepted again
- Mandate templates (three built in), versioned mandates with a preview of what an agent
  may do, and a comparison of versions
- Audit log with hash chain, signed checkpoints, a daily checkpoint notification to the
  approvers, verification in the UI and on the command line, 30 days retention
- Web UI in English and German; command line administration
- Agents over MCP (Streamable HTTP) with OAuth 2.1: browser sign-in (Authorization Code with
  PKCE and Client ID Metadata Documents) or a pairing code (Device Authorization Grant);
  no open client registration
- One static binary in a `FROM scratch` image for `amd64` and `aarch64`, signed with cosign

## Installation

There are two ways to run Home-Mandate. Both use the same image.

| | Home Assistant OS app | Container mode |
|---|---|---|
| For | Home Assistant OS | Home Assistant Container (Docker or Podman Compose) |
| Install | Add this repository in Home Assistant's app store | Compose file next to Home Assistant |
| UI | Home Assistant sidebar (Ingress) | `https://<your host>/ui/`, signed in through Home Assistant |
| Access to Home Assistant | Through the Supervisor, nothing to configure | Long-lived token of a dedicated Home Assistant user |
| Guide | [docs/install-ha-os.md](docs/install-ha-os.md) | [docs/install-container.md](docs/install-container.md) |

Then continue with [docs/usage.md](docs/usage.md) to set up approvers and mandates, and
[docs/agents.md](docs/agents.md) to connect your first agent.

## Running in container mode

The complete guide is [docs/install-container.md](docs/install-container.md): the
dedicated Home Assistant user, the token file, running without root as `65532:65532`
([data directory ownership](docs/install-container.md#run-without-root)), the three ways to
expose Home-Mandate, the annotated Compose file and the
[reference of all environment variables](docs/install-container.md#environment-variables).
Error messages about file permissions point here; the fixes are in
[docs/troubleshooting.md](docs/troubleshooting.md#start-up-errors).

## Security in short

- Agents never see a Home Assistant token. They get short-lived OAuth tokens (10 minutes,
  refresh tokens 30 days, rotated) bound to Home-Mandate.
- No agent is admitted without a Home Assistant administrator who signs in through Home
  Assistant and picks the mandate.
- The decision is made by fixed rules (the reference evaluator of the Home-Mandate
  specification), never by a language model. The default is deny.
- Devices an agent may not read do not exist for it: they are neither listed nor named in
  errors.
- The UI is for Home Assistant administrators only; the check is repeated on every request.
- TLS 1.3 only; no plaintext outside `localhost`, unless you put a TLS-ending reverse proxy
  in front and name it (`HM_PROXY`, container mode).
- In container mode Home-Mandate needs a Home Assistant user with **administrator rights**.
  The only reason is that Home Assistant lets only administrators subscribe to
  `mobile_app_notification_action`, the event that carries the answers to approval
  requests. Home-Mandate sends only a fixed list of WebSocket commands
  ([docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), section 11, decision 2); the UI shows
  that list under Settings → Home Assistant connection.

The threat model and how to report vulnerabilities: [SECURITY.md](SECURITY.md).

## Limits of this version

- Experimental release candidate. The Home Assistant OS app is first tested on real
  installations with this release candidate; container mode is covered by the end-to-end
  tests against a real Home Assistant.
- Camera snapshots and `set` on devices of the category `other` are evaluated and logged but
  not executed: Home Assistant offers no safe way to perform them for one entity.
- Reading a device with the decision *ask* is refused; only actions are confirmed by a
  human.
- An approval waits at most 10 minutes (the approval timeout setting, 30 to 600 seconds),
  although a mandate may name up to an hour: the agent's request waits for the answer.
- Open approval requests live in memory: a restart ends them without execution.
- Reconnecting after an emergency stop is offered only for agents of the same OAuth client
  without valid access, and only on the sign-in or pairing page when the agent signs in
  again; the administrator chooses the agent, Home-Mandate never matches one by itself.
- Removed agents and mandates are hidden at once; their data is deleted only once the audit
  log no longer mentions them (30 days after their last entry), not on request.
- In the Home Assistant OS app, a reverse proxy that ends TLS (`HM_PROXY`) is not available;
  agents from outside need a TLS passthrough or a port forward
  ([docs/install-ha-os.md](docs/install-ha-os.md#reaching-home-mandate-from-outside)).
- In container mode the UI needs a certificate or a reverse proxy and an `https://` public
  URL; without them only the command line manages Home-Mandate.

## Documentation

| Document | Contents |
|---|---|
| [docs/install-ha-os.md](docs/install-ha-os.md) | Install, configure, update, back up and remove the Home Assistant OS app |
| [docs/install-container.md](docs/install-container.md) | Container mode with Docker or Podman Compose, all environment variables |
| [docs/deploy/reverse-proxy.md](docs/deploy/reverse-proxy.md) | Reaching Home-Mandate from outside behind Traefik, nginx, Nginx Proxy Manager or Caddy |
| [docs/usage.md](docs/usage.md) | Concepts, first steps in the UI, approvals, emergency stop, audit log, command line |
| [docs/agents.md](docs/agents.md) | Connecting agents: Claude Desktop, Claude Code, claude.ai, other MCP clients; tools and errors |
| [docs/troubleshooting.md](docs/troubleshooting.md) | Start-up errors, sign-in, approvals, unavailable answers, rate limits |
| [SECURITY.md](SECURITY.md) | Threat model, reporting vulnerabilities |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Architecture and design decisions (for developers and reviewers) |
| [docs/TESTING.md](docs/TESTING.md) | Test strategy (for developers and reviewers) |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | Repository layout, development setup, checks, building the image |
| [app/CHANGELOG.md](app/CHANGELOG.md) | Changes per version |

## Related repositories

| Repository | Contents | License |
|---|---|---|
| `home-mandate/spec` | Home-Mandate specification: vendor-neutral specification, schema, conformance cases, reference evaluator, test tool | CC BY 4.0 / Apache 2.0 |
| `home-mandate/ha-home-mandate` (this one) | Gateway, local UI, Home Assistant app | AGPL-3.0-or-later |

Home-Mandate embeds the reference evaluator of `home-mandate/spec` as a Go module and must
pass all its conformance cases.

## License

AGPL-3.0-or-later, see [LICENSE](LICENSE). The licenses of all included components are in
the UI under Settings → About → Licenses of the included packages.
