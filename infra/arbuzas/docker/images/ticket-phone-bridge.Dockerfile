# syntax=docker/dockerfile:1.7

FROM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends android-tools-adb ca-certificates curl socat \
  && groupadd --gid 1002 ticketbridge \
  && useradd --uid 1002 --gid ticketbridge --create-home --home-dir /home/ticketbridge --shell /usr/sbin/nologin ticketbridge \
  && install -d -o 1002 -g 1002 -m 0700 /home/ticketbridge/.android \
  && rm -rf /var/lib/apt/lists/*

COPY infra/arbuzas/docker/images/ticket-phone-bridge-loop.sh /usr/local/bin/ticket-phone-bridge-loop
COPY infra/arbuzas/docker/images/ticket-phone-bridge-health.sh /usr/local/bin/ticket-phone-bridge-health
RUN chmod +x /usr/local/bin/ticket-phone-bridge-loop /usr/local/bin/ticket-phone-bridge-health

ENV HOME=/home/ticketbridge

USER 1002:1002

CMD ["/usr/local/bin/ticket-phone-bridge-loop"]
