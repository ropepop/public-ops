# Arbuzas Boost-Off Boot Hook

This directory stores the live Arbuzas host setup that keeps Intel turbo boost
disabled across reboots without restoring the old `2 GHz` CPU lock.

It consists of:

- `home/ropepop/.local/bin/arbuzas-disable-boost.sh`: the reboot hook script
- `ropepop.crontab`: the `@reboot` entry for the `ropepop` user
- `Dockerfile`: the helper image recipe for `arbuzas/cpu-boost-off:latest`

## What It Does

At boot, the script waits for Docker, runs a small privileged helper container,
and writes `1` to the host `no_turbo` control file.

That keeps turbo boost off while still allowing normal dynamic CPU scaling below
turbo range.

The script writes logs to:

- `~/.local/state/arbuzas/cpu-boost-off.log`

## Rebuild The Helper Image

Run this on the Arbuzas host:

```sh
docker build -t arbuzas/cpu-boost-off:latest /path/to/repo/infra/arbuzas/cpu-boost-off
```

## Reinstall The Boot Hook

Install the script and crontab entry for the `ropepop` user:

```sh
install -D -m 0755 \
  /path/to/repo/infra/arbuzas/cpu-boost-off/home/ropepop/.local/bin/arbuzas-disable-boost.sh \
  /home/ropepop/.local/bin/arbuzas-disable-boost.sh

crontab /path/to/repo/infra/arbuzas/cpu-boost-off/ropepop.crontab
```
