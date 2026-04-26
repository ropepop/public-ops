FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/workloads/satiksme-bot

COPY workloads/satiksme-bot/go.mod workloads/satiksme-bot/go.sum ./
COPY workloads/shared-go /src/workloads/shared-go
RUN go mod download

COPY workloads/satiksme-bot ./

RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
  go build -ldflags "$(bash ./scripts/ldflags.sh)" -o /out/satiksme-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/satiksme-bot

COPY --from=build /out/satiksme-bot /usr/local/bin/satiksme-bot

CMD ["/usr/local/bin/satiksme-bot"]
