# Test strategy

Home-Mandate can open doors. Therefore: **no code without a test, no security feature without
a negative test, no release without green end-to-end tests.**

## 1. Levels

| Level | Tool | Runs | Purpose |
|---|---|---|---|
| Unit | `go test`, table-driven | every push to a branch | Every function, every branch |
| Conformance | Cases from `home-mandate/spec` (embedded in the Go module) against our own PDP; `mandate-conformance` against `tools/conformance` (process binding: classes evaluator, selection, audit, audit-anchored; HTTP binding: class pdp), `make conformance` | every pull request (Go cases: every push) | Evaluation and selection exactly per specification, through Home-Mandate's own decision path |
| Negative | Own test cases per package, catalog in section 4 | every push to a branch | Attacks and invalid input are rejected |
| Fuzzing | `go test -fuzz`, `make fuzz` | every pull request 60 s per target; 1st and 15th of every month 60 min | No panic, unknown input becomes `deny` |
| Integration | Real HA instance in a container | every pull request | HA client, catalog, service calls |
| End to end | MCP client + OAuth + HA container, UI with Playwright | every pull request | Complete flows from the perspective of agent and human |
| UI unit | Vitest + Svelte Testing Library | every push to a branch | Components, form logic, formatting |
| i18n | Own checks in CI (section 5) | every push to a branch | No missing, orphaned or broken translations |
| Mutation | Mutation tests on `home-mandate/spec/evaluator` | before every release | Tests detect deliberately injected faults |

All Go tests run with `-race`.

Everything is checked before the merge: a push to a branch runs the unit stage, a pull
request against main additionally the conformance run, short fuzzing, the image, E2E and
Playwright. main takes squash-merged pull requests only, up to date and with every required
check green, so nothing runs after the merge; every merge is tagged. Independent of changes,
`govulncheck` and `pnpm audit` run every night and long fuzzing twice a month; a failure opens
an issue.

## 2. Coverage thresholds (CI fails if not met)

| Scope | Line coverage |
|---|---|
| `home-mandate/spec/evaluator`, `internal/pdp`, `internal/oauth`, `internal/approval`, `internal/audit`, `internal/api` | ≥ 95 % |
| Other Go packages | ≥ 85 % |
| `web/src/lib` (logic, excluding pure presentation) | ≥ 85 % |
| Mutation score `home-mandate/spec/evaluator` | ≥ 90 % killed mutants |

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
- **Sign-in, consent and pairing pages in a real browser** (`web/e2e-live/oauth.spec.ts`,
  started by `e2e/oauth_test.go`, `make e2e-ui`): Chromium drives the authorization server's
  pages against the release image in German and English, with Home Assistant's real login
  page; Go HTTP clients set request headers themselves and miss what browsers do. The spec
  also plays the agent: it reads the metadata, starts the Authorization Code flow with PKCE
  and a loopback redirect, exchanges the code and calls the MCP endpoint; and it pairs by
  code, entered in the browser, and polls for the tokens. The client ID metadata document
  comes from a test-only server (`tools/cimdserver`) on port 443 in an internal test network
  of its own, in a subnet the gateway's address rules do not reserve, with a certificate of
  the test CA that the gateway trusts through `SSL_CERT_FILE` in the test run only: the
  release image keeps refusing private addresses and is not changed for the test. Every
  test fails on a CSP violation, a console or page error on the gateway's pages, or a page
  of ours without its policy.
- **UI screen sweep** (`web/e2e/sweep.spec.ts`): every screen and state (empty household, all
  banners, emergency stop sheet, no access, start-up error, long and bidi names) in light and
  dark, left-to-right and right-to-left (`dir=rtl` forced, no RTL language yet), at 375 and
  1280 px, in German, English and the pseudo-localized build. Each variant fails on: horizontal
  page scrolling, content clipped without an ellipsis or line clamp, text outside the
  viewport, any WCAG 2.2 A/AA violation found by axe. Two opposite variants are also walked
  with the Tab key only: every stop needs a visible focus indicator and must not be hidden,
  covered or inside inert content. The helpers have their own tests
  (`web/e2e/sweep-helpers.spec.ts`), so a green sweep cannot come from a check that finds nothing.
