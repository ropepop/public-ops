# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM node:22.22.1-bookworm-slim@sha256:4f77a690f2f8946ab16fe1e791a3ac0667ae1c3575c3e4d0d4589e9ed5bfaf3d AS vuetorrent-builder

ADD --checksum=sha256:af29d17312bcf0c1d8b496f96ae74e511cbf3d31a25071d38e7eb5b61c7dcfb4 \
    https://github.com/VueTorrent/VueTorrent/archive/refs/tags/v2.34.1.tar.gz \
    /tmp/vuetorrent-source.tar.gz

RUN set -eux; \
    printf '%s  %s\n' \
        'af29d17312bcf0c1d8b496f96ae74e511cbf3d31a25071d38e7eb5b61c7dcfb4' \
        '/tmp/vuetorrent-source.tar.gz' \
        | sha256sum -c -; \
    mkdir -p /work/vuetorrent; \
    tar -xzf /tmp/vuetorrent-source.tar.gz --strip-components=1 -C /work/vuetorrent; \
    printf '%s  %s\n' \
        '1bd06f97b868cbea3d2d2bd7277e05163efd7ff326a357ca39e075e94ed4ee61' \
        '/work/vuetorrent/src/components/DnDZone.vue' \
        | sha256sum -c -

COPY infra/arbuzas/docker/images/vuetorrent-2.34.1-overlay/ \
    /tmp/vuetorrent-overlay/

RUN set -eux; \
    cp -a /tmp/vuetorrent-overlay/. /work/vuetorrent/

WORKDIR /work/vuetorrent

RUN set -eux; \
    npm ci; \
    npm test; \
    npm run build; \
    test "$(cat vuetorrent/version.txt)" = "2.34.1"; \
    test -f vuetorrent/public/index.html; \
    node -e 'const fs = require("node:fs"); const html = fs.readFileSync("vuetorrent/public/index.html", "utf8"); const worker = fs.readFileSync("vuetorrent/public/sw.js", "utf8"); const assets = Array.from(html.matchAll(/(?:src|href)="\.\/(assets\/[^\"]+\.(?:js|css))"/g), match => match[1]); if (assets.length < 2) throw new Error("built index has no hashed JavaScript and CSS assets"); for (const asset of assets) if (!worker.includes(`url:"${asset}"`)) throw new Error(`PWA worker is missing current asset: ${asset}`);'; \
    chmod -R a=rX vuetorrent

# qBittorrent 5.2.3, LinuxServer.io linux/amd64 image manifest.
FROM lscr.io/linuxserver/qbittorrent:5.2.3@sha256:1a4641fa759dee784708ed277ece10adbbc5810ebb8bb9fdfe1cf00031f5ab2b

LABEL org.opencontainers.image.title="Arbuzas qBittorrent" \
      org.opencontainers.image.description="Pinned qBittorrent with pinned VueTorrent mobile WebUI" \
      org.opencontainers.image.version="5.2.3-vuetorrent-2.34.1" \
      org.opencontainers.image.source="https://github.com/ropepop/ops" \
      io.arbuzas.qbittorrent.base-manifest="sha256:1a4641fa759dee784708ed277ece10adbbc5810ebb8bb9fdfe1cf00031f5ab2b" \
      io.arbuzas.vuetorrent.version="2.34.1" \
      io.arbuzas.vuetorrent.source-sha256="af29d17312bcf0c1d8b496f96ae74e511cbf3d31a25071d38e7eb5b61c7dcfb4" \
      io.arbuzas.vuetorrent.patch="ios-drag-materialize-v1"

COPY --from=vuetorrent-builder /work/vuetorrent/vuetorrent /vuetorrent

COPY --chmod=0755 infra/arbuzas/docker/images/qbittorrent-entrypoint.sh \
    /usr/local/bin/qbittorrent-entrypoint
COPY --chmod=0755 infra/arbuzas/docker/images/qbittorrent-memory-health.sh \
    /usr/local/bin/qbittorrent-memory-health

ENTRYPOINT ["/usr/local/bin/qbittorrent-entrypoint"]
