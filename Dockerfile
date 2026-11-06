# syntax=docker/dockerfile:1

# Multi-stage image. The build stage compiles a static binary (CGO_ENABLED=0).
# The final stage runs only that binary.
#
#   docker build -t mcp-static-deploy .
#   docker run --rm -p 8080:8080 -e BASE_DOMAIN=localhost -v mcp-static-data:/data mcp-static-deploy
#
# Runtime environment (override with docker run -e):
#   ADDR         listen address                         default :8080
#   BASE_URL     apex URL; http leaves cookies non-Secure  default http://localhost:8080
#   BASE_DOMAIN  required; sites are {slug}.{BASE_DOMAIN}. No default.
#                The process refuses to start if this is empty.
#                localhost in the example above is only a local-test host.
#   DATA_DIR     SQLite file, sites/{slug}/, setup-token   default /data
#                First boot writes setup-token (mode 0600) and logs the token once.
#                Later boots read that file. SETUP_TOKEN is not read from the environment.
#
# Wildcard DNS for *.BASE_DOMAIN is not configured by this image.

FROM node:22-bookworm AS ui

WORKDIR /src

COPY web/package.json web/package-lock.json web/
RUN npm ci --prefix web

COPY web web
RUN npm run build --prefix web

FROM golang:1.26.10-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=ui /src/internal/web/dist ./internal/web/dist

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-static-deploy ./cmd/server \
    && mkdir -p /out/data \
    && touch /out/data/.keep

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/mcp-static-deploy /mcp-static-deploy
COPY --from=build --chown=nonroot:nonroot /out/data /data

ENV ADDR=":8080" \
    BASE_URL="http://localhost:8080" \
    DATA_DIR="/data"

WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/mcp-static-deploy"]
