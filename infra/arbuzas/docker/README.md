# Arbuzas Docker Layout

This directory is the active production deployment layout for the single-host Arbuzas runtime.

## What Lives Here

- `compose.yml`: the one active Docker Compose project for Portainer, apps, tunnels, and DNS.
- `env/arbuzas.example.env`: the operator template for hostnames, ports, and image pins.
- `images/`: Dockerfiles and entrypoints for the Arbuzas-owned workloads and DNS sidecars.

## Host Layout

- Persistent state: `/srv/arbuzas`
- Secrets and runtime env files: `/etc/arbuzas`
- Release bundles: `/etc/arbuzas/releases/<release-id>`
- Active release symlink: `/etc/arbuzas/current`

## Operator Entry Point

- Active deploy flow: `tools/arbuzas/deploy.sh`

Portainer runs directly against the local Docker socket on port `9443`. The live Arbuzas host must stay out of Docker Swarm, and the active repair flow now rewrites stale `tasks.agent` state in place before falling back to a clean first-run setup. The old Swarm and Pixel/orchestrator deployment paths are rollback-only legacy material.
The native Arbuzas DNS controlplane publishes encrypted DNS directly on host ports `443` and `853`.
