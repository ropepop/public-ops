#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
policy_root="${repo_root}/infra/arbuzas/host-security"

grep -Fxq 'PermitRootLogin yes' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'PasswordAuthentication yes' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'MaxAuthTries 4' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'PerSourceMaxStartups 3' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'X11Forwarding no' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'AllowAgentForwarding no' "${policy_root}/sshd/00-local-hardening.conf"
grep -Fxq 'AllowTcpForwarding local' "${policy_root}/sshd/00-local-hardening.conf"

grep -Fxq 'banaction = iptables-multiport' "${policy_root}/fail2ban/90-sshd-local.conf"
grep -Fxq 'action = iptables-multiport[name=sshd, port=ssh, protocol=tcp, chain=INPUT]' "${policy_root}/fail2ban/90-sshd-local.conf"
grep -Fxq 'maxretry = 4' "${policy_root}/fail2ban/90-sshd-local.conf"
grep -Fxq 'findtime = 10m' "${policy_root}/fail2ban/90-sshd-local.conf"
grep -Fxq 'bantime = 6h' "${policy_root}/fail2ban/90-sshd-local.conf"
grep -Fxq 'bantime.maxtime = 1w' "${policy_root}/fail2ban/90-sshd-local.conf"

grep -Fxq 'LLMNR=no' "${policy_root}/systemd/00-disable-llmnr.conf"

if rg -n '(^|[[:space:]])(AllowUsers|DenyUsers|AllowGroups|DenyGroups|ignoreip)[[:space:]=]' "${policy_root}"; then
  echo 'host security policy unexpectedly contains an explicit allow/deny list' >&2
  exit 1
fi

echo 'host security policy contract passed'
