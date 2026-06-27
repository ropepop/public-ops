# Arbuzas RAM Maxout Investigation Plan

**Goal:** Reconstruct why Arbuzas used all available RAM during deployment, then choose resource-conscious deployment and workload-splitting changes from measured facts instead of guesses.

**Current known shape from repo docs and deploy code:** Arbuzas is a single-host Docker Compose runtime. A normal full deploy prepares a release bundle, builds the DNS image, runs DNS preparation commands, switches `/etc/arbuzas/current`, then runs `docker compose up -d --build --force-recreate` for the non-DNS services, recreates tunnels, recreates DNS, validates the stack, and only then runs cleanup. The Compose file currently has no memory limits. Build work happens on the Arbuzas host while existing containers are still running.

## Finishing Criteria

This investigation is done only when all of these are true:

- We have a timeline for the RAM maxout: deployment command, release id, start/end time, and the moment memory was exhausted.
- We know whether the peak came from image builds, live containers, container recreation overlap, validation, DNS preparation, Docker cache behavior, logs/state growth, swap absence, or a combination.
- We have a current steady-state footprint table by service: memory, disk/state, role, dependencies, and whether it is required to stay on kitty-gration.
- We have a deployment peak profile: which build or service pushed RAM up, how high it went, and what else was running at that time.
- We have architecture options ranked by impact, risk, and effort: deploy sequencing, off-host builds, service grouping, memory limits, preflight checks, swap/zram, and possible workload split.
- We have a chosen first change set with a rollback path and validation plan.
- Any future changes are tested by repeating the same measurements and showing lower peak memory or a clearly larger safety margin.

## Guardrails

- Start read-only. Do not delete images, prune caches, restart containers, deploy, or change architecture during fact collection.
- Do not print secrets, environment file contents, browser sessions, databases, or user activity logs.
- Treat cleanup and migration as later actions. Cleanup can hide the evidence if it runs too early.
- Use the Arbuzas SSH profile and the repo deploy entrypoint for deploy or validation actions:
  - `./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user ropepop`
  - `./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user ropepop`
- If config/secrets/env are touched later, pull and reconcile the local host mirror first.

## Phase 1: Reconstruct The Incident

Purpose: determine what actually happened during the failed or stressed deployment.

Evidence to collect:

- The exact deployment command that was run.
- The release id involved.
- Approximate local and Arbuzas host time of the maxout.
- Whether the deploy completed, failed, rolled back, or needed manual recovery.
- Any terminal output from the deploy command.
- Kernel and Docker evidence for memory pressure or killed processes.

Read-only host checks:

```bash
ssh kitty-gration 'date; uptime; free -h; swapon --show || true'
ssh kitty-gration 'journalctl -k --since "24 hours ago" --no-pager | grep -Ei "out of memory|oom|killed process|memory allocation|invoked oom" || true'
ssh kitty-gration 'journalctl -u docker --since "24 hours ago" --no-pager | grep -Ei "oom|killed|memory|build|compose|container" || true'
ssh kitty-gration 'docker events --since 24h --until 0s --filter event=oom --filter event=die --filter event=kill 2>/dev/null || true'
```

Output to produce:

- A short timeline with exact times where possible.
- A list of processes or containers killed, if any.
- A first classification: build peak, runtime peak, Docker daemon pressure, validation pressure, or unknown.

## Phase 2: Capture Current Steady-State Footprint

Purpose: understand today’s baseline before reproducing or changing anything.

Read-only host checks:

```bash
ssh kitty-gration 'date; hostname; uname -a; uptime; free -h; swapon --show || true'
ssh kitty-gration 'df -h / /srv/arbuzas /etc/arbuzas /var/lib/docker 2>/dev/null || df -h'
ssh kitty-gration 'docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"'
ssh kitty-gration 'docker stats --no-stream --format "table {{.Name}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.CPUPerc}}\t{{.BlockIO}}\t{{.PIDs}}"'
ssh kitty-gration 'docker system df -v'
ssh kitty-gration 'du -sh /srv/arbuzas/* /etc/arbuzas/releases /var/lib/docker 2>/dev/null || true'
```

Service groups to attribute:

- Core host operations: Portainer, Netdata, Docker daemon, host OS.
- Public app group: `train_bot`, `satiksme_bot`, `subscription_bot`.
- Phone group: `ticket_phone_bridge`, `phone_broker`, `ticket_remote`.
- Public tunnel group: `train_tunnel`, `satiksme_tunnel`, `subscription_tunnel`, `ticket_remote_tunnel`.
- DNS group: `dns_controlplane`.
- Persistent state: `/srv/arbuzas/*`.
- Release and build footprint: `/etc/arbuzas/releases`, Docker images, Docker build cache.

Output to produce:

