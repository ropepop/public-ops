#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd "${REPO_ROOT}"

if rg -n \
  --glob '!tools/arbuzas-rs/crates/arbuzas-dns-lib/src/state.rs' \
  'pixel[A-Z_a-z]|__pixelStack|pixelstack:|pixel_identity|pixel_doh|lv\.jolkins\.pixel|/pixel-stack/' \
  tools/arbuzas-rs/crates/arbuzas-dns/src \
  tools/arbuzas-rs/crates/arbuzas-dns-lib/src/config.rs \
  infra/adguardhome/module.yaml \
  infra/arbuzas/docker/images/dns-controlplane.Dockerfile
then
  echo "FAIL: active Arbuzas DNS runtime still carries live pixel naming" >&2
  exit 1
fi

echo "PASS: active Arbuzas DNS runtime no longer carries live pixel naming"
