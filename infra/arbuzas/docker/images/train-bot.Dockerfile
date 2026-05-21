# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG ARBUZAS_RELEASE_ID=unknown
ARG ARBUZAS_RELEASE_SOURCE_COMMIT=nogit
ARG ARBUZAS_RELEASE_SOURCE_DIRTY=unknown
ARG ARBUZAS_RELEASE_SOURCE_SHA256=unknown

WORKDIR /src/workloads/train-bot

COPY workloads/train-bot/go.mod workloads/train-bot/go.sum ./
COPY workloads/shared-go /src/workloads/shared-go
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/train-bot ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  set -eux; \
  export ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID}"; \
  export ARBUZAS_RELEASE_SOURCE_COMMIT="${ARBUZAS_RELEASE_SOURCE_COMMIT}"; \
  export ARBUZAS_RELEASE_SOURCE_DIRTY="${ARBUZAS_RELEASE_SOURCE_DIRTY}"; \
  export ARBUZAS_RELEASE_SOURCE_SHA256="${ARBUZAS_RELEASE_SOURCE_SHA256}"; \
  ldflags="$(bash ./scripts/ldflags.sh)"; \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags "$ldflags" -o /out/train-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/train-bot

COPY --from=build /out/train-bot /usr/local/bin/train-bot

CMD ["/usr/local/bin/train-bot"]
