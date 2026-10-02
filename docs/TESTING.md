# Test strategy

Home-Mandate can open doors. Therefore: **no code without a test, no security feature without
a negative test, no release without green end-to-end tests.**

## 1. Levels

| Level | Tool | Runs | Purpose |
|---|---|---|---|
| Unit | `go test`, table-driven | every commit | Every function, every branch |
| Conformance | Cases from `mandate-spec` (embedded in the Go module) against our own PDP | every commit | Evaluation exactly per specification |
| Negative | Own test cases per package, catalog in section 4 | every commit | Attacks and invalid input are rejected |
| Fuzzing | `go test -fuzz` | nightly, 10 min per target | No panic, unknown input becomes `deny` |
| Integration | Real HA instance in a container | every push | HA client, catalog, service calls |
| End to end | MCP client + OAuth + HA container, UI with Playwright | every push, before every release | Complete flows from the perspective of agent and human |
| UI unit | Vitest + Svelte Testing Library | every commit | Components, form logic, formatting |
| i18n | Own checks in CI (section 5) | every commit | No missing, orphaned or broken translations |
| Mutation | Mutation tests on `mandate-spec/evaluator` | before every release | Tests detect deliberately injected faults |

All Go tests run with `-race`.

## 2. Coverage thresholds (CI fails if not met)

| Scope | Line coverage |
|---|---|
| `mandate-spec/evaluator`, `internal/pdp`, `internal/oauth`, `internal/approval`, `internal/audit`, `internal/api` | ≥ 95 % |
| Other Go packages | ≥ 85 % |
| `web/src/lib` (logic, excluding pure presentation) | ≥ 85 % |
| Mutation score `mandate-spec/evaluator` | ≥ 90 % killed mutants |

Coverage is a lower bound, not a goal. Every decision branch (`allow`, `ask`, `deny`,
protection class, expired, not yet valid) needs its own named test.

## 3. End-to-end environment

- **Home Assistant** as a container (official image, fixed version) with a prepared
  configuration. The built-in `demo` integration provides lights, locks, cameras, alarm panel
  and climate without real hardware.
- **Test users** in HA: `admin-approver` (admin, approver), `admin-other` (admin, not an
  approver), `user-plain` (not an admin).
- **Home-Mandate** as a container from the release image, not from source, so that the shipped
  artifact is tested.
- **Agent** = test client based on the official MCP Go SDK, going through real OAuth flows.
- **Approval requests:** sent to a configurable `notify` service; the answer is fired as a
  `mobile_app_notification_action` event via the HA API with the respective test user, so
  that `context.user_id` is set for real.
- **UI:** Playwright against the UI (mandate editor, emergency stop, revoke agent), every UI
  scenario in German **and** English, embedded under a random Ingress path so that relative
  paths and hash routing are verified.
- Start and teardown via `docker compose` or Podman; every test run starts from a fresh state.

### Mandatory E2E scenarios

1. Pair an agent via code, choose mandate "voice assistant", switch a light → executed, logged.
2. Open door → approval request → `admin-approver` confirms → executed.
3. Open door → approval request → no answer → denied after the timeout, door stays closed.
4. Open door → answer from `admin-other` → discarded, denied, audit entry "invalid approval".
5. Request camera → denied, camera does not appear in `list_devices`.
6. Revoke agent in the UI → next request with the old token denied.
7. Emergency stop → all agents blocked immediately; lifting it → only newly issued tokens work.
8. Exceed the rate limit → refusal from request n+1, logged.
9. Mandate with a time window: request outside of it (test clock) → denied.
10. `user-plain` tries to admit an agent → denied.
11. Restart Home-Mandate → mandates, agents and audit log unchanged, audit chain valid.
12. HA unreachable → requests denied with a clear error, no queue that executes later.

## 4. Negative test catalog

Every line is at least one test. New attack ideas are added here before they are fixed.