- **Unsaved changes in the editors** (`web/e2e/unsaved.spec.ts`, de, en and pseudo): after
  "Done" a changed rule says that it is not saved yet and the live region says it too; the
  save bar sits at the bottom of the viewport at 375 and 1280 px and the end of the page
  stays reachable above it; an edit left earlier comes back with a warning; saving
  ends bar and hint with a new version; reloading with unsaved changes (mandate and
  template) brings up the browser's prompt, after saving it does not. The sweep has these
  states as screens of their own.
- **Save bar on scrolled pages** (`web/e2e/savebar.spec.ts`, de, en and pseudo, at 1280×800,
  1440×900 and 375×812, mandate and template editor): scrolled to the middle, a change makes
  the bar appear without moving the page or the focus; the bar's bottom is the viewport's
  bottom; the focused element and every keyboard stop stay uncovered; at the very end the
  last rule lies above the bar; saving removes it without a jump.
- **Colour scheme switch** (`web/e2e/theme.spec.ts`, de, en and pseudo): the header switch
  changes the scheme at once and the choice survives a reload; blocked storage or an unknown
  stored value falls back to the system scheme; at 375 px the compact switch sits next to the
  emergency stop without overflow; the main screens keep WCAG 2.2 AA with the scheme forced
  against the opposite system scheme. `web/src/lib/tokens/tokens.test.ts` keeps forced and
  system dark tokens equal and checks the contrast of the token pairs in both schemes.
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
   `home-mandate/spec` and `internal/pdp`.
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
- Version that restores an earlier one → stored as a new version with the next number and the next `version` (SPEC-v0 section 3.5), so its digest differs; versions are addressed by number
- Older version offered again with its own `version` (rollback), also after a revocation → refused; a mandate of another `issuer` → refused; a mandate stored before versions existed → still evaluated, its next change gets version 1
- Agent whose only mandate is revoked, or whose agent is revoked → `no_mandate` (revoked mandates are no candidates, SPEC-v0 section 4.3)
- Stored version that no longer parses after a stricter specification, or whose content no longer has its digest (changed in the database) → `invalid_mandate`, never evaluated; `home-mandate mandate check` lists it
- Entity in a spelling the directory does not have (`Lock.keller` for `lock.keller`) → `unknown_resource`
- Rule that names a device together with a category the device does not have → refused when saving (mandate and template); a device the directory does not know → stored and reported as a stale reference
- Parameter that is no integer in the unit of the vocabulary (21.555 °C, a temperature in a °F household) → not passed, a rule with a limit on it does not match; a call that sets one quantity through two fields (`brightness` and `brightness_pct`) → refused; what is executed is exactly the evaluated value
- Limits on a `deny` or `ask` rule, on "all actions" or on an action without that value → refused when saving; the editor reports them and never drops them silently (that would widen an allow)

