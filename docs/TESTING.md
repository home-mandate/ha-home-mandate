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
- **UI screen sweep** (`web/e2e/sweep.spec.ts`): every screen and state (empty household, all
  banners, emergency stop sheet, no access, start-up error, long and bidi names) in light and
  dark, left-to-right and right-to-left (`dir=rtl` forced, no RTL language yet), at 375 and
  1280 px, in German, English and the pseudo-localized build. Each variant fails on: horizontal
  page scrolling, content clipped without an ellipsis or line clamp, text outside the
  viewport, any WCAG 2.2 A/AA violation found by axe. Two opposite variants are also walked
  with the Tab key only: every stop needs a visible focus indicator and must not be hidden,
  covered or inside inert content. The helpers have their own tests
  (`web/e2e/sweep-helpers.spec.ts`), so a green sweep cannot come from a check that finds nothing.
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
9. Mandate with a time window: request outside of it → denied. There is no test clock in
   the release image (it would be a security-relevant switch, decision U5): the test computes
   the household's local time and sets a window that does not hold now (now + 2 h to + 3 h,
   across midnight handled). The boundaries (midnight, DST change) are unit tests of
   `mandate-spec` and `internal/pdp`.
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
- Pairing in the UI: approve or deny with a `pairing_id` that is not the request behind the code (code reissued or request replaced since the check) → `conflict`, nobody admitted; two approvals at once → exactly one wins
- Pairing lock: a locked session or a global lock also refuses a correct code (no oracle); `Retry-After` is the real remaining time; wrong code and unknown request look alike; the code itself is never logged; parallel wrong codes of one session are counted one after the other (no burst past the limit); the global lock is shared by `/pair` and the UI
- Pairing approved shortly before the code expires → the tokens wait at least 2 minutes for the agent's poll, no admitted agent without tokens
- Applying a template that lacks rules, approval or limits → `invalid_mandate`, nothing stored
- Free client identifier shaped like a URL (`https://…`) → refused, so it can never show as a checked domain
- `requested_from` behind a proxy: `X-Forwarded-For` or `Forwarded` from a peer outside the configured trusted proxies → ignored; the address is normalized and at most 45 characters
- Redirect URIs from client metadata: not `https` (except loopback), with userinfo, fragment, wildcards, control, bidi or format characters, more than 10 or longer than 2048 characters → refused; a later metadata fetch never widens the admitted set
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
- Approvers API (`PUT|DELETE api/approvers/{id}`, test): without an admin session or CSRF token → rejected; a device that is not in the registry, more than 5, a duplicate, no channel, `ui_critical` without `ui`, the UI channel for someone who is no administrator now (checked live, fail closed) → `invalid_input` naming only the field; Home-Mandate's own HA user, a system user or someone without a person → refused; the channels of a person are replaced as a whole in one transaction (the UI applies each change to the newest state; the contract has no version for approvers)
- Reach per kind of request and channel (push, UI only, none) matches the channels and the admin role now; candidate devices carry their owner, and the suggestion for critical requests is on only for the person's own iOS devices
- Test notification: only to the approver's stored devices, neutral text without action buttons or nonce, rate limited per approver and overall (`Retry-After`)
- `system.ha.commands` is generated from the allowlist `internal/ha` really uses (a test fails when they differ); `licenses.txt` is served as `text/plain` with `nosniff`
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
- CSRF token of another user, of a former run, older than two 12-hour periods, twice in the request, or a write without `Sec-Fetch-Site: same-origin` → rejected; the event stream without the token as first message (wrong, extra field, binary, none within 10 s) → closed with 4419, longer than 1 KiB → closed with 1009, from another site (`Sec-Fetch-Site`, or without it an `Origin` other than the host the browser asked for, `X-Forwarded-Host` behind Ingress) or without `Origin` → refused
- Administrator check: no or two `X-Remote-User-Id`, malformed, unknown user, no administrator → refused (also for the UI's event stream and unknown paths); Home Assistant not reachable → 503, never an older answer; rights withdrawn → refused within 30 s, an open event stream closed with 4403
- Request limit per user, test notifications per approver and overall, approval answers per person, verification of the audit log → `rate_limited` with the real `Retry-After`
- Body over 64 KiB, not JSON, unknown or mistyped fields, several objects, a body where none belongs → refused naming at most the field
- Mandate draft with fields beyond the editable ones (`principal`, `default`, `id`) → refused; the identity of a version always comes from the server; a rename without changed rules stores no version but still needs the current version as its base
- Database failure on any endpoint → `internal`, without details, nothing half done (revocation in one transaction)
- The Home Assistant token never in the data directory or the log
- Ingress request from a source other than 172.30.32.2 → rejected; in container mode from any address but `HM_INGRESS_PROXY` (exactly one IP, required with `HM_INGRESS_ADDR`, no range, zone, unspecified or multicast address) → rejected, the Supervisor's address included
- App mode: `supervisor` does not resolve to 172.30.32.2 (or not at all) → the UI stays locked for everyone and an error is logged
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
- Approval request with service data (`set_temperature`, `set_cover_position`, `brightness_pct`, …) → the UI card and its approve confirmation show every field, cleaned like the push text (each name and value at most 80 characters, isolated); the confirmation also carries the critical marker
- Write answered with `csrf_invalid` (token rotated while the page stays open) → the UI fetches a fresh session and repeats the write once; the emergency stop, a revoke and an approval answer work without a page reload; a second refusal is reported
- Agent whose client ID equals a page name (`pair`, `browser`) → its page, revoke dialog and audit filter stay reachable (agent pages live under `#/agents/id/…`)
- URL query with keys named `__proto__`, `constructor`, `toString` or `hasOwnProperty` → treated as plain unknown keys, the page loads
- Live announcement of a new approval request → the agent's name is followed by the "unverified" marker, as everywhere it is shown
- Answer to an approval request in the UI that gets no clear answer from the server (network down, timeout, 5xx from the server or a proxy) → the UI never claims it failed; it says the outcome is unclear and the history shows it; the buttons stay busy until the list shows the outcome
- One agent with the worst-case name (bidi override and isolate, markup, line breaks and tabs, zero-width and blank-looking letters, stacked combining marks, one long word, over 500 characters) and a request reason of the same kind, on every screen that shows agent text (overview, requests and the approval confirmation, audit log and entry, agents, agent page, revoke dialog, mandates, editor, pairing candidate) → no bidi, zero-width or line-break characters reach the page, at most 500 characters, at most two stacked marks, no element or `javascript:` link from it, no overflow at 375 and 1280 px in ltr and rtl (`web/e2e/hostile.spec.ts`; it fails when the cleaning is switched off)
- Agent and display names made only of blank-looking letters (Hangul fillers, braille blank), variation selectors or stacked combining marks → cleaned; a display name without a letter or digit → refused (UI and server alike)
- MCP address with quotes, spaces, `;`, `$`, backticks, a backslash or a line break, or not built from the configuration → no copy-paste command is shown; the server never derives it from request headers
- Revoke whose answer is lost although the server revoked → the UI reloads and shows the agent as revoked; a repeated revoke answers like the first; agent, tokens, mandate and pending approvals end in one transaction
- `apply-template`, `POST mandates` and pairing approval with a template whose rules allow critical actions without approval → refused without the separate confirmation, like a mandate edit; the UI then asks for it in a box that names the template, the agent and every such rule, with Cancel first and focused (Escape cancels, another template drops it), and only "Allow without approval" repeats the request with the confirmation
- Mandate change from an agent's page based on an outdated version → `conflict`, nothing replaced

**Audit log**
- Tampered entry in the database → chain verification fails and reports the position
- No tokens, nonces or HA credentials in logs (a test searches the log output of all E2E runs)
- Search text with `%`, `_`, `\`, quotes, control, bidi or zero-width characters → cleaned, then matched literally (bound parameter, wildcards escaped or `instr`); errors name only `/q`, never the text
- Search text over 100 characters after cleaning, a repeated `q`, or invalid UTF-8 → `invalid_input`, nothing run; empty or whitespace-only `q` → same result as no search
- Search: client and server clean and fold case the same way (shared test vectors incl. ß, İ, Σ/ς, composed/decomposed é); device and area names are matched as the UI shows them
- Search over a large log or catalog (one-letter `q`, 40,000 devices) → finishes within the query timeout, no SQL variable limit hit, writing the log is not blocked
- Search text and the query string never appear in logs
- Agent named like a device or area → the search finds only that agent's own entries, shown as the agent's claim
- Link with a crafted `agent` or `device` filter (hidden characters, look-alike letters, not an HA ID) → filter ignored, never shown as a clean-looking value that filters something else
- Audit log or search requested without an admin session or over MCP → rejected or not present

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
  accented characters; the padding comes in word-sized pieces (at most 8 characters), so it
  lengthens texts the way a real language does instead of adding one unbreakable word.
  Playwright checks every screen for truncation and overflow (screen sweep, section 3). The
  release build does not contain it.
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
