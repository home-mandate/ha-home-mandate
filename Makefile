# SPDX-License-Identifier: AGPL-3.0-or-later

# Checks run against the pinned mandate-spec version from go.mod, as in CI.
# For local development against ../mandate-spec: make test GOWORK=$(CURDIR)/go.work
# (go.work is not checked in). This also keeps a go.work in a parent directory out.
export GOWORK ?= off

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

# Interim total threshold (docs/TESTING.md section 2: other Go packages ≥ 85 %).
# Per-package thresholds follow with tools/covercheck.
COVER_MIN := 85

VERSION ?= dev
GOARCHES := amd64 arm64

.PHONY: check test cover vet staticcheck vulncheck build

## check: everything that must be green before a commit
check: vet staticcheck cover vulncheck

test:
	go test -race ./...

cover:
	go test -race -coverprofile=cover.out ./...
	@go tool cover -func=cover.out | awk -v min=$(COVER_MIN) '/^total:/ { sub("%", "", $$3); \
		if ($$3 + 0 < min) { print "Coverage " $$3 "% < " min "%"; exit 1 } else print "Coverage " $$3 "%" }'

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

## build: static binaries for all release architectures in bin/
build:
	@for arch in $(GOARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false \
			-ldflags="-s -w -buildid= -X main.version=$(VERSION)" \
			-o bin/home-mandate-linux-$$arch ./cmd/home-mandate || exit 1; \
	done
