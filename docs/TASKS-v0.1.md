# Tasks v0.1 – release 2026-10-31

Way of working: every week ends with something that runs in our own household. Tests first
wherever decisions are involved. A box is only done when unit, negative and affected E2E tests
are green and `govulncheck` and `pnpm audit` report nothing.

Two repositories: **[S]** = `mandate-spec`, **[H]** = `home-mandate`.

## Week 1 · 09-29 – 10-05 · Foundation and reference evaluation

- [x] [S] Create the Go module; the root package embeds `schema/`, `examples/`, `conformance/` via `embed`
- [x] [S] `evaluator`: data model, schema validation, evaluation per `SPEC-v0.md` section 4, standard library plus schema validator only
- [x] [S] All conformance cases as Go tests; fuzz test (never panics, unknown input → `deny`); prepare mutation tests
- [x] [S] Tag `v0.1.0-alpha.1`
- [ ] [H] Repository, Go module, `go.work` for local development with [S]
- [ ] [H] CI: `go vet`, `staticcheck`, `go test -race`, `govulncheck`, coverage thresholds from `docs/TESTING.md`, build amd64/aarch64
- [ ] [H] `web/`: Svelte 5 + Vite 8 + TypeScript, Paraglide with `de` and `en`, Vitest, Playwright; CI steps for lint, type checking, tests, i18n completeness
- [ ] [H] `internal/store`: SQLite (pure Go), migrations
- [ ] [H] `internal/ha`: WebSocket connection, authentication, `get_states`, registry queries, reconnection
- [x] **Open decisions 1–5** from `docs/ARCHITECTURE.md`: resolve and record them there
- [ ] Claude Design: first designs for agent list, mandate editor, audit log, approval view; define design tokens

## Week 2 · 10-06 – 10-12 · Decision and enforcement

- [ ] [H] `internal/catalog`: HA entities → category, area, actions (spec section 5), including `gate` via device_class
- [ ] [H] `internal/mandate`: storing, versioning, validating; evaluation via `mandate-spec/evaluator`
- [ ] [H] `internal/pdp`: AuthZEN endpoint, bound internally only, `ask` in the response context; all conformance cases additionally run against this endpoint
- [ ] [H] `internal/mcp`: `list_devices`, `get_state`, `perform_action`, `list_my_permissions`
- [ ] [H] PEP path: token → rate limit → catalog → PDP → execution → audit log
- [ ] [H] `internal/ratelimit`, `internal/audit` (hash-chained, chain verification, 30 days)
- [ ] [H] E2E environment per `docs/TESTING.md` section 3; E2E scenarios 1, 5, 8, 12 green; negative tests for the MCP interface

## Week 3 · 10-13 – 10-19 · Agent onboarding and approval requests

- [ ] [H] `internal/oauth`: metadata (RFC 8414, RFC 9728), Authorization Code + PKCE, Resource Indicators, CIMD, human sign-in via HA account, admins only
- [ ] [H] Device Authorization Grant (RFC 8628) as pairing code
- [ ] [H] Tokens: 256 bits, only the hash stored, access 10 min, refresh 30 days with rotation and reuse detection; revocation and emergency stop
- [ ] [H] `internal/approval`: actionable notification, 128-bit nonce, `context.user_id`, timeout → `deny`, iOS `authenticationRequired`
- [ ] [H] `internal/i18n`: approval texts and server error messages in `de` and `en`
- [ ] [H] Negative tests for tokens, sign-in and approval requests complete; E2E scenarios 2, 3, 4, 6, 7, 10 green

## Week 4 · 10-20 – 10-26 · UI and packaging

- [ ] [H] `internal/api` + `internal/webui`: JSON API, CSRF, strict CSP, Ingress only from 172.30.32.2
- [ ] [H] `web/`: agents (add via code, revoke), mandate editor with "may afterwards" preview, audit log, settings, emergency stop; everything from the Claude Design designs
- [ ] [H] Safe defaults in the editor; `allow_critical` only with a separate confirmation
- [ ] [H] l10n: household time zone, HA units, `Intl` formatting; pseudo-localization without truncated texts
- [ ] [H] App package: `app/config.yaml`, multi-stage Dockerfile, separate app repository; TLS for MCP per decision 1
- [ ] [H] Playwright: UI in `de` and `en` including negative tests; E2E scenarios 9, 11 green
- [ ] [H] Installation on our own HA OS instance

## Week 5 · 10-27 – 10-31 · Hardening and release

- [ ] Own use with Claude Code and a local model, review one week of audit log
- [ ] Security review along `SECURITY.md`: every line has a test
- [ ] Mutation tests `mandate-spec/evaluator` ≥ 90 %; fuzzing without findings
- [ ] All 12 E2E scenarios green against the release image; log search for secrets without hits
- [ ] Reproducible build, signed image, SBOM (Go and JavaScript)
- [ ] README in `de` and `en`: installation, first mandate in 5 minutes, limits of v0.1
- [ ] License files: [H] AGPL-3.0; [S] CC BY 4.0 for the specification, Apache-2.0 for everything else
- [ ] [S] Tag `v0.1.0`; [H] release `v0.1.0` on 10-31

## Cut lines if time runs out

Move to v0.2 in this order, **never** cut security or tests:

1. Container mode (app mode only for v0.1)
2. Device Authorization Grant (Authorization Code only)
3. "May afterwards" preview in the mandate editor
4. UI polish beyond the Claude Design designs

Non-negotiable for v0.1: default `deny`, approval requests for critical actions, token
handling, emergency stop, conformance tests, negative tests from `docs/TESTING.md`, coverage
thresholds, E2E scenarios 2–7, UI in German and English.
