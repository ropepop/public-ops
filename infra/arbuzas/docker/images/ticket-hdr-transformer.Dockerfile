# syntax=docker/dockerfile:1.7

FROM debian:bookworm-slim AS ultrahdr-build

ARG LIBULTRAHDR_COMMIT=e5f5a022fe96fc4dc2ee35c19f733a50df807abe
ARG LIBULTRAHDR_SHA256=5a7b6347a4a32c6936b81392cd6394250649380202f86c8214042aa645cc385c

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates cmake curl g++ libjpeg62-turbo-dev make ninja-build pkg-config \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN curl -fsSL "https://github.com/google/libultrahdr/archive/${LIBULTRAHDR_COMMIT}.tar.gz" -o libultrahdr.tar.gz \
  && echo "${LIBULTRAHDR_SHA256}  libultrahdr.tar.gz" | sha256sum -c - \
  && mkdir libultrahdr \
  && tar -xzf libultrahdr.tar.gz -C libultrahdr --strip-components=1 \
  && cmake -S libultrahdr -B build -G Ninja \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_INSTALL_PREFIX=/opt/libultrahdr \
    -DUHDR_BUILD_EXAMPLES=ON \
    -DUHDR_BUILD_TESTS=OFF \
    -DUHDR_ENABLE_HEIF=OFF \
    -DUHDR_ENABLE_INSTALL=ON \
    -DUHDR_WRITE_ISO=ON \
    -DUHDR_WRITE_XMP=OFF \
    -DUHDR_MAX_DIMENSION=1800 \
  && cmake --build build --parallel 2 \
  && cmake --install build

FROM --platform=$BUILDPLATFORM golang:1.26.5-bookworm AS go-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/workloads/ticket-remote
COPY workloads/ticket-remote/go.mod workloads/ticket-remote/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY workloads/ticket-remote ./
RUN --mount=type=cache,target=/go/pkg/mod \
  --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
  go build -trimpath -o /out/ticket-hdr-transformer ./cmd/ticket-hdr-transformer

FROM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates curl ffmpeg libjpeg62-turbo \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /tmp/ticket-hdr-transformer \
  && chown -R 1001:1001 /tmp/ticket-hdr-transformer

COPY --from=ultrahdr-build /opt/libultrahdr/lib/ /usr/local/lib/
COPY --from=ultrahdr-build /opt/libultrahdr/bin/ultrahdr_app /usr/local/bin/ultrahdr_app
COPY --from=go-build /out/ticket-hdr-transformer /usr/local/bin/ticket-hdr-transformer

ENV LD_LIBRARY_PATH=/usr/local/lib
USER 1001:1001
ENTRYPOINT ["/usr/local/bin/ticket-hdr-transformer"]
