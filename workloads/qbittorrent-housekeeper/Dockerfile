FROM golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/qbittorrent-housekeeper ./cmd/qbittorrent-housekeeper

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/qbittorrent-housekeeper /usr/local/bin/qbittorrent-housekeeper
USER 1000:1000
EXPOSE 9091
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
  CMD wget -q -T 2 -O /dev/null http://127.0.0.1:9091/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/qbittorrent-housekeeper"]
