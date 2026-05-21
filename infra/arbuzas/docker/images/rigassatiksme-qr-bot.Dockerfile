# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/workloads/rigassatiksme-qr-bot

COPY workloads/rigassatiksme-qr-bot/go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/rigassatiksme-qr-bot ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -o /out/rigassatiksme-qr-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/rigassatiksme-qr-bot

COPY --from=build /out/rigassatiksme-qr-bot /usr/local/bin/rigassatiksme-qr-bot

CMD ["/usr/local/bin/rigassatiksme-qr-bot"]
