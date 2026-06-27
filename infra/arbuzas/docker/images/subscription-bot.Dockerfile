# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG ARBUZAS_RELEASE_ID=unknown
ARG ARBUZAS_RELEASE_SOURCE_COMMIT=nogit
ARG ARBUZAS_RELEASE_SOURCE_DIRTY=unknown
ARG ARBUZAS_RELEASE_SOURCE_SHA256=unknown

WORKDIR /src/workloads/subscription-bot

COPY workloads/subscription-bot/go.mod workloads/subscription-bot/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/subscription-bot ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  set -eux; \
  export ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID}"; \
  export ARBUZAS_RELEASE_SOURCE_COMMIT="${ARBUZAS_RELEASE_SOURCE_COMMIT}"; \
  export ARBUZAS_RELEASE_SOURCE_DIRTY="${ARBUZAS_RELEASE_SOURCE_DIRTY}"; \
  export ARBUZAS_RELEASE_SOURCE_SHA256="${ARBUZAS_RELEASE_SOURCE_SHA256}"; \
  ldflags="$(bash ./scripts/ldflags.sh)"; \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags "$ldflags" -o /out/subscription-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/subscription-bot

COPY --from=build /out/subscription-bot /usr/local/bin/subscription-bot

CMD ["/usr/local/bin/subscription-bot"]
