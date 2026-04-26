FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/workloads/subscription-bot

COPY workloads/subscription-bot/go.mod workloads/subscription-bot/go.sum ./
RUN go mod download

COPY workloads/subscription-bot ./

RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
  go build -ldflags "$(bash ./scripts/ldflags.sh)" -o /out/subscription-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/subscription-bot

COPY --from=build /out/subscription-bot /usr/local/bin/subscription-bot

CMD ["/usr/local/bin/subscription-bot"]
