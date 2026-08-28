#!/usr/bin/env bash
set -euo pipefail

readonly LAB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly HDR_JPEG="${1:-${LAB_DIR}/assets/synthetic-ultrahdr-4x.jpg}"
readonly ORDINARY_JPEG="${2:-${LAB_DIR}/assets/synthetic-sdr-reference.jpg}"
readonly WIDTH=768
readonly HEIGHT=512

for tool in cc pkg-config ultrahdr_app magick; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
done

for file in "${HDR_JPEG}" "${ORDINARY_JPEG}"; do
  if [[ ! -s "${file}" ]]; then
    echo "missing or empty fixture: ${file}" >&2
    exit 1
  fi
done

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ticket-hdr-validation.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

cc -std=c11 -O2 -Wall -Wextra -Werror "${LAB_DIR}/fixture_probe.c" \
  -o "${tmp_dir}/fixture_probe" $(pkg-config --cflags --libs libuhdr)
"${tmp_dir}/fixture_probe" "${HDR_JPEG}" "${ORDINARY_JPEG}"

if ! ultrahdr_app -m 1 -P -j "${HDR_JPEG}" > "${tmp_dir}/hdr-probe.txt" 2>&1; then
  cat "${tmp_dir}/hdr-probe.txt" >&2
  echo "Ultra HDR probe rejected the HDR fixture" >&2
  exit 1
fi

if ultrahdr_app -m 1 -P -j "${ORDINARY_JPEG}" > "${tmp_dir}/ordinary-probe.txt" 2>&1; then
  cat "${tmp_dir}/ordinary-probe.txt" >&2
  echo "ordinary JPEG was incorrectly accepted as Ultra HDR" >&2
  exit 1
fi

if ! grep -Eqi 'gain.?map' "${tmp_dir}/hdr-probe.txt"; then
  cat "${tmp_dir}/hdr-probe.txt" >&2
  echo "probe did not report gain-map metadata" >&2
  exit 1
fi

dimensions="$(magick identify -format '%wx%h' "${HDR_JPEG}")"
if [[ "${dimensions}" != "${WIDTH}x${HEIGHT}" ]]; then
  echo "unexpected base image dimensions: ${dimensions}" >&2
  exit 1
fi

ultrahdr_app -m 1 -j "${HDR_JPEG}" -o 0 -O 4 -z "${tmp_dir}/decoded-linear-rgba16f.raw" \
  > "${tmp_dir}/decode.txt" 2>&1
decoded_size="$(wc -c < "${tmp_dir}/decoded-linear-rgba16f.raw" | tr -d ' ')"
expected_size=$((WIDTH * HEIGHT * 8))
if [[ "${decoded_size}" != "${expected_size}" ]]; then
  cat "${tmp_dir}/decode.txt" >&2
  echo "unexpected decoded HDR byte size: ${decoded_size}, expected ${expected_size}" >&2
  exit 1
fi

echo "PASS: Ultra HDR structure and gain-map metadata accepted by libultrahdr"
echo "PASS: ordinary JPEG rejected by the same gain-map probe"
echo "PASS: base dimensions ${dimensions}; linear HDR decode ${decoded_size} bytes"
sed -n '/gain/Ip;/boost/Ip;/offset/Ip;/gamma/Ip' "${tmp_dir}/hdr-probe.txt"
