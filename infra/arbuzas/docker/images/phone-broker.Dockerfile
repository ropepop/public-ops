# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/workloads/phone-broker

COPY workloads/phone-broker/go.mod workloads/phone-broker/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/phone-broker ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -o /out/phone-broker ./cmd/phone-broker

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/phone-broker

COPY --from=build /out/phone-broker /usr/local/bin/phone-broker

CMD ["/usr/local/bin/phone-broker"]
