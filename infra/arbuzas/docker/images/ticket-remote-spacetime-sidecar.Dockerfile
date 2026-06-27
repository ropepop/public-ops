# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM rust:1.94-bookworm AS build

ARG TARGETARCH
ARG ARBUZAS_RELEASE_ID=unknown
ARG ARBUZAS_RELEASE_SOURCE_COMMIT=nogit
ARG ARBUZAS_RELEASE_SOURCE_DIRTY=unknown
ARG ARBUZAS_RELEASE_SOURCE_SHA256=unknown

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates libssl-dev pkg-config \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src/workloads/ticket-remote/spacetime-sidecar

COPY workloads/ticket-remote/spacetime-sidecar/Cargo.toml workloads/ticket-remote/spacetime-sidecar/Cargo.lock ./
COPY workloads/ticket-remote/spacetime-sidecar/src ./src

RUN --mount=type=cache,target=/usr/local/cargo/registry \
  --mount=type=cache,target=/usr/local/cargo/git \
  --mount=type=cache,target=/src/workloads/ticket-remote/spacetime-sidecar/target \
  set -eux; \
  cargo build --release; \
  bin="/src/workloads/ticket-remote/spacetime-sidecar/target/release/ticket-remote-spacetime-sidecar"; \
  cp "$bin" /tmp/ticket-remote-spacetime-sidecar

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl libssl3 \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/ticket-remote-spacetime-sidecar

COPY --from=build /tmp/ticket-remote-spacetime-sidecar /usr/local/bin/ticket-remote-spacetime-sidecar

CMD ["/usr/local/bin/ticket-remote-spacetime-sidecar"]