**Resource directory**
- Entity renamed in Home Assistant (event, or registry ID after an outage) → never rewritten on its own; for a mandate that names the former ID the stricter of both evaluations wins until a human takes the rename over or dismisses it (a broad allow rule does not slip past a deny or ask rule on the former ID); the mandate list offers both, the editor says so; the edit in progress is kept
- Taking a rename over with a rule that allows critical actions without approval → refused without the separate confirmation, no mandate changed; dismissing asks inline first; a rename to a device whose category does not fit the rule → refused; a rename back undoes the rename; a rename that cannot be stored stays in force in memory
- Renamed entity that was marked critical → the mark moves to the new ID, the old ID keeps it; a failed move is logged as an error
- Rename event with the same ID, without an ID, with a space, control character or more than 255 characters, or not an update → ignored; at most 1000 renames are kept between two refreshes
- Area removed → rules on it reported like a renamed device; `deny` or `ask` rule that names only an area → the editor says that a device moved elsewhere leaves it
- Directory not loaded (start, connection lost) → nothing reported as missing, every request denied
- Critical mark set or removed → only by an administrator with CSRF token; an entity the directory does not know → `not_found`; every change is a `directory.changed` audit entry in the same transaction (a change that cannot be recorded is not made), failed attempts are in the server log
- Template stored, removed, hidden or shown, approver added or removed (UI and command line) → exactly one `template.changed` or `approver.changed` entry in the same transaction, the human as actor (`local-admin` on the command line); unchanged saves, hiding a hidden template, other channels for an approver, refused and unrecordable changes → no entry and no change; `audit verify` and the export include the entries and verify with the specification
- Renames that cannot be stored → held up to 1000, beyond that the catalog is not ready and every request is denied; storing that fails for two minutes → banner; a mark that could not be carried → carried with the next refresh, also after a restart
- Rename of an entity no active mandate names → stored, no audit entry; if it cannot be told which entities mandates name → recorded; more than 50 renames in an hour → notice in the UI and a notification to the approvers, at most once an hour
- Directory changes in the audit log: marks set and removed by a user, carried by the system; renames found by the system, taken over or dismissed by a user, one entry per former ID; an agent as actor, a rename without its former ID or to itself → invalid entry

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
- Client metadata on a private, loopback, link-local, site-local, IPv4-compatible (`::/96`) or Teredo address (also after DNS resolution), other port than 443, redirect, more than 5 KB, repeated keys → rejected without a connection to the private address
- Redirect URI host with characters that could end a CSP directive → rejected
- Sign-in callback without session, with a wrong, reused or expired `state` → rejected; a wrong `state` uses the attempt up
- Session cookie from before the sign-in → worthless afterwards (session fixation)
- Consent without CSRF token, from another origin, or posted twice at the same time → rejected, at most one agent admitted
- Authorization code used twice, expired, for another client, redirect URI or resource → rejected
- Refresh token presented by another OAuth client or for another resource → rejected; a used one from another client revokes nothing (the family stays) and is logged as `auth.rejected`
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
- Admission preview of who may approve (consent page and pairing in the UI): a placeholder or named approver without any channel → marked; nobody reachable for the template's ordinary or critical requests → warned, admission still possible; Home Assistant not answering → unknown, never reachable; Home-Mandate's own user → never counted; names from Home Assistant with markup → shown as text; a hidden or unknown template → `not_found`
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
- Approvers API (`PUT|DELETE api/approvers/{id}`, test): without an admin session or CSRF token → rejected; a device that is not in the registry, more than 5, a duplicate, no channel, `ui_critical` without `ui`, the UI channel for someone who is no administrator now (checked live, fail closed) → `invalid_input` naming only the field; Home-Mandate's own HA user, a system user or someone without a person → refused; the channels of a person are replaced as a whole in one transaction; every change carries the version of the approvers it is based on (`base_version`, missing or malformed → `invalid_input`), the first change wins and one on an older version → `conflict` (409), the UI reloads and says so
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
- Log rewritten consistently by someone who can write the database → `audit verify` reports it anchored only up to the last checkpoint; a checkpoint signed with another key or for another log ID → invalid
- Shortening of the log with an agent as actor → refused; every shortening is followed by a checkpoint
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

**Test interface (SPEC-v0 section 10.1)**
- The release binary depends on `tools/conformance` or the harness of the specification → a test fails (`go list -deps ./cmd/home-mandate`)
- HTTP binding without or with a wrong bearer token → 401; a token shorter than 32 characters or an address other than loopback → refused at start

