# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG ARBUZAS_RELEASE_ID=unknown
ARG ARBUZAS_RELEASE_SOURCE_COMMIT=nogit
ARG ARBUZAS_RELEASE_SOURCE_DIRTY=unknown
ARG ARBUZAS_RELEASE_SOURCE_SHA256=unknown

WORKDIR /src/workloads/satiksme-bot

COPY workloads/satiksme-bot/go.mod workloads/satiksme-bot/go.sum ./
COPY workloads/shared-go /src/workloads/shared-go
RUN --mount=type=cache,target=/go/pkg/mod \
  go mod download

COPY workloads/satiksme-bot ./

RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  set -eux; \
  export ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID}"; \
  export ARBUZAS_RELEASE_SOURCE_COMMIT="${ARBUZAS_RELEASE_SOURCE_COMMIT}"; \
  export ARBUZAS_RELEASE_SOURCE_DIRTY="${ARBUZAS_RELEASE_SOURCE_DIRTY}"; \
  export ARBUZAS_RELEASE_SOURCE_SHA256="${ARBUZAS_RELEASE_SOURCE_SHA256}"; \
  ldflags="$(bash ./scripts/ldflags.sh)"; \
  export CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}"; \
  go build -ldflags "$ldflags" -o /out/satiksme-bot ./cmd/bot; \
  go build -ldflags "$ldflags" -o /out/satiksme-chat-analyzer-session ./cmd/chat-analyzer-session; \
  go build -ldflags "$ldflags" -o /out/satiksme-chat-analyzer-dry-run ./cmd/chat-analyzer-dry-run; \
  go build -ldflags "$ldflags" -o /out/satiksme-chat-analyzer-batch-once ./cmd/chat-analyzer-batch-once; \
  go build -ldflags "$ldflags" -o /out/rs-acquisition-campaign ./cmd/rs-acquisition-campaign

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/satiksme-bot

COPY --from=build /out/satiksme-bot /usr/local/bin/satiksme-bot
COPY --from=build /out/satiksme-chat-analyzer-session /usr/local/bin/satiksme-chat-analyzer-session
COPY --from=build /out/satiksme-chat-analyzer-dry-run /usr/local/bin/satiksme-chat-analyzer-dry-run
COPY --from=build /out/satiksme-chat-analyzer-batch-once /usr/local/bin/satiksme-chat-analyzer-batch-once
COPY --from=build /out/rs-acquisition-campaign /usr/local/bin/rs-acquisition-campaign

CMD ["/usr/local/bin/satiksme-bot"]
