#!/usr/bin/env bash
set -euo pipefail

readonly LAB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly ASSET_DIR="${LAB_DIR}/assets"
readonly CAPABILITY_FIXTURE="${LAB_DIR}/../internal/web/static/hdr-capability-fixture.jpg"
readonly WIDTH=768
readonly HEIGHT=512

for tool in cc ultrahdr_app magick shasum perl; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "missing required tool: ${tool}" >&2
    exit 1
  fi
done

now_ms() {
  perl -MTime::HiRes=time -e 'printf "%.3f", time * 1000'
}

elapsed_ms() {
  awk -v start="$1" -v end="$2" 'BEGIN { printf "%.3f", end - start }'
}

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ticket-hdr-lab.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT
mkdir -p "${ASSET_DIR}"

compile_start="$(now_ms)"
cc -std=c11 -O2 -Wall -Wextra -Werror "${LAB_DIR}/fixture_generator.c" -lm \
  -o "${tmp_dir}/fixture_generator"
compile_end="$(now_ms)"

raster_start="$(now_ms)"
"${tmp_dir}/fixture_generator" "${tmp_dir}/sdr.rgba" "${tmp_dir}/hdr.rgba16f"
raster_end="$(now_ms)"

expected_sdr_size=$((WIDTH * HEIGHT * 4))
expected_hdr_size=$((WIDTH * HEIGHT * 8))
actual_sdr_size="$(wc -c < "${tmp_dir}/sdr.rgba" | tr -d ' ')"
actual_hdr_size="$(wc -c < "${tmp_dir}/hdr.rgba16f" | tr -d ' ')"
[[ "${actual_sdr_size}" == "${expected_sdr_size}" ]]
[[ "${actual_hdr_size}" == "${expected_hdr_size}" ]]

sdr_start="$(now_ms)"
magick -size "${WIDTH}x${HEIGHT}" -depth 8 "rgba:${tmp_dir}/sdr.rgba" \
  -quality 95 -strip "${ASSET_DIR}/synthetic-sdr-reference.jpg"
sdr_end="$(now_ms)"

encode_start="$(now_ms)"
ultrahdr_app -m 0 \
  -p "${tmp_dir}/hdr.rgba16f" -y "${tmp_dir}/sdr.rgba" \
  -w "${WIDTH}" -h "${HEIGHT}" -a 4 -b 3 -C 0 -c 0 -t 0 \
  -q 95 -Q 95 -s 2 -M 1 -D 1 -k 1 -K 4 \
  -z "${ASSET_DIR}/synthetic-ultrahdr-4x.jpg"
encode_end="$(now_ms)"

# The authenticated page decodes the same sanitized fixture before exposing
# the HDR toggle. This generated copy contains no Ticket or user pixels.
install -m 0644 "${ASSET_DIR}/synthetic-ultrahdr-4x.jpg" "${CAPABILITY_FIXTURE}"

probe_start="$(now_ms)"
ultrahdr_app -m 1 -P -j "${ASSET_DIR}/synthetic-ultrahdr-4x.jpg" \
  > "${tmp_dir}/probe.txt" 2>&1
probe_end="$(now_ms)"

readonly encoder_version="$(ultrahdr_app 2>&1 | sed -n 's/.*lib version: \([^ ]*\).*/\1/p' | head -1)"
readonly fixture_sha="$(shasum -a 256 "${ASSET_DIR}/synthetic-ultrahdr-4x.jpg" | awk '{print $1}')"
readonly ordinary_sha="$(shasum -a 256 "${ASSET_DIR}/synthetic-sdr-reference.jpg" | awk '{print $1}')"

{
  echo "fixture=synthetic-ultrahdr-4x.jpg"
  echo "privacy=synthetic-only-no-ticket-or-user-pixels"
  echo "dimensions=${WIDTH}x${HEIGHT}"
  echo "sdr_intent=8-bit-rgba-srgb-bt709"
  echo "hdr_intent=16-bit-half-float-linear-bt709"
  echo "linear_content_boost=4.0"
  echo "gain_map_downsample=2"
  echo "gain_map_expected_dimensions=$((WIDTH / 2))x$((HEIGHT / 2))"
  echo "encoder=ultrahdr_app-${encoder_version}"
  echo "ultrahdr_sha256=${fixture_sha}"
  echo "ordinary_sdr_sha256=${ordinary_sha}"
  echo "compile_ms=$(elapsed_ms "${compile_start}" "${compile_end}")"
  echo "raster_generation_ms=$(elapsed_ms "${raster_start}" "${raster_end}")"
  echo "sdr_jpeg_encode_ms=$(elapsed_ms "${sdr_start}" "${sdr_end}")"
  echo "ultrahdr_encode_ms=$(elapsed_ms "${encode_start}" "${encode_end}")"
  echo "ultrahdr_probe_ms=$(elapsed_ms "${probe_start}" "${probe_end}")"
} > "${ASSET_DIR}/fixture-manifest.txt"

"${LAB_DIR}/validate_fixture.sh"
cat "${ASSET_DIR}/fixture-manifest.txt"
