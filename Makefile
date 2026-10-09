# SPDX-License-Identifier: AGPL-3.0-or-later

# Checks run against the pinned version of the specification from go.mod, as in CI.
# For local development against ../spec: make test GOWORK=$(CURDIR)/go.work
# (go.work is not checked in; a command-line value overrides this, an exported
# GOWORK in the shell does not). This also keeps a go.work in a parent directory out.
export GOWORK := off

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

ACTIONLINT  := github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

# Fuzz targets as package:target; FUZZTIME each (short in pull requests, long twice a month).
FUZZTIME     ?= 10m
FUZZ_TARGETS := internal/ha:FuzzDecodeMessage

# Coverage thresholds per package (docs/TESTING.md section 2). The strict packages are
# enforced as soon as their directory exists.
COVER_DEFAULT := 85
STRICT_PKGS   := internal/pdp internal/oauth internal/approval internal/audit internal/api
COVER_FLAGS   := -default $(COVER_DEFAULT) $(foreach p,$(STRICT_PKGS),$(if $(wildcard $(p)),-min $(p)=95))

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
GOARCHES := amd64 arm64

.PHONY: check test cover vet staticcheck vulncheck actionlint conformance fuzz build image webui web-install web-check web-conformance web-e2e e2e e2e-ui

## check: everything that must be green before a commit
check: vet staticcheck cover vulncheck actionlint conformance

test:
	go test -race ./...

cover:
	go test -race -coverprofile=cover.out ./...
	go run ./tools/covercheck -profile cover.out $(COVER_FLAGS)

vet:
	go vet ./...

# Temporary (2026-10-09): staticcheck v0.8.1 cannot read Go 1.27.2's export data
# (dominikh/go-tools#1832). A run whose only output is that error counts as a warning;
# any other output still fails. Remove once a compatible staticcheck release is pinned.
STATICCHECK_EXPORT_DATA := export data version 5 is greater than maximum supported version 4
staticcheck:
	@out=$$(go run $(STATICCHECK) ./... 2>&1); status=$$?; \
	if [ $$status -ne 0 ] && echo "$$out" | grep -qF "$(STATICCHECK_EXPORT_DATA)" && \
	   ! echo "$$out" | grep -vF -e "$(STATICCHECK_EXPORT_DATA)" -e "exit status" | grep -q .; then \
		msg="staticcheck skipped: $(STATICCHECK) cannot read this Go version's export data (dominikh/go-tools#1832)"; \
		if [ -n "$$GITHUB_ACTIONS" ]; then echo "::warning::$$msg"; else echo "WARNING: $$msg"; fi; \
		exit 0; \
	fi; \
	[ -z "$$out" ] || echo "$$out"; exit $$status

vulncheck:
	go run $(GOVULNCHECK) ./...

actionlint:
	go run $(ACTIONLINT)

## fuzz: each fuzz target for FUZZTIME, e.g. make fuzz FUZZTIME=30s
fuzz:
	@for spec in $(FUZZ_TARGETS); do \
		pkg=$${spec%%:*}; target=$${spec#*:}; \
		go test ./$$pkg/ -run '^$$' -fuzz "^$$target\$$" -fuzztime $(FUZZTIME) || exit 1; \
	done

## conformance: mandate-conformance against Home-Mandate's PDP over both bindings of the
## test interface (SPEC-v0 section 10); the test tool is never part of the binary
conformance:
	tools/conformance/run.sh

## build: static binaries for all release architectures in bin/
build:
	@for arch in $(GOARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false \
			-ldflags="-s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT)" \
			-o bin/home-mandate-linux-$$arch ./cmd/home-mandate || exit 1; \
	done

## image: the container image for this machine's architecture, tagged home-mandate:$(VERSION)
## (podman, or docker with E2E_RUNTIME=docker)
image:
	$(or $(E2E_RUNTIME),podman) build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t home-mandate:$(VERSION) .

## webui: build the UI and put it where the binary embeds it, with the licenses of the
## Go code appended to licenses.txt (run before build for a binary with the UI)
webui:
	cd web && pnpm build
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
	cp -R web/dist/. internal/webui/dist/
	go run ./tools/golicenses -file internal/webui/dist/licenses.txt

## web-install: install the UI dependencies exactly as locked (no install scripts run)
web-install:
	cd web && pnpm install --frozen-lockfile --ignore-scripts

## web-check: lint, type check, unit tests with coverage, i18n checks, build with dist
## check, audit
web-check:
	cd web && pnpm lint && pnpm typecheck && pnpm test && pnpm i18n:check && pnpm build && pnpm audit

## web-conformance: copy the evaluation cases of the specification into the UI (a Go test fails
## if the copy differs from the pinned version of the specification)
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
## Ingress stand-in, and on the sign-in, consent and pairing pages (needs make web-install
## and Playwright's Chromium)
e2e-ui:
	cd e2e && E2E_PLAYWRIGHT=1 go test -tags e2e -count=1 -timeout 30m -v .
