# Arbuzas CPU Lock

This directory preserves the live Arbuzas CPU lock setup that was found on the
host on April 15, 2026.

It consists of:

- `home/ropepop/.local/bin/arbuzas-set-cpu-2ghz.sh`: the host boot script
- `ropepop.crontab`: the `@reboot` entry that starts it
- `Dockerfile`: the helper image recipe for `arbuzas/cpu-lock:latest`

## What It Does

At boot, the script waits for Docker, starts a privileged helper container, and
then:

- turns Intel turbo off
- sets the CPU governor to `performance`
- pins min and max CPU frequency to `2GHz`
- verifies the lock on all four CPU cores

The script writes logs to:

- `~/.local/state/arbuzas/cpu-lock.log`

## Rebuild The Helper Image

Run this on the Arbuzas host:

```sh
docker build -t arbuzas/cpu-lock:latest /path/to/repo/infra/arbuzas/cpu-lock
```

## Reinstall The Boot Hook

Install the script and crontab entry for the `ropepop` user:

```sh
install -D -m 0755 \
  /path/to/repo/infra/arbuzas/cpu-lock/home/ropepop/.local/bin/arbuzas-set-cpu-2ghz.sh \
  /home/ropepop/.local/bin/arbuzas-set-cpu-2ghz.sh

crontab /path/to/repo/infra/arbuzas/cpu-lock/ropepop.crontab
```