**Mandate templates**
- Storing, changing, removing or hiding with a name starting with `hm-` that is no base template → refused; changing or removing a base template → refused (`builtin_template`), saving it under a new name works
- Hidden base template → not offered at admission or for a new mandate, and refused when named anyway
- `$approvers` → replaced at admission by the admitting human plus every approver, without duplicates; nobody to put there (command line without approvers) → admission refused; any value starting with `$` in a mandate (also at rule level) → storing refused
- Changing a template without the digest of the version it started from, or with an outdated one → `conflict`, nothing overwritten
- Template allowing critical actions without approval → stored only with the separate confirmation, as for mandates
- Plain-words summary of a template on the consent page and in the UI: every allowed, asked and denied category is named, the default (`deny`) is said; names and descriptions are escaped
- Template changed between showing it and approving it (consent page or the UI's pairing) → nobody admitted (`conflict`); a choice without the digest of what was shown → refused
- Two edits of the same template version at once (also from another process such as the command line) → exactly one is stored, the other is a conflict
- Template granting critical actions without approval → not offered on the consent page (which asks for no separate confirmation); admitted only through the UI with it
- Device IDs, areas and names in a template's summary containing markup → shown as text

**Sign-in, consent and pairing pages**
- In a real browser (`web/e2e-live/oauth.spec.ts`): approving sends the code to the agent's loopback redirect, which the consent page's `form-action` allows, and only the PKCE verifier redeems it, once; denying sends `access_denied` without a code; someone who is no administrator is refused after Home Assistant's sign-in and the agent hears nothing; a hidden base template is not offered; exactly the approved agents are admitted, by the human who approved them
- Referrer policy `no-referrer` on these pages → a browser sends `Origin: null` with the consent and pairing forms, which the same-origin check refuses: the pages keep `same-origin` (header and meta), which sends the real origin to themselves and no referrer to other origins; a form post with `Origin: null` or another origin stays refused

**UI in direct mode (container mode without Ingress)**
- No certificate or no `https://` public URL → no UI, `/ui/` answers 404
- Request without session cookie, with an unknown, guessed, expired (12 hours) or idle (30 minutes) session, or after a restart → API `unauthenticated`, page sends to the sign-in
- Sign-in callback without the sign-in cookie, with another `state`, a reused or malformed code, or a code Home Assistant refuses → refused, no session; the Home Assistant tokens of a sign-in are revoked in every case
- Signed-in user who is no administrator, or not active → no session; administrator rights withdrawn later → refused within 30 s, an open event stream closed with 4403
- Session ID unchanged by the sign-in (fixation) → impossible: every sign-in issues a new ID and the sign-in cookie is cleared
- Cookie without `__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Strict` or with a domain → test fails
- Sign-out without CSRF token or from another site → refused; after sign-out the old cookie is worthless
- Sixth session of a user → the oldest ends; all sessions taken → the sign-in says "busy"
- Flood of sign-in starts (one address or many) → the oldest sign-in in progress gives way (of the address first), never a refusal for everyone; the table stays bounded and nothing else grows with unauthenticated requests
- `X-Remote-User-Id` or `X-Forwarded-Host` sent by the client in direct mode → ignored, never a user or an origin; an agent's bearer token on `/ui/api` → no user
- Path tricks on the prefix (`/ui/../api`, `/ui/api%2f…`, `/ui//api`, encoded dots) → never a session without signing in
- Inactive Home Assistant user → no session
- Event stream with an `Origin` other than the public URL, or none → refused
- Page framed by another page → refused by CSP (`frame-ancestors 'none'`)

**Transport**
- TLS 1.2 or older → connection rejected
- Handshake with `X25519MLKEM768` is negotiated when the client offers it
- Plaintext HTTP to the TLS port → no answer in plaintext
- Certificate that does not cover the host of the public URL → start aborted; as a renewal → not taken over, the previous one stays and the error is logged and shown
- Renewed certificate and key → the next handshake uses them, without a restart, also when size and modification time stayed the same (compared by content); a key that does not belong to the certificate, a half-written, unreadable or oversized file, or a renewal not valid now (expired, not yet valid) → previous pair stays, retried at the next look (at most once a minute); the UI's status looks at the files too, without a handshake
- Certificate valid for less than 14 days → warning in the log and in the UI

**Behind a reverse proxy (`HM_PROXY`)**
- `HM_PROXY` as a range, a list, a host name, with a zone, unspecified or multicast → start refused; without an https `HM_PUBLIC_URL` → start refused; without `HM_PROXY` plaintext beyond loopback stays refused
- `HM_PROXY` in app mode → start refused (not available there yet)
- Request from any address but the proxy (also one whose `X-Forwarded-For` names the proxy) → empty 403, on every path; logged at most once a minute
- Sender: the last entry of the last `X-Forwarded-For` line; client-written earlier entries ignored; missing, empty, with a port or no address → the proxy itself
- Without a certificate of its own: UI in direct mode, sign-in redirect and OAuth issuer from the public URL, MCP address shown in the UI; the UI says TLS ends at the proxy instead of warning about a missing certificate

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

## 7. Obligations of the PEP (SPEC-v0 section 11)

How Home-Mandate meets each obligation and which tests show it. Gaps are listed, not hidden.

### 11.1 Approval

| Item | How | Tests | Gap |
|---|---|---|---|
| 1 Who | A push answer counts only from a `context.user_id` among the approvers of the request (critical requests, including those on devices the household marked, only on devices where critical requests are on); anyone else denies as `invalid_response` and warns the approvers; the UI checks approver and administrator now; nobody reachable → `denied: no_approver` | `approval.TestAnswerFromANonApprover`, `approval.TestAnswerWithoutUserIsInvalid`, `approval.TestNoApproverCanBeReached`, `approval.TestUIAnswerRejectsUnknownRequestsAndUsers`, `approval.TestWithdrawnApprover`, `api.TestAnswerRefusals`, `mcp.TestRefusedApprovals` | |
| 2 What is confirmed | The nonce belongs to one pending request (agent, entity, action, parameters); after the answer the digest of a new evaluation must equal the confirmed one; the push and the UI show the agent's name with its `client_id`, the device with its entity ID, and the reason marked as the agent's claim | `approval.TestParametersAreShown`, `approval.TestShownParamsAndText`, `approval.TestSanitize`, `mcp.TestApprovalShowsTheServiceData`, `mcp.TestRequestIsMarkedAndChannelRecorded`, `api.TestOpenApprovals`, `approval.TestPushNamesAgentAndDeviceByID` | |
| 3 Once | 128-bit nonce from `crypto/rand`, stored as a hash, first answer wins | `approval.TestFirstAnswerCounts`, `approval.TestForeignAndMalformedAnswersAreIgnored`, `approval.TestSecondAnswerAfterTheEnd`, `approval.TestPhoneAndUIRace`, `approval.TestThreeWayRace` | |
| 4 Independent of the agent | MCP has four tools, none answers; answers come only from Home Assistant notification events or the administrator UI behind Ingress | `mcp.TestOnlyTheFourToolsExist`, `api.TestOnlyTheSupervisorIsServed`, `api.TestAPINeedsAnAdministrator` | |
| 5 In time | The wait ends at the shorter of the rule's timeout and `HM_APPROVAL_TIMEOUT`; rejection and invalid answers deny; right before the call the confirmation must be younger than that timeout, and the call ends at that point at the latest | `approval.TestTimeout`, `approval.TestMandateTimeoutShortensTheWait`, `approval.TestDefaultUpperLimit`, `mcp.TestExpiredConfirmationIsNotExecuted`, `mcp.TestApprovalValidity`, `mcp.TestRefusedApprovals` | |
| 6 Evaluated again | After the answer: emergency stop, token and a fresh evaluation; executed only if not `deny` and the digest is unchanged | `mcp.TestChecksAfterApproval`, `mcp.TestRevokedTokenAfterApproval`, `mcp.TestMandateUnavailableAfterApproval` | Expiry and a closing time window after the answer are covered by the fresh evaluation, without tests of their own |
| 7 Limited | At most 2 waiting requests per agent, further ones `denied: approval_pending`; the rate limit runs before the decision, so `ask` counts | `mcp.TestPendingAsksAreBounded`, `mcp.TestRateLimit`, `mcp.TestAskRequestsCountTowardsTheRateLimit` | |
| Audit | Every outcome with `approval.outcome`, `by`, `via`; a cancelled request with `denied_by` | `mcp.TestCancelledApprovals`, `mcp.TestApprovalEntriesNameTheirRequest`, `audit.TestApprovedAskIsRecordedWithApproval`, `audit.TestApprovalChannelAndCancellation` | |

### 11.2 Rate limit

| Item | How | Tests | Gap |
|---|---|---|---|
| N in every 3600 s | `home-mandate/spec/ratelimit` keeps the timestamps of the last hour | `mcp.TestRateLimit`; in the specification `TestNoBurstAfterAnIdlePeriod`, `TestWindowBoundaryIsExact` | |
| Counted per mandate | Key `mandate:<id>`; without a mandate the agent is counted on its own; restored per mandate | `pdp.TestRateLimitOfTheCandidates`, `pdp.TestRateKeyIsTheMandate`, `audit.TestRequestsSince` | |
| Every request counts | The limiter runs before every decision and before lists | `mcp.TestRateLimit`, `mcp.TestAgentsWithoutMandateAreRateLimited` | |
| Refusals by the limit or the stop do not count | The stop is checked first; the limiter does not count its own refusals | `mcp.TestEmergencyStopIsEnforcedByThePEP`; in the specification `TestDeniedRequestsDoNotCount` | |
| Survives a restart | Restored from the decisions of the last hour in the audit log | `cmd/home-mandate.TestRestoredLimiterCountsTheLastHour`, `audit.TestRequestsSince` | Lists leave no audit entry and are not restored |
| Change of N | Read from the candidates for every request | `pdp.TestRateLimitOfTheCandidates`; in the specification `TestLoweredLimitAppliesAtOnce` | |
| Compaction | At most one rate-limit entry per agent and interval | `mcp.TestRateLimitRefusalsAreLoggedOncePerInterval` | |

### 11.3 Revocation and emergency stop

| Item | How | Tests |
|---|---|---|
| Next evaluation, no caching | Candidates are read for every request; revoked mandates and agents are no candidates | `mandate.TestCandidatesAreTheActiveMandatesOfAnActiveAgent`, `pdp.TestSelectionCasesOverHTTP`, `mcp.TestAuthentication` |
| Waiting approvals end | Revoking a mandate or an agent cancels its requests | `api.TestRevokeMandate`, `api.TestRevokeAgent`, `approval.TestCancel`, `mcp.TestCancelledApprovals` |
| Emergency stop | Checked before everything, `denied_by: emergency_stop`, ends waiting requests, recorded, released only by a human (UI or CLI) | `mcp.TestEmergencyStopIsEnforcedByThePEP`, `mcp.TestEmergencyStopRefusesLists`, `api.TestEmergencyStop`, `cmd/home-mandate.TestEmergencyStopCommand` |

### 11.4 Clock and directory

| Item | How | Tests | Gap |
|---|---|---|---|
| Clock | Nothing is decided while the clock lies more than a minute behind the latest time in the audit log (`clock_behind`); a failed check counts as behind; `audit accept-clock` after a clock that ran ahead | `audit.TestClockBehindTheNewestEntry`, `mcp.TestNothingIsDecidedWhileTheClockIsBehind`, `api.TestSystem` | |
| Directory | Exact lookup; unknown → `unknown_resource`; a rename keeps the rules on the former ID in force until a human resolves it, marks carried | `pdp.TestUnknownEntityIsDenied`, `pdp.TestRulesOnAFormerIDKeepApplying`, `catalog.TestRenamesTakeEffectAtOnce`, `catalog.TestRegistryIDsFindRenamesAfterAnOutage`, `api.TestRenamesAreListedAppliedAndDismissed` | |

