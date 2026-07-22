#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

# The deployment-timing workload remains available as a migration source, but
# the live deploy script now reports to the unified operational logger. Keep
# this legacy test entry point useful by exercising the canonical integration.
exec bash "${REPO_ROOT}/workloads/operational-logging/scripts/deploy_integration_test.sh"
