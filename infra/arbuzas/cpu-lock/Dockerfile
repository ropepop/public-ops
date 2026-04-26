FROM debian:trixie-slim

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends linux-cpupower \
    && rm -rf /var/lib/apt/lists/*

CMD ["bash"]