**Mandate and evaluation**
- Mandate violates the schema (unknown fields, `default: allow`, `any` with other fields) → rejected when saving
- Action does not fit the category (`unlock` on `light`) → rejected when saving
- Unknown category or entity at runtime → `deny`
- Empty mandate, mandate without rules → everything `deny`
- Time window across midnight, boundaries 00:00 and 23:59, DST change (2026-10-25) → correct
- Critical action with `allow` without `allow_critical` → `ask`
- Edited mandate based on a version that is no longer the current one, or of a revoked mandate → refused as a conflict, nothing stored
- Edited mandate with an `allow_critical` rule that is new, changed in any field or renamed, without the separate confirmation → refused, nothing stored; an unchanged rule needs no new confirmation
- Version that restores an earlier one (same digest) → stored as a new version with its own number; versions are addressed by number

**Tokens and sign-in**
- No token, wrong scheme, expired, revoked, issued for another resource → 401
- Refresh token used twice → whole chain revoked
- PKCE missing or wrong verifier → rejected
- Redirect URI differs (even just in case or by an appended path) → rejected
- Client metadata unreachable, wrong format, client ID ≠ URL → rejected
- Pairing code wrong, expired, used more than once, brute force → locked after n attempts
- Admission by a non-admin → rejected
- Client metadata on a private, loopback or link-local address (also after DNS resolution), other port than 443, redirect, more than 5 KB, repeated keys → rejected without a connection to the private address
- Redirect URI host with characters that could end a CSP directive → rejected
- Sign-in callback without session, with a wrong, reused or expired `state` → rejected; a wrong `state` uses the attempt up
- Session cookie from before the sign-in → worthless afterwards (session fixation)
- Consent without CSRF token, from another origin, or posted twice at the same time → rejected, at most one agent admitted
- Authorization code used twice, expired, for another client, redirect URI or resource → rejected
- Refresh token presented by another OAuth client or for another resource → rejected
- Admission during the emergency stop → no agent, no tokens
- Many sign-ins, pairings or metadata fetches from one sender → refused beyond the per-sender limit

