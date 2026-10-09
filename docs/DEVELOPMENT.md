# Development

For people who work on Home-Mandate itself. Users start with the [README](../README.md).

## Repository layout

| Path | Contents |
|---|---|
| `cmd/home-mandate` | The gateway binary: configuration, start-up, the administration commands |
| `internal/…` | Packages of the gateway: `config`, `ha` (Home Assistant client), `catalog` (device directory), `mandate`, `pdp`, `mcp`, `oauth`, `admission`, `agent`, `approval`, `audit`, `store`, `api` (JSON API of the UI), `webui`, `tlscert`, `i18n`, `untrusted`; see [ARCHITECTURE.md](ARCHITECTURE.md), section 3 |
| `web/` | The local UI: Svelte 5, Vite, Paraglide (translations in `web/messages`) |
| `e2e/` | End-to-end tests against a real Home Assistant container and the release image |
| `tools/` | Development tools: `conformance` (test interface of the specification), `covercheck`, `golicenses`, `webconformance`, `cimdserver` and `ingressproxy` (E2E helpers); never part of the binary |
| `app/` | The Home Assistant app: `config.yaml`, translations, `DOCS.md`, `CHANGELOG.md`, AppArmor profile, icons |
| `repository.yaml` | Makes this repository a Home Assistant app repository |
| `docs/` | Architecture, tests, user documentation, `deploy/` examples |
| `Dockerfile` | Multi-stage build: UI (Vite) → Go binary with the embedded UI → `FROM scratch` image |
| `.github/workflows/` | CI (`ci.yml`, `integration.yml`, `scheduled.yml`) and releases (`release.yml`) |

## Requirements

- Go 1.27.2
- Node 24 LTS with corepack (`corepack enable pnpm`)
- Podman, or Docker with `E2E_RUNTIME=docker`, for the image and the E2E tests

## Checks

```bash
make check                  # vet, staticcheck, race tests, coverage per package, govulncheck, actionlint, conformance
make web-install web-check  # UI: lint, svelte-check, Vitest, i18n checks, build, pnpm audit
make web-e2e                # Playwright in German, English and pseudo-locale under a random Ingress path
make e2e                    # E2E scenarios against Home Assistant and the release image
make e2e-ui                 # E2E plus Playwright against the release image (needs web-install and Chromium)
make fuzz FUZZTIME=30s      # fuzz targets
```

Coverage thresholds and the test catalogue are in [TESTING.md](TESTING.md).

Checks run against the version of the specification pinned in `go.mod` (`GOWORK=off` in
the Makefile). To develop against a local checkout of `home-mandate/spec`, create an
untracked `go.work` (`go work init . ../spec`) and pass `GOWORK=$PWD/go.work` to `make`.

UI dependencies are installed from the lockfile without install scripts and only in
versions published at least seven days ago (`web/pnpm-workspace.yaml`). Playwright's
browsers are not npm packages: `pnpm exec playwright install chromium` downloads them from
Playwright's CDN, for tests only.

## Building

```bash
make webui build            # static binaries in bin/ with the embedded UI (without webui: a placeholder page)
make image                  # image home-mandate:dev for this machine (podman; E2E_RUNTIME=docker for docker)
make image VERSION=0.1.0-rc.1
```

The image sets no `USER`: in app mode the Supervisor runs it as root and creates `/data`
and the key in `/ssl` for root ([ARCHITECTURE.md](ARCHITECTURE.md), section 11, decision
3). Container mode runs it unprivileged with a user the operator sets.

## Releases

A tag `vX.Y.Z` or `vX.Y.Z-rc.N` on `main` runs `.github/workflows/release.yml`. It checks
that the tag points to a commit on `main` that came from a pull request whose checks all
passed, and that it matches `version` in `app/config.yaml` (the Supervisor uses that
version as the image tag). It then builds `ghcr.io/home-mandate/ha-home-mandate:<version>`
for `linux/amd64` and `linux/arm64`, signs it with cosign (keyless), attaches provenance
and SBOMs, verifies all of that, tags the image and creates the GitHub release (a
pre-release for `-rc.N`).

Before tagging: update `app/config.yaml` (`version`) and `app/CHANGELOG.md`.

## Contributing

Read the rules for contributions in [SECURITY.md](../SECURITY.md#rules-for-contributions):
never log secrets, every action goes through the decision point, deny is the default, and
new dependencies need a justification. Report vulnerabilities privately, never in a
public issue.
