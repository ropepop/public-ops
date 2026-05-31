# syntax=docker/dockerfile:1.7

FROM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends android-tools-adb ca-certificates curl socat \
  && mkdir -p /root/.android \
  && rm -rf /var/lib/apt/lists/*

COPY infra/arbuzas/docker/images/ticket-phone-bridge-loop.sh /usr/local/bin/ticket-phone-bridge-loop
COPY infra/arbuzas/docker/images/ticket-phone-bridge-health.sh /usr/local/bin/ticket-phone-bridge-health
RUN chmod +x /usr/local/bin/ticket-phone-bridge-loop /usr/local/bin/ticket-phone-bridge-health

CMD ["/usr/local/bin/ticket-phone-bridge-loop"]
