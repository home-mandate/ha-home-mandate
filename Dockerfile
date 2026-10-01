# 1) UI: Svelte + Vite, built without package install scripts
FROM node:24-alpine AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm run build            # produces /web/dist with relative paths (base: './')

# 2) Gateway: static Go binary with the embedded UI
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/webui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
      -o /out/home-mandate ./cmd/home-mandate

# 3) Runtime: only the binary and CA certificates, no Node runtime
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/home-mandate /home-mandate
# Non-root, provided /data is writable in app mode (decision 3, checked in week 4).
USER 65532:65532
EXPOSE 8765 8099
ENTRYPOINT ["/home-mandate"]

# For real reproducibility, pin all base images by digest
# (node:<version>-alpine@sha256:…, golang:<version>-alpine@sha256:…),
# sign in CI (cosign) and generate an SBOM for Go and JavaScript dependencies.