**Approval requests**
- Answer with an unknown, expired or already used nonce → discarded
- Answer from a non-approver (also without user, or from Home-Mandate's own HA user) → request denied as `invalid_response`, approvers warned (decision W8)
- "Yes" and "No" at the same time → first valid answer counts, second discarded, both logged
- Very long or manipulated "reason" from the agent (control characters, Markdown, links) → truncated, sanitized, marked as the agent's claim
- Invalid action parameters → rejected before a human is asked
- Emergency stop, revoked token or changed mandate while the human decides → not executed
- More than 2 pending approval requests of one agent → refused
- No approver set up or reachable → denied at once
- Approver without any channel, more than 5 devices, duplicate device, critical actions in the UI without the UI channel → refused when saving
- Critical request → never sent to a device without critical requests; a person with only such devices (and no UI for critical actions) counts as unreachable for it
- UI channel for someone who is no administrator → refused when saving (`CheckUI`, called by the API); at the time of a request or answer → no UI channel (also when the check fails)
- Answer in the UI by someone who is no approver of the request (also Home-Mandate's own HA user) → refused, request stays open
- Answer in the UI to a critical action without "critical actions in the UI" → refused, request stays open
- Answer in the UI after the UI channel was switched off, the person removed or the administrator rights withdrawn → refused
- Answer in the UI with an unknown, guessed or truncated request ID, or with the nonce → refused; the request ID is never the nonce
- Phone and UI answer at the same time → exactly one counts; the other gets "already answered"
- Answer on either channel after the timeout, a revocation or the emergency stop → no effect
- Revocation or emergency stop in the gateway while a request is open → ended at once, no further notification sent, recorded without approval, denied with the cause
- Approver removed while a request is open → their answer counts as one from anyone else
- Bell in Home Assistant (if switched on) → no agent name, device, reason, link, nonce or request ID in it; removed however the request ends

**MCP interface**
- Unknown tool, missing or extra parameters, wrong types → error without internal details
- Entity outside the mandate in `get_state` → identical response as for a non-existent entity
- Oversized requests, deeply nested JSON → rejected
- Attempt to reach administrative functions via MCP → not present
- Read decision `ask`: device not listed in `list_devices`; `ask` or `deny` on an unreadable entity → same answer as for a non-existent one
- Audit log not writable → nothing executed, nothing read
- Service parameters outside the declared list, type or range; parameters that widen the target (`entity_id`, `area_id`, …) → rejected before Home Assistant
- Attributes carrying access tokens (`entity_picture`, `…token…`, `token=` in values) → never returned
- Agent above its rate limit or without a mandate → refused; refusals logged at most once a minute

**UI**
- Request without CSRF token → rejected
- Ingress request from a source other than 172.30.32.2 → rejected
- Input containing HTML/script → correctly escaped (Playwright checks the rendering)
- Content Security Policy: Playwright reports every CSP violation as a test failure
- Build contains no references to external hosts (check of the `dist/` directory)
- API call without a valid Ingress session or as a non-admin → rejected
- Mandate editor: critical actions without approval → only through the separate confirmation; any edit of the rule's scope, actions or conditions takes the confirmation back
- Edit put on top of a newer version (conflict): a confirmation for critical actions that the newer version took back → dropped, never sent as confirmed
- Save of an edit based on an outdated version → conflict shown, nothing overwritten; an undo of a deleted rule never reaches into a version taken over from the server
- Save summary and version compare: a rule that allows critical actions without approval → never shown as a plain "allowed"; critical changes are never cut from a long list
- Device list not loadable → the save summary says the effect is unknown, never "no effect"
- Draft that would not apply right now (not yet valid, expired, revoked) → the preview says so; a longer validity is flagged in the save summary
- Device, area and agent names with HTML, bidi overrides or control characters in the editor, preview and versions → shown as text, isolated

**Audit log**
- Tampered entry in the database → chain verification fails and reports the position
- No tokens, nonces or HA credentials in logs (a test searches the log output of all E2E runs)

**Home Assistant connection**
- WebSocket command not on the allowlist (ARCHITECTURE section 11.2) → rejected before sending, nothing reaches HA
- `subscribe_events` for an event type not on the allowlist → rejected before sending
- `auth_invalid` → no retry, permanent error state
- Connection lost with requests in flight → they fail immediately, nothing is executed after reconnecting
- Oversized or malformed message from HA → connection closed, no panic
- Plaintext `ws://` to a host other than loopback or the Supervisor, also after DNS resolution → refused; redirects are not followed; untrusted TLS certificate → refused
- Access token in logs, error messages or formatted configuration → never (redacted)

**Storage**
- Checksum of an applied migration changed → start aborted
- Database schema newer than the binary → start aborted (no downgrade)
- Database directory writable by group or others, or not owned by the service user; database file readable by others, a symlink or hard link → start aborted

**Transport**
- TLS 1.2 or older → connection rejected
- Handshake with `X25519MLKEM768` is negotiated when the client offers it

## 5. Checking internationalization

Every commit automatically checks:
- **Completeness:** every key exists in `de` and `en`; no orphaned keys.
- **Placeholders:** same variables in all languages; every message is valid ICU MessageFormat,
  every plural or select has an `other` case, and numbers are written `{count, number}` (never
  `#`, which would not be formatted for the locale).
- **Usage:** every catalog key is used in the sources. Keys of screens not built yet are listed
  in `web/scripts/i18n-pending.json`; a listed key that is used, or that left the catalog, fails
  the check. The list must be empty for the release.
- **No hard-coded texts:** lint rule against visible strings in Svelte components outside the
  message catalogs.
- **Pseudo-localization:** a test build (`pnpm build:pseudo`) with texts lengthened by 40 % and
  accented characters; Playwright checks that nothing is truncated or overflows. The release
  build does not contain it.
- **Formatting:** unit tests for date, time, numbers and relative times in `de-DE` and
  `en-US`, each with a household time zone that differs from the test machine's time zone;
  edge case DST change on 2026-10-25.
- **Server texts:** approval notifications are rendered in both languages and compared against
  stored references.

## 6. Rules

- A bug is first reproduced as a failing test, then fixed.
- Tests check behaviour, not implementation details. No tests that only test mocks.
- No skipped tests on the main branch. Flaky tests are fixed, not disabled.
- Test data contains no real credentials; secrets are generated per run.
