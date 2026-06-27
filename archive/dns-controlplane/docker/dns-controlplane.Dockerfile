FROM rust:1.94-bookworm AS builder

WORKDIR /build
COPY tools/arbuzas-rs/Cargo.toml tools/arbuzas-rs/Cargo.lock ./tools/arbuzas-rs/
COPY tools/arbuzas-rs/crates/arbuzas-dns/Cargo.toml ./tools/arbuzas-rs/crates/arbuzas-dns/Cargo.toml
COPY tools/arbuzas-rs/crates/arbuzas-dns-lib/Cargo.toml ./tools/arbuzas-rs/crates/arbuzas-dns-lib/Cargo.toml
# Seed a minimal workspace so Docker can cache dependency compilation separately
# from the real application sources.
RUN install -d \
      ./tools/arbuzas-rs/crates/arbuzas-dns/src \
      ./tools/arbuzas-rs/crates/arbuzas-dns-lib/src \
  && printf 'fn main() {}\n' > ./tools/arbuzas-rs/crates/arbuzas-dns/src/main.rs \
  && printf 'pub fn dependency_stub() {}\n' > ./tools/arbuzas-rs/crates/arbuzas-dns-lib/src/lib.rs
RUN cargo build --manifest-path tools/arbuzas-rs/Cargo.toml --release -p arbuzas-dns

COPY tools/arbuzas-rs/crates/arbuzas-dns ./tools/arbuzas-rs/crates/arbuzas-dns
COPY tools/arbuzas-rs/crates/arbuzas-dns-lib ./tools/arbuzas-rs/crates/arbuzas-dns-lib
RUN rm -f /build/tools/arbuzas-rs/target/release/arbuzas-dns \
      /build/tools/arbuzas-rs/target/release/deps/arbuzas_dns* \
      /build/tools/arbuzas-rs/target/release/deps/libarbuzas_dns_lib* \
  && find /build/tools/arbuzas-rs/target/release/.fingerprint -maxdepth 1 \
      \( -name 'arbuzas-dns-*' -o -name 'arbuzas-dns-lib-*' \) \
      -exec rm -rf {} +
RUN cargo build --manifest-path tools/arbuzas-rs/Cargo.toml --release -p arbuzas-dns

FROM debian:bookworm-slim

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl openssl tzdata \
  && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/tools/arbuzas-rs/target/release/arbuzas-dns /usr/local/bin/arbuzas-dns

RUN install -d -m 0755 /run/arbuzas/dns

CMD ["/usr/local/bin/arbuzas-dns", "serve"]
