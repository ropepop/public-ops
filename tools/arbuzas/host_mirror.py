#!/usr/bin/env python3
"""Local-first mirrors for host deployment variables and secrets."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import shutil
import shlex
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
from dataclasses import dataclass


MANIFEST_NAME = ".host-mirror-manifest.json"


@dataclass(frozen=True)
class Entry:
    kind: str
    rel: str


PROFILES: dict[str, list[Entry]] = {
    "arbuzas": [
        Entry("tree", "etc/arbuzas/env"),
        Entry("tree", "etc/arbuzas/secrets"),
        Entry("tree", "etc/arbuzas/cloudflared"),
        Entry("file", "etc/arbuzas/dns/runtime.env"),
        Entry("file", "etc/arbuzas/dns/arbuzas-dns.yaml"),
        Entry("file", "etc/arbuzas/dns/doh-identities.json"),
        Entry("tree", "etc/arbuzas/dns/tls"),
        Entry("tree", "etc/arbuzas/dns/secrets"),
        Entry("file", "etc/arbuzas/current/release.env"),
    ],
    "pixel": [
        Entry("tree", "data/local/pixel-stack/conf"),
    ],
}

EXCLUDES: dict[str, tuple[str, ...]] = {
    "arbuzas": (),
    "pixel": (
        "data/local/pixel-stack/conf/runtime/artifacts",
    ),
}

SERVICE_ORDER = [
    "portainer",
    "train_bot",
    "train_tunnel",
    "satiksme_bot",
    "satiksme_tunnel",
    "subscription_bot",
    "subscription_tunnel",
    "ticket_phone_bridge",
    "phone_broker",
    "rigassatiksme_qr_bot",
    "ticket_remote",
    "ticket_remote_tunnel",
    "dns_controlplane",
]


def eprint(message: str) -> None:
    print(message, file=sys.stderr)


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def is_excluded(profile: str, rel: str) -> bool:
    rel = rel.strip("/")
    if profile == "pixel" and rel.startswith("data/local/pixel-stack/conf/runtime/components/"):
        parts = pathlib.PurePosixPath(rel).parts
        if "artifacts" in parts:
            return True
    for excluded in EXCLUDES[profile]:
        if rel == excluded or rel.startswith(excluded + "/"):
            return True
    return False


def is_allowed(profile: str, rel: str) -> bool:
    rel = rel.strip("/")
    if rel == MANIFEST_NAME or is_excluded(profile, rel):
        return False
    for entry in PROFILES[profile]:
        if entry.kind == "file" and rel == entry.rel:
            return True
        if entry.kind == "tree" and (rel == entry.rel or rel.startswith(entry.rel + "/")):
            return True
    return False


def clean_empty_dirs(root: pathlib.Path) -> None:
    if not root.exists():
        return
    dirs = sorted((p for p in root.rglob("*") if p.is_dir()), reverse=True)
    for path in dirs:
        try:
            path.rmdir()
        except OSError:
            pass


def scan_files(root: pathlib.Path, profile: str) -> dict[str, dict[str, object]]:
    files: dict[str, dict[str, object]] = {}
    if not root.exists():
        return files
    for entry in PROFILES[profile]:
        base = root / entry.rel
        if entry.kind == "file":
            candidates = [base]
        else:
            if not base.exists():
                continue
            candidates = [p for p in base.rglob("*")]
        for path in candidates:
            if not path.exists() or not path.is_file():
                continue
            rel = path.relative_to(root).as_posix()
            if not is_allowed(profile, rel):
                continue
            st = path.stat()
            files[rel] = {
                "sha256": sha256_file(path),
                "mode": stat.S_IMODE(st.st_mode),
                "size": st.st_size,
            }
    return files


def manifest_path(mirror_root: pathlib.Path) -> pathlib.Path:
    return mirror_root / MANIFEST_NAME


def load_manifest(mirror_root: pathlib.Path) -> dict[str, object] | None:
    path = manifest_path(mirror_root)
    if not path.exists():
        return None
    return json.loads(path.read_text(encoding="utf-8"))


def write_manifest(mirror_root: pathlib.Path, profile: str, files: dict[str, dict[str, object]]) -> None:
    mirror_root.mkdir(parents=True, exist_ok=True)
    payload = {
        "profile": profile,
        "updatedEpochSeconds": int(time.time()),
        "files": files,
    }
    manifest_path(mirror_root).write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def classify(
    baseline: dict[str, dict[str, object]],
    local_files: dict[str, dict[str, object]],
    remote_files: dict[str, dict[str, object]],
) -> tuple[list[str], list[str], list[str]]:
    local_changed: list[str] = []
    remote_changed: list[str] = []
    conflicts: list[str] = []
    all_paths = sorted(set(baseline) | set(local_files) | set(remote_files))
    for rel in all_paths:
        base_hash = (baseline.get(rel) or {}).get("sha256")
        local_hash = (local_files.get(rel) or {}).get("sha256")
        remote_hash = (remote_files.get(rel) or {}).get("sha256")
        local_is_changed = local_hash != base_hash
        remote_is_changed = remote_hash != base_hash
        if local_is_changed and remote_is_changed and local_hash != remote_hash:
            conflicts.append(rel)
        elif local_is_changed:
            local_changed.append(rel)
        elif remote_is_changed:
            remote_changed.append(rel)
    return local_changed, remote_changed, conflicts


def ssh_base_args(args: argparse.Namespace) -> list[str]:
    command = ["ssh"]
    if args.ssh_port:
        command += ["-p", args.ssh_port]
    command.append(args.ssh_target)
    return command


def scp_base_args(args: argparse.Namespace) -> list[str]:
    command = ["scp"]
    if args.ssh_port:
        command += ["-P", args.ssh_port]
    return command


def remote_tar_command(profile: str) -> str:
    tar_parts = ["tar", "-C", "/", "--ignore-failed-read"]
    for excluded in EXCLUDES[profile]:
        tar_parts.append(f"--exclude={excluded}")
    tar_parts += ["-cf", "-"]
    tar_parts += [entry.rel for entry in PROFILES[profile]]
    return " ".join(shlex.quote(part) for part in tar_parts)


def fetch_remote_tree(args: argparse.Namespace, profile: str, destination: pathlib.Path) -> pathlib.Path:
    destination.mkdir(parents=True, exist_ok=True)
    if args.remote_root:
        source = pathlib.Path(args.remote_root)
        for entry in PROFILES[profile]:
            src = source / entry.rel
            dst = destination / entry.rel
            if not src.exists():
                continue
            if entry.kind == "file":
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(src, dst)
            else:
                shutil.copytree(src, dst, copy_function=shutil.copy2, dirs_exist_ok=True)
        for excluded in EXCLUDES[profile]:
            excluded_path = destination / excluded
            if excluded_path.exists():
                if excluded_path.is_dir():
                    shutil.rmtree(excluded_path)
                else:
                    excluded_path.unlink()
        return destination

    if not args.ssh_target:
        raise SystemExit("--ssh-target or --remote-root is required")
    command = ssh_base_args(args) + [remote_tar_command(profile)]
    with subprocess.Popen(command, stdout=subprocess.PIPE) as proc:
        assert proc.stdout is not None
        extract = subprocess.run(["tar", "-C", str(destination), "-xf", "-"], stdin=proc.stdout)
        proc.stdout.close()
        ssh_rc = proc.wait()
    if ssh_rc != 0 or extract.returncode != 0:
        raise SystemExit("failed to fetch remote mirror files")
    return destination


def copy_remote_to_mirror(remote_root: pathlib.Path, mirror_root: pathlib.Path, profile: str) -> dict[str, dict[str, object]]:
    mirror_root.mkdir(parents=True, exist_ok=True)
    for rel in list(scan_files(mirror_root, profile)):
        path = mirror_root / rel
        if path.exists():
            path.unlink()
    for rel in scan_files(remote_root, profile):
        src = remote_root / rel
        dst = mirror_root / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    clean_empty_dirs(mirror_root)
    return scan_files(mirror_root, profile)


def ensure_no_pull_overwrite(
    manifest: dict[str, object] | None,
    local_files: dict[str, dict[str, object]],
    remote_files: dict[str, dict[str, object]],
) -> None:
    if manifest is None:
        if local_files:
            raise SystemExit("local mirror already has files but no manifest; move it aside or run with a clean mirror")
        return
    baseline = manifest.get("files") or {}
    local_changed, _remote_changed, conflicts = classify(baseline, local_files, remote_files)  # type: ignore[arg-type]
    if conflicts:
        for rel in conflicts:
            eprint(f"conflict: {rel}")
        raise SystemExit("pull blocked by local and remote changes")
    if local_changed:
        for rel in local_changed:
            eprint(f"local changed: {rel}")
        raise SystemExit("pull blocked because local mirror has unpushed changes")


def command_pull(args: argparse.Namespace) -> int:
    profile = args.profile
    mirror_root = pathlib.Path(args.mirror_root)
    with tempfile.TemporaryDirectory() as tmp:
        remote_root = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile)
        remote_files = scan_files(remote_root, profile)
        ensure_no_pull_overwrite(load_manifest(mirror_root), local_files, remote_files)
        copied = copy_remote_to_mirror(remote_root, mirror_root, profile)
        write_manifest(mirror_root, profile, copied)
    print(f"pulled {len(copied)} files into {mirror_root}")
    return 0


def command_audit(args: argparse.Namespace) -> int:
    profile = args.profile
    mirror_root = pathlib.Path(args.mirror_root)
    manifest = load_manifest(mirror_root)
    if manifest is None:
        eprint("local mirror has no manifest; run mirror-pull first")
        return 3
    with tempfile.TemporaryDirectory() as tmp:
        remote_root = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile)
        remote_files = scan_files(remote_root, profile)
        local_changed, remote_changed, conflicts = classify(manifest.get("files") or {}, local_files, remote_files)  # type: ignore[arg-type]
    for rel in conflicts:
        eprint(f"conflict: {rel}")
    for rel in remote_changed:
        eprint(f"remote changed: {rel}")
    for rel in local_changed:
        print(f"local changed: {rel}")
    if conflicts or remote_changed:
        return 3
    print("mirror audit clean" if not local_changed else "mirror audit has local changes ready to push")
    return 0


def update_remote_root_local(remote_root: pathlib.Path, mirror_root: pathlib.Path, changed: list[str], local_files: dict[str, dict[str, object]]) -> None:
    for rel in changed:
        dst = remote_root / rel
        src = mirror_root / rel
        if rel not in local_files:
            if dst.exists():
                dst.unlink()
            continue
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)


def push_remote_ssh(args: argparse.Namespace, mirror_root: pathlib.Path, changed: list[str], local_files: dict[str, dict[str, object]]) -> None:
    with tempfile.TemporaryDirectory() as tmp:
        tmp_path = pathlib.Path(tmp)
        tar_path = tmp_path / "host-mirror-push.tar"
        deletes = [rel for rel in changed if rel not in local_files]
        with tarfile.open(tar_path, "w") as archive:
            delete_file = tmp_path / ".host-mirror-deletes.json"
            delete_file.write_text(json.dumps(deletes), encoding="utf-8")
            archive.add(delete_file, arcname=".host-mirror-deletes.json")
            for rel in changed:
                if rel not in local_files:
                    continue
                archive.add(mirror_root / rel, arcname=rel)
        remote_tar = f"/tmp/host-mirror-push-{os.getpid()}.tar"
        subprocess.run(scp_base_args(args) + [str(tar_path), f"{args.ssh_target}:{remote_tar}"], check=True)
        remote_script = r'''
import json
import os
import pathlib
import shutil
import stat
import sys
import tarfile
import tempfile

tar_path = pathlib.Path(sys.argv[1])
with tempfile.TemporaryDirectory() as tmp:
    tmp_path = pathlib.Path(tmp)
    with tarfile.open(tar_path, "r") as archive:
        archive.extractall(tmp_path)
        members = [m for m in archive.getmembers() if m.isfile() and m.name != ".host-mirror-deletes.json"]
    deletes = json.loads((tmp_path / ".host-mirror-deletes.json").read_text(encoding="utf-8"))
    for rel in deletes:
        dest = pathlib.Path("/") / rel
        try:
            dest.unlink()
        except FileNotFoundError:
            pass
    for member in members:
        src = tmp_path / member.name
        dest = pathlib.Path("/") / member.name
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(src, dest)
        os.chmod(dest, stat.S_IMODE(member.mode))
tar_path.unlink(missing_ok=True)
'''
        command = ssh_base_args(args) + ["python3 - " + shlex.quote(remote_tar)]
        subprocess.run(command, input=remote_script, text=True, check=True)


def write_changed_paths(path: str | None, changed: list[str]) -> None:
    if not path:
        return
    pathlib.Path(path).write_text("".join(f"{rel}\n" for rel in changed), encoding="utf-8")


def command_push(args: argparse.Namespace) -> int:
    profile = args.profile
    mirror_root = pathlib.Path(args.mirror_root)
    manifest = load_manifest(mirror_root)
    if manifest is None:
        eprint("local mirror has no manifest; run mirror-pull first")
        return 3
    with tempfile.TemporaryDirectory() as tmp:
        remote_snapshot = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile)
        remote_files = scan_files(remote_snapshot, profile)
        local_changed, remote_changed, conflicts = classify(manifest.get("files") or {}, local_files, remote_files)  # type: ignore[arg-type]
    for rel in conflicts:
        eprint(f"conflict: {rel}")
    for rel in remote_changed:
        eprint(f"remote changed: {rel}")
    if conflicts or remote_changed:
        eprint("push blocked; run mirror-pull or resolve conflicts before overwriting host files")
        return 3
    if not local_changed:
        write_changed_paths(args.changed_paths_file, [])
        print("mirror push clean; no local changes")
        return 0
    if args.remote_root:
        update_remote_root_local(pathlib.Path(args.remote_root), mirror_root, local_changed, local_files)
    else:
        push_remote_ssh(args, mirror_root, local_changed, local_files)
    write_manifest(mirror_root, profile, local_files)
    write_changed_paths(args.changed_paths_file, local_changed)
    for rel in local_changed:
        print(f"pushed: {rel}")
    return 0


def add_service(services: set[str], name: str) -> None:
    services.add(name)


def affected_services_for_path(rel: str) -> set[str]:
    services: set[str] = set()
    if rel == "etc/arbuzas/current/release.env":
        services.update(SERVICE_ORDER)
    if rel == "etc/arbuzas/env/train-bot.env":
        add_service(services, "train_bot")
    elif rel == "etc/arbuzas/env/satiksme-bot.env":
        add_service(services, "satiksme_bot")
    elif rel == "etc/arbuzas/env/subscription-bot.env":
        add_service(services, "subscription_bot")
    elif rel == "etc/arbuzas/env/ticket-remote.env":
        add_service(services, "ticket_remote")
    elif rel == "etc/arbuzas/env/rigassatiksme-qr-bot.env":
        add_service(services, "rigassatiksme_qr_bot")
    elif rel.startswith("etc/arbuzas/secrets/ticket-remote/"):
        add_service(services, "ticket_remote")
    elif rel.startswith("etc/arbuzas/secrets/android-adb/"):
        add_service(services, "ticket_phone_bridge")
    elif rel.startswith("etc/arbuzas/secrets/"):
        services.update({"train_bot", "satiksme_bot", "subscription_bot", "ticket_remote"})
    elif rel.startswith("etc/arbuzas/cloudflared/"):
        name = pathlib.PurePosixPath(rel).name
        tunnel_map = {
            "train-bot.json": "train_tunnel",
            "satiksme-bot.json": "satiksme_tunnel",
            "subscription-bot.json": "subscription_tunnel",
            "ticket-remote.json": "ticket_remote_tunnel",
        }
        if name in tunnel_map:
            add_service(services, tunnel_map[name])
    elif rel.startswith("etc/arbuzas/dns/"):
        add_service(services, "dns_controlplane")
    return services


def command_affected(args: argparse.Namespace) -> int:
    if args.profile != "arbuzas":
        return 0
    changed_file = pathlib.Path(args.changed_paths_file)
    if not changed_file.exists():
        return 0
    services: set[str] = set()
    for raw in changed_file.read_text(encoding="utf-8").splitlines():
        rel = raw.strip()
        if not rel:
            continue
        services.update(affected_services_for_path(rel))
    for service in SERVICE_ORDER:
        if service in services:
            print(service)
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["pull", "audit", "push", "affected"])
    parser.add_argument("--profile", choices=sorted(PROFILES), required=True)
    parser.add_argument("--mirror-root", default="")
    parser.add_argument("--remote-root", default="")
    parser.add_argument("--ssh-target", default="")
    parser.add_argument("--ssh-port", default="")
    parser.add_argument("--changed-paths-file", default="")
    args = parser.parse_args(argv)

    if args.command != "affected" and not args.mirror_root:
        parser.error("--mirror-root is required")
    if args.command == "affected" and not args.changed_paths_file:
        parser.error("--changed-paths-file is required for affected")

    if args.command == "pull":
        return command_pull(args)
    if args.command == "audit":
        return command_audit(args)
    if args.command == "push":
        return command_push(args)
    if args.command == "affected":
        return command_affected(args)
    raise AssertionError(args.command)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
