# 1) UI: Svelte + Vite, built without package install scripts
FROM node:24.21.0-alpine@sha256:83f1c388c31fb2e51f7cbd4dea949b96260798c98f206e8e4696bc93bd964e3a AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm run build            # produces /web/dist with relative paths (base: './')

# 2) Gateway: static Go binary with the embedded UI
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
# Build against the mandate-spec version pinned in go.mod, never a workspace.
ENV GOWORK=off
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/webui/dist
# The licenses of the linked Go modules join those of the UI (decision B10).
RUN go run ./tools/golicenses -file internal/webui/dist/licenses.txt \
      -pending github.com/mandate-spec/mandate-spec=Apache-2.0
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/home-mandate ./cmd/home-mandate

# 3) Runtime: only the binary and CA certificates, no Node runtime
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/home-mandate /home-mandate
# Runs as root (docs/ARCHITECTURE.md decision 3): in app mode, Supervisor creates /data,
# /data/options.json and the private key in /ssl as root-only. The image holds nothing
# but the binary and CA certificates; there is no shell and nothing else to escalate to.
EXPOSE 8765 8099
ENTRYPOINT ["/home-mandate"]

# For real reproducibility, pin all base images by digest
# (node:<version>-alpine@sha256:…, golang:<version>-alpine@sha256:…),
# sign in CI (cosign) and generate an SBOM for Go and JavaScript dependencies.
