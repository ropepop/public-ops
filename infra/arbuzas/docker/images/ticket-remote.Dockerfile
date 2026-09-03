# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS build

ARG TARGETOS
ARG TARGETARCH
ARG ARBUZAS_RELEASE_ID=unknown
ARG ARBUZAS_RELEASE_SOURCE_COMMIT=nogit
ARG ARBUZAS_RELEASE_SOURCE_DIRTY=unknown
ARG ARBUZAS_RELEASE_SOURCE_SHA256=unknown

WORKDIR /src/workloads/ticket-remote

COPY workloads/ticket-remote/go.mod workloads/ticket-remote/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/ticket-remote ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  set -eux; \
  export ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID}"; \
  export ARBUZAS_RELEASE_SOURCE_COMMIT="${ARBUZAS_RELEASE_SOURCE_COMMIT}"; \
  export ARBUZAS_RELEASE_SOURCE_DIRTY="${ARBUZAS_RELEASE_SOURCE_DIRTY}"; \
  export ARBUZAS_RELEASE_SOURCE_SHA256="${ARBUZAS_RELEASE_SOURCE_SHA256}"; \
  ldflags="$(bash ./scripts/ldflags.sh)"; \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags "$ldflags" -o /out/ticket-remote ./cmd/ticket-remote

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /srv/ticket-remote/state \
  && chown -R 1001:1001 /srv/ticket-remote

WORKDIR /srv/ticket-remote

COPY --from=build /out/ticket-remote /usr/local/bin/ticket-remote

USER 1001:1001

CMD ["/usr/local/bin/ticket-remote"]
