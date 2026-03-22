#!/usr/bin/env bash

agent_browser_require_cli() {
  if [[ -n "${AGENT_BROWSER_CLI:-}" ]]; then
    if [[ ! -x "${AGENT_BROWSER_CLI}" ]]; then
      log "agent-browser CLI is not executable: ${AGENT_BROWSER_CLI}"
      exit 1
    fi
    return
  fi

  if ! command -v agent-browser >/dev/null 2>&1; then
    log "Missing required command: agent-browser"
    exit 1
  fi

  AGENT_BROWSER_CLI="$(command -v agent-browser)"
  export AGENT_BROWSER_CLI
}

agent_browser_run() {
  local session="$1"
  shift

  agent_browser_require_cli

  local output=""
  if output="$("${AGENT_BROWSER_CLI}" --session "${session}" "$@" 2>&1)"; then
    AGENT_BROWSER_LAST_OUTPUT="${output}"
    printf '%s\n' "${output}"
    return 0
  fi

  local rc=$?
  AGENT_BROWSER_LAST_OUTPUT="${output}"
  printf '%s\n' "${output}"
  return "${rc}"
}

agent_browser_run_with_profile() {
  local session="$1"
  local profile_dir="$2"
  shift 2

  agent_browser_require_cli

  local output=""
  if output="$("${AGENT_BROWSER_CLI}" --session "${session}" --profile "${profile_dir}" "$@" 2>&1)"; then
    AGENT_BROWSER_LAST_OUTPUT="${output}"
    printf '%s\n' "${output}"
    return 0
  fi

  local rc=$?
  AGENT_BROWSER_LAST_OUTPUT="${output}"
  printf '%s\n' "${output}"
  return "${rc}"
}

agent_browser_last_text() {
  printf '%s\n' "${AGENT_BROWSER_LAST_OUTPUT:-}" | sed -e '1{s/^"//;s/"$//;}'
}

agent_browser_output_has() {
  local pattern="$1"
  printf '%s\n' "$(agent_browser_last_text)" | grep -Eq "${pattern}"
}

agent_browser_output_value() {
  local key="$1"
  printf '%s\n' "$(agent_browser_last_text)" | sed -n "s/.*${key}=\\([^;[:space:]]*\\).*/\\1/p" | tail -n 1
}

agent_browser_wait_for_eval_match() {
  local session="$1"
  local script="$2"
  local pattern="$3"
  local loops="${4:-20}"
  local delay_s="${5:-1}"
  local i

  for ((i = 0; i < loops; i++)); do
    agent_browser_run "${session}" eval "${script}" || true
    if agent_browser_output_has "${pattern}"; then
      return 0
    fi
    sleep "${delay_s}"
  done
  return 1
}

agent_browser_write_output() {
  local session="$1"
  local output_file="$2"
  shift 2

  agent_browser_require_cli
  "${AGENT_BROWSER_CLI}" --session "${session}" "$@" >"${output_file}" 2>&1
}

agent_browser_prepare_profile_dir() {
  local profile_dir="$1"
  if [[ ! -d "${profile_dir}" || -z "$(find "${profile_dir}" -mindepth 1 -print -quit 2>/dev/null)" ]]; then
    log "agent-browser profile dir missing or empty: ${profile_dir}"
    log "Log in to Telegram Web once with this profile before running smoke."
    exit 1
  fi
}
