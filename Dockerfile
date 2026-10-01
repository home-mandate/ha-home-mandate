# 1) Oberfläche: Svelte + Vite, Build ohne Installations-Skripte von Paketen
FROM node:22-alpine AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts
COPY web/ ./
RUN pnpm run build            # erzeugt /web/dist, relative Pfade (base: './')

# 2) Gateway: statisches Go-Binary mit eingebetteter Oberfläche
FROM golang:1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/webui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
      -o /out/home-mandate ./cmd/home-mandate

# 3) Laufzeit: nur Binary und CA-Zertifikate, keine Node-Laufzeit
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/home-mandate /home-mandate
# Nicht-Root, sofern /data im App-Modus beschreibbar ist (offene Entscheidung 3).
USER 65532:65532
EXPOSE 8765 8099
ENTRYPOINT ["/home-mandate"]

# Für echte Reproduzierbarkeit alle Basis-Images per Digest pinnen
# (node:<version>-alpine@sha256:…, golang:<version>-alpine@sha256:…),
# in CI signieren (cosign) und eine SBOM für Go- und JavaScript-Abhängigkeiten erzeugen.
