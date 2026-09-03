# kitty-gration host security baseline

These files are the reviewed host-native security policy for `kitty-gration`.
They deliberately preserve public root login by password or key and do not
create a host-wide allowlist firewall.

- `sshd/00-local-hardening.conf` must sort before the provider and cloud-init
  SSH drop-ins because OpenSSH uses the first value it obtains.
- `fail2ban/90-sshd-local.conf` uses the already installed iptables backend;
  the previous nftables action could not run because the `nft` command was
  absent.
- `systemd/00-disable-llmnr.conf` disables LLMNR without changing ordinary DNS.
- `modules-load/arbuzas-qbittorrent-loop.conf` loads the kernel loop driver
  before the capped qBittorrent/Jellyfin media image is mounted. The storage
  installer deploys and verifies this policy, and the matching systemd mount
  drop-in explicitly waits for `systemd-modules-load.service`.

Before installation, keep one root session and one `ropepop` plus `sudo`
session open and back up every replaced file. Validate SSH with `sshd -t`
before reload. Validate Fail2ban with `fail2ban-client -t` before restart.
After installation and after every reboot, prove fresh root password, root key,
and `ropepop` key access; inspect the effective SSH settings; verify an
`f2b-sshd` INPUT hook; and confirm a temporary TEST-NET ban can be added and
removed.
