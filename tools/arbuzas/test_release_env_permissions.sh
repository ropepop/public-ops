#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SCRIPT="${SCRIPT_DIR}/deploy.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/arbuzas-release-env-permissions.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

extract_function() {
  local function_name="$1"
  local next_function_name="$2"
  awk -v start="${function_name}() {" -v stop="${next_function_name}() {" '
    $0 == start { copying = 1 }
    $0 == stop { exit }
    copying { print }
  ' "${DEPLOY_SCRIPT}"
}

file_mode() {
  local path="$1"
  if stat -f '%Lp' "${path}" >/dev/null 2>&1; then
    stat -f '%Lp' "${path}"
  else
    stat -c '%a' "${path}"
  fi
}

file_uid() {
  local path="$1"
  if stat -f '%u' "${path}" >/dev/null 2>&1; then
    stat -f '%u' "${path}"
  else
    stat -c '%u' "${path}"
  fi
}

release_metadata_function="$(extract_function prepare_local_release_metadata prepare_local_release_bundle)"
if [[ -z "${release_metadata_function}" ]]; then
  printf 'could not extract prepare_local_release_metadata\n' >&2
  exit 1
fi
eval "${release_metadata_function}"

copy_tree_into_release() { :; }
compute_release_source_commit() { printf 'permission-test-commit'; }
compute_release_source_dirty() { printf 'clean'; }
compute_release_source_sha256() { printf '%064d' 0; }
validate_release_identity_values() { :; }

while IFS= read -r variable_name; do
  printf -v "${variable_name}" '%s' 'permission-test'
done < <(
  printf '%s\n' "${release_metadata_function}" \
    | grep -oE '\$\{[A-Z][A-Z0-9_]*' \
    | tr -d '${' \
    | sort -u
)
ARBUZAS_RELEASE_DIR="${tmp_dir}/local-release"
ARBUZAS_RELEASE_ID="permission-test-release"
mkdir -p "${ARBUZAS_RELEASE_DIR}"
printf 'old=insecure\n' > "${ARBUZAS_RELEASE_DIR}/release.env"
chmod 0644 "${ARBUZAS_RELEASE_DIR}/release.env"
prepare_local_release_metadata
if [[ "$(file_mode "${ARBUZAS_RELEASE_DIR}/release.env")" != "600" ]]; then
  printf 'local release.env was not created with mode 0600\n' >&2
  exit 1
fi

release_env_hardening_function="$(extract_function harden_remote_release_env_permissions copy_release_to_remote)"
if [[ -z "${release_env_hardening_function}" ]]; then
  printf 'could not extract harden_remote_release_env_permissions\n' >&2
  exit 1
fi
eval "${release_env_hardening_function}"

fake_bin="${tmp_dir}/bin"
REMOTE_RELEASES_ROOT="${tmp_dir}/remote-releases"
mkdir -p "${fake_bin}" "${REMOTE_RELEASES_ROOT}/release-a" "${REMOTE_RELEASES_ROOT}/release-b"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'if [[ "${1:-}" == "-n" ]]; then shift; fi' \
  'exec "$@"' > "${fake_bin}/sudo"
chmod 0755 "${fake_bin}/sudo"
printf 'secret=a\n' > "${REMOTE_RELEASES_ROOT}/release-a/release.env"
printf 'secret=b\n' > "${REMOTE_RELEASES_ROOT}/release-b/release.env"
printf 'ordinary\n' > "${REMOTE_RELEASES_ROOT}/release-a/README.txt"
chmod 0644 \
  "${REMOTE_RELEASES_ROOT}/release-a/release.env" \
  "${REMOTE_RELEASES_ROOT}/release-b/release.env" \
  "${REMOTE_RELEASES_ROOT}/release-a/README.txt"
remote_shell() {
  PATH="${fake_bin}:${PATH}" bash -c "$1"
}
harden_remote_release_env_permissions

for remote_env in \
  "${REMOTE_RELEASES_ROOT}/release-a/release.env" \
  "${REMOTE_RELEASES_ROOT}/release-b/release.env"; do
  if [[ "$(file_mode "${remote_env}")" != "600" ]]; then
    printf 'existing remote release.env was not hardened to mode 0600: %s\n' "${remote_env}" >&2
    exit 1
  fi
  if [[ "$(file_uid "${remote_env}")" != "$(id -u)" ]]; then
    printf 'existing remote release.env was not assigned to the deploy account: %s\n' "${remote_env}" >&2
    exit 1
  fi
  if [[ ! -r "${remote_env}" ]]; then
    printf 'deploy account cannot read hardened remote release.env: %s\n' "${remote_env}" >&2
    exit 1
  fi
done
if [[ "$(file_mode "${REMOTE_RELEASES_ROOT}/release-a/README.txt")" != "644" ]]; then
  printf 'remote permission hardening changed an unrelated file\n' >&2
  exit 1
fi

upload_function="$(extract_function upload_remote_file usage)"
if [[ -z "${upload_function}" ]]; then
  printf 'could not extract upload_remote_file\n' >&2
  exit 1
fi
for required_upload_guard in \
  'umask 077' \
  'mktemp -d' \
  'chmod 0700' \
  'chmod 0600' \
  'refusing unsafe remote upload target'; do
  if [[ "${upload_function}" != *"${required_upload_guard}"* ]]; then
    printf 'remote upload hardening is missing: %s\n' "${required_upload_guard}" >&2
    exit 1
  fi
done
if [[ "${upload_function}" == *'.uploading.$$'* ]]; then
  printf 'remote upload still uses a predictable process-id staging path\n' >&2
  exit 1
fi
eval "${upload_function}"

shell_quote() {
  printf "'%s'" "$1"
}
remote_target() {
  printf 'permission-test-host'
}
run_ssh() {
  local arguments=("$@")
  local command_index=$((${#arguments[@]} - 1))
  bash -c "${arguments[${command_index}]}"
}

upload_source="${tmp_dir}/upload-source"
upload_dir="${tmp_dir}/upload-target"
upload_target="${upload_dir}/release.tar"
mkdir -p "${upload_dir}"
printf 'private release payload\n' > "${upload_source}"
upload_remote_file "${upload_source}" "${upload_target}"
cmp -s "${upload_source}" "${upload_target}"
if [[ "$(file_mode "${upload_target}")" != "600" ]]; then
  printf 'uploaded release payload was not mode 0600\n' >&2
  exit 1
fi
if find "${upload_dir}" -mindepth 1 -maxdepth 1 -type d -name '.arbuzas-upload.*' -print -quit | grep -q .; then
  printf 'remote upload left a private staging directory behind\n' >&2
  exit 1
fi

upload_victim="${tmp_dir}/upload-victim"
upload_symlink="${upload_dir}/unsafe-symlink"
printf 'must remain unchanged\n' > "${upload_victim}"
ln -s "${upload_victim}" "${upload_symlink}"
if upload_remote_file "${upload_source}" "${upload_symlink}" >/dev/null 2>&1; then
  printf 'remote upload accepted a symlink destination\n' >&2
  exit 1
fi
grep -Fx 'must remain unchanged' "${upload_victim}" >/dev/null
if [[ ! -L "${upload_symlink}" ]]; then
  printf 'remote upload changed an unsafe symlink destination\n' >&2
  exit 1
fi

upload_directory_target="${upload_dir}/unsafe-directory"
mkdir "${upload_directory_target}"
if upload_remote_file "${upload_source}" "${upload_directory_target}" >/dev/null 2>&1; then
  printf 'remote upload accepted a non-regular destination\n' >&2
  exit 1
fi

printf 'release.env permission tests passed\n'
