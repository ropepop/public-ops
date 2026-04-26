FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/workloads/train-bot

COPY workloads/train-bot/go.mod workloads/train-bot/go.sum ./
COPY workloads/shared-go /src/workloads/shared-go
RUN go mod download

COPY workloads/train-bot ./

RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
  go build -ldflags "$(bash ./scripts/ldflags.sh)" -o /out/train-bot ./cmd/bot

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /srv/train-bot

COPY --from=build /out/train-bot /usr/local/bin/train-bot

CMD ["/usr/local/bin/train-bot"]
