# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

# qBittorrent 5.2.3, LinuxServer.io linux/amd64 image manifest.
FROM lscr.io/linuxserver/qbittorrent:5.2.3@sha256:1a4641fa759dee784708ed277ece10adbbc5810ebb8bb9fdfe1cf00031f5ab2b

LABEL org.opencontainers.image.title="Arbuzas qBittorrent" \
      org.opencontainers.image.description="Pinned qBittorrent with pinned VueTorrent mobile WebUI" \
      org.opencontainers.image.version="5.2.3-vuetorrent-2.34.1" \
      org.opencontainers.image.source="https://github.com/ropepop/ops" \
      io.arbuzas.qbittorrent.base-manifest="sha256:1a4641fa759dee784708ed277ece10adbbc5810ebb8bb9fdfe1cf00031f5ab2b" \
      io.arbuzas.vuetorrent.version="2.34.1" \
      io.arbuzas.vuetorrent.sha256="6cf0f2c6533835602b1d18cd26e83926d53c9330e0e898e971af6850233d20eb"

ADD --checksum=sha256:6cf0f2c6533835602b1d18cd26e83926d53c9330e0e898e971af6850233d20eb \
    https://github.com/VueTorrent/VueTorrent/releases/download/v2.34.1/vuetorrent.zip \
    /tmp/vuetorrent.zip

RUN set -eux; \
    mkdir -p /tmp/vuetorrent-release /vuetorrent; \
    unzip -q /tmp/vuetorrent.zip -d /tmp/vuetorrent-release; \
    test "$(cat /tmp/vuetorrent-release/vuetorrent/version.txt)" = "2.34.1"; \
    test -f /tmp/vuetorrent-release/vuetorrent/public/index.html; \
    cp -a /tmp/vuetorrent-release/vuetorrent/. /vuetorrent/; \
    chmod -R a=rX /vuetorrent; \
    rm -rf /tmp/vuetorrent.zip /tmp/vuetorrent-release

COPY --chmod=0755 infra/arbuzas/docker/images/qbittorrent-entrypoint.sh \
    /usr/local/bin/qbittorrent-entrypoint
COPY --chmod=0755 infra/arbuzas/docker/images/qbittorrent-memory-health.sh \
    /usr/local/bin/qbittorrent-memory-health

ENTRYPOINT ["/usr/local/bin/qbittorrent-entrypoint"]
