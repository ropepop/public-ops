#!/usr/bin/env bash
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/migrate_satiksme_analyzer_secrets.py"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

env_file="${tmp_dir}/satiksme-bot.env"
secrets_dir="${tmp_dir}/secrets"
cat >"${env_file}" <<'EOF'
SATIKSME_CHAT_ANALYZER_ENABLED=false
SATIKSME_CHAT_ANALYZER_API_ID=12345678
SATIKSME_CHAT_ANALYZER_API_HASH=0123456789abcdef0123456789abcdef
SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY=synthetic-google-key
SATIKSME_CHAT_ANALYZER_MODEL_API_KEY=synthetic-model-key
KEEP_ME=true
EOF
chmod 0644 "${env_file}"

python3 "${SCRIPT}" \
  --env-file "${env_file}" \
  --secrets-dir "${secrets_dir}"
python3 "${SCRIPT}" \
  --env-file "${env_file}" \
  --secrets-dir "${secrets_dir}" \
  --check

for direct_key in \
  SATIKSME_CHAT_ANALYZER_API_ID \
  SATIKSME_CHAT_ANALYZER_API_HASH \
  SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY \
  SATIKSME_CHAT_ANALYZER_MODEL_API_KEY; do
  if grep -Eq "^${direct_key}=" "${env_file}"; then
    printf 'direct analyzer credential remained in env: %s\n' "${direct_key}" >&2
    exit 1
  fi
done
grep -Fx 'KEEP_ME=true' "${env_file}" >/dev/null

file_mode() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

[[ "$(file_mode "${env_file}")" == 600 ]]
for secret_file in \
  telegram-api-id.secret \
  telegram-api-hash.secret \
  google-api-key.secret \
  model-api-key.secret; do
  [[ -s "${secrets_dir}/${secret_file}" ]]
  [[ "$(file_mode "${secrets_dir}/${secret_file}")" == 600 ]]
done

# A second migration is an idempotent validation and must not rewrite values.
before_hashes="$(shasum -a 256 "${env_file}" "${secrets_dir}"/*.secret)"
python3 "${SCRIPT}" --env-file "${env_file}" --secrets-dir "${secrets_dir}"
after_hashes="$(shasum -a 256 "${env_file}" "${secrets_dir}"/*.secret)"
[[ "${before_hashes}" == "${after_hashes}" ]]

replacement_output="$(printf '%s' 'synthetic-replacement-key' | python3 "${SCRIPT}" \
  --secrets-dir "${secrets_dir}" --set-google-key-stdin)"
if [[ "${replacement_output}" == *synthetic-replacement-key* ]]; then
  printf 'stdin replacement command printed the submitted secret\n' >&2
  exit 1
fi
grep -Fx 'synthetic-replacement-key' "${secrets_dir}/google-api-key.secret" >/dev/null
grep -Fx 'synthetic-replacement-key' "${secrets_dir}/model-api-key.secret" >/dev/null
[[ "$(file_mode "${secrets_dir}/google-api-key.secret")" == 600 ]]
[[ "$(file_mode "${secrets_dir}/model-api-key.secret")" == 600 ]]

printf 'Satiksme analyzer secret migration tests passed\n'
