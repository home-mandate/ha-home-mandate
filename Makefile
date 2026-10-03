# SPDX-License-Identifier: AGPL-3.0-or-later

# Checks run against the pinned mandate-spec version from go.mod, as in CI.
# For local development against ../mandate-spec: make test GOWORK=$(CURDIR)/go.work
# (go.work is not checked in; a command-line value overrides this, an exported
# GOWORK in the shell does not). This also keeps a go.work in a parent directory out.
export GOWORK := off

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

ACTIONLINT  := github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

# Coverage thresholds per package (docs/TESTING.md section 2). The strict packages are
# enforced as soon as their directory exists.
COVER_DEFAULT := 85
STRICT_PKGS   := internal/pdp internal/oauth internal/approval internal/audit internal/api
COVER_FLAGS   := -default $(COVER_DEFAULT) $(foreach p,$(STRICT_PKGS),$(if $(wildcard $(p)),-min $(p)=95))

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
GOARCHES := amd64 arm64

.PHONY: check test cover vet staticcheck vulncheck actionlint build webui web-install web-check web-conformance web-e2e e2e e2e-ui

## check: everything that must be green before a commit
check: vet staticcheck cover vulncheck actionlint

test:
	go test -race ./...

cover:
	go test -race -coverprofile=cover.out ./...
	go run ./tools/covercheck -profile cover.out $(COVER_FLAGS)

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

actionlint:
	go run $(ACTIONLINT)

## build: static binaries for all release architectures in bin/
build:
	@for arch in $(GOARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false \
			-ldflags="-s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT)" \
			-o bin/home-mandate-linux-$$arch ./cmd/home-mandate || exit 1; \
	done

# Modules that declare a license but ship no license file yet (tools/golicenses); goes
# once mandate-spec ships its license files.
LICENSES_PENDING := -pending github.com/mandate-spec/mandate-spec=Apache-2.0

## webui: build the UI and put it where the binary embeds it, with the licenses of the
## Go code appended to licenses.txt (run before build for a binary with the UI)
webui:
	cd web && pnpm build
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
	cp -R web/dist/. internal/webui/dist/
	go run ./tools/golicenses -file internal/webui/dist/licenses.txt $(LICENSES_PENDING)

## web-install: install the UI dependencies exactly as locked (no install scripts run)
web-install:
	cd web && pnpm install --frozen-lockfile --ignore-scripts

## web-check: lint, type check, unit tests with coverage, i18n checks, build with dist
## check, audit
web-check:
	cd web && pnpm lint && pnpm typecheck && pnpm test && pnpm i18n:check && pnpm build && pnpm audit

## web-conformance: copy the mandate-spec evaluation cases into the UI (a Go test fails
## if the copy differs from the pinned mandate-spec version)
web-conformance:
	go run ./tools/webconformance

## web-e2e: Playwright in de, en and pseudo under a random Ingress path
web-e2e:
	cd web && pnpm e2e

## e2e: scenarios of docs/TESTING.md section 3 against Home Assistant and the release
## image (podman, or docker with E2E_RUNTIME=docker)
e2e:
	cd e2e && go test -tags e2e -count=1 -timeout 25m -v .

## e2e-ui: the E2E scenarios plus Playwright (de, en) against the release image behind the
## Ingress stand-in (needs make web-install and Playwright's Chromium)
e2e-ui:
	cd e2e && E2E_PLAYWRIGHT=1 go test -tags e2e -count=1 -timeout 30m -v .
