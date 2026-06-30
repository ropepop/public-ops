#!/usr/bin/env bash
set -euo pipefail

pkg="chatgptbroker/internal/version"
printf -- "-X '%s.ReleaseID=%s' -X '%s.SourceCommit=%s' -X '%s.SourceDirty=%s' -X '%s.SourceSHA256=%s'\n" \
  "$pkg" "${ARBUZAS_RELEASE_ID:-dev}" \
  "$pkg" "${ARBUZAS_RELEASE_SOURCE_COMMIT:-unknown}" \
  "$pkg" "${ARBUZAS_RELEASE_SOURCE_DIRTY:-unknown}" \
  "$pkg" "${ARBUZAS_RELEASE_SOURCE_SHA256:-unknown}"
