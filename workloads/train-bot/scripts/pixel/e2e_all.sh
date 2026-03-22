#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"${SCRIPT_DIR}/public_smoke.sh"
"${SCRIPT_DIR}/miniapp_smoke.sh"
"${SCRIPT_DIR}/e2e_agent_browser.sh"