- One table with memory and disk by group.
- Notes on what must stay on kitty-gration because of hardware, DNS ports, private state, or public routing.
- Notes on what is a candidate for splitting out later.

## Phase 3: Map The Deployment Peak

Purpose: identify where deployment creates memory overlap.

Known deploy phases to verify:

- Local release bundle preparation.
- Remote release upload and extraction.
- DNS image build.
- DNS migration and policy sync.
- Current release symlink switch.
- Non-DNS `docker compose up -d --build --force-recreate`.
- Tunnel recreation.
- DNS recreation.
- Full validation.
- Cleanup after successful validation.

Suspects to test or rule out:

- Rust release build for `dns_controlplane`.
- Multiple Go service builds happening too close together.
- Satiksme image build, because it compiles several binaries in one image.
- Old containers staying live while replacement images are built.
- Docker build cache behavior after cleanup or cache misses.
- Validation temporarily increasing load while all services are fresh.
- No swap or too little swap headroom.
- No per-container memory ceilings.

Output to produce:

- A phase-by-phase deployment diagram.
- For each phase, whether RAM rises, falls, or stays flat.
- A clear statement of the highest-risk phase.

## Phase 4: Measure A Reproduction Safely

Purpose: capture the peak with a sampler before changing architecture.

This phase should happen only after we agree on timing, because it may run a real deploy or build.

Start a lightweight host sampler before the deploy:

```bash
ssh kitty-gration 'out="/tmp/arbuzas-ram-sampler-$(date +%Y%m%dT%H%M%S).log"; echo "$out"; while true; do echo "=== $(date -Is) ==="; free -m; docker stats --no-stream --format "{{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}\t{{.PIDs}}" 2>/dev/null || true; ps -eo pid,ppid,rss,pcpu,comm --sort=-rss | head -25; sleep 2; done | tee "$out"'
```

Then run one of these, depending on risk:

```bash
./tools/arbuzas/deploy.sh deploy --services dns_controlplane --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh deploy --services satiksme_bot --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user ropepop
```

After the run, stop the sampler and copy the log into evidence:

```bash
mkdir -p ops/evidence/arbuzas/2026-06-04-ram-maxout
scp kitty-gration:/tmp/arbuzas-ram-sampler-*.log ops/evidence/arbuzas/2026-06-04-ram-maxout/
```

Output to produce:

- Peak RAM timestamp.
- Top memory consumers at the peak.
- Whether the peak appears during build, restart, validation, or cleanup.
- Whether targeted deploys avoid the peak.

## Phase 5: Decide Architecture Options From Evidence

Evaluate these options only after Phases 1-4 have facts:

- Make targeted deploys the normal default for small changes.
- Split full deploy into waves so only one heavy build runs at a time.
- Add a deploy preflight that refuses a full build when available RAM and swap are below a measured threshold.
- Add a deploy sampler or summary so every release records peak memory.
- Build images away from kitty-gration and only ship finished images to the host.
- Add runtime memory ceilings for services that can safely restart if they misbehave.
- Add swap or zram if the host has none or too little emergency headroom.
- Split workload groups across hosts:
  - Keep the physical-phone group near the Pixel unless the phone connection model changes.
  - Consider whether public app bots can move away from DNS and phone services.
  - Consider whether DNS deserves to stay isolated because it owns public `443/853`.
  - Keep tunnels paired with the services they expose unless routing is redesigned.

Output to produce:

- Recommendation matrix with impact, risk, effort, and rollback difficulty.
- One preferred first step, one medium-term step, and one option rejected with reasons.

## Phase 6: Implement Only The Chosen First Step

Possible first changes, depending on measured cause:

- If build concurrency is the cause: deploy one build-heavy service at a time or enforce one-at-a-time Compose build behavior if the live Docker Compose version supports it.
- If Rust DNS build is the cause: move DNS image building off-host or separate DNS deploy from app deploys.
- If Go builds are the cause: wave the app builds or move app image builds off-host.
- If runtime containers are the cause: add service-level memory ceilings and validate behavior under normal traffic.
- If no swap is the cause: add conservative swap/zram with clear monitoring and test that deployment survives memory pressure.
- If disk/cache pressure is the real bottleneck: tune cleanup, but do not treat disk cleanup as a RAM fix unless measurements show it helps.

Verification after any change:

```bash
bash -n tools/arbuzas/deploy.sh
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user ropepop
```

Then repeat the same sampler from Phase 4 and compare peak memory against the baseline.

## First Pass Local Findings

- The full deploy path builds on kitty-gration, not just locally.
- Cleanup runs after successful deploy/rollback, so it does not protect the peak build moment.
- The Compose file has no visible memory ceilings.
- The DNS image runs a Rust release build.
- Most app images run Go builds.
- The Satiksme image builds multiple Go binaries in one image.
- The deploy script already supports targeted service deploys, which is likely useful as a lower-risk mitigation path.
