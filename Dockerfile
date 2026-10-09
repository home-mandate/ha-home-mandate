# Multi-arch: the UI and the Go build run on the build machine's platform; Go cross-compiles
# for the target (TARGETOS/TARGETARCH), so no stage runs under emulation.

# 1) UI: Svelte + Vite, built without package install scripts
FROM --platform=$BUILDPLATFORM node:24.21.0-alpine@sha256:83f1c388c31fb2e51f7cbd4dea949b96260798c98f206e8e4696bc93bd964e3a AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm run build            # produces /web/dist with relative paths (base: './')

# 2) Gateway: static Go binary with the embedded UI
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673 AS build
WORKDIR /src
# Build against the version of the specification pinned in go.mod, never a workspace.
ENV GOWORK=off
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/webui/dist
# The licenses of the linked Go modules join those of the UI (decision B10).
RUN go run ./tools/golicenses -file internal/webui/dist/licenses.txt \
      -pending github.com/home-mandate/spec=Apache-2.0
ARG VERSION=dev
ARG COMMIT=unknown
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/home-mandate ./cmd/home-mandate

# 3) Runtime: only the binary and CA certificates, no Node runtime
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/home-mandate /home-mandate
# No USER (docs/ARCHITECTURE.md decision 3): in app mode, Supervisor runs the image as is
# and creates /data, /data/options.json and the private key in /ssl as root-only, and an
# app cannot set a user. Container mode runs unprivileged with a user the operator sets
# (user: "65532:65532" in docs/deploy/compose.yaml). The image holds nothing but the
# binary and CA certificates; there is no shell and nothing else to escalate to.
EXPOSE 8765 8099
ENTRYPOINT ["/home-mandate"]

# Releases (.github/workflows/release.yml) build this for linux/amd64 and linux/arm64, sign
# the image with cosign and attach provenance and SBOMs for the Go and JavaScript dependencies.
