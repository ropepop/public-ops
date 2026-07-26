#!/usr/bin/env python3
"""Local-first mirrors for host deployment variables and secrets."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import secrets
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

HOST_HISTORY_TAR_EXCLUDES = (
    "etc/arbuzas/env/*.bak*",
    "etc/arbuzas/env/*.before-*",
    "etc/arbuzas/env/*.retired-*",
    "etc/arbuzas/env/*~",
)


@dataclass(frozen=True)
class Entry:
    kind: str
    rel: str
    pushable: bool = True


PROFILES: dict[str, list[Entry]] = {
    "arbuzas": [
        Entry("tree", "etc/arbuzas/env"),
        Entry("tree", "etc/arbuzas/secrets"),
        Entry("tree", "etc/arbuzas/cloudflared"),
        Entry("file", "etc/arbuzas/current/release.env", pushable=False),
    ],
    "pixel": [
        Entry("tree", "data/local/pixel-stack/conf"),
    ],
    "ticket-recovery": [
        Entry("file", "etc/arbuzas/env/ticket-remote.env"),
        Entry("file", "etc/arbuzas/env/train-bot.env"),
        Entry("file", "etc/arbuzas/secrets/android-adb/adbkey"),
        Entry("file", "etc/arbuzas/secrets/android-adb/adbkey.pub"),
        Entry("file", "etc/arbuzas/secrets/android-adb/adb_known_hosts.pb"),
        Entry("file", "etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem"),
        Entry("file", "etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret"),
        Entry("file", "etc/arbuzas/secrets/ticket-remote/turn.secret"),
        Entry("file", "etc/arbuzas/secrets/train-bot-spacetime.key"),
        Entry("file", "etc/arbuzas/secrets/train-bot-web-session-secret"),
        Entry("file", "etc/arbuzas/secrets/train-bot-test-ticket.secret"),
        Entry("file", "etc/arbuzas/cloudflared/ticket-remote.json"),
        Entry("file", "etc/arbuzas/cloudflared/train-bot.json"),
    ],
}

EXCLUDES: dict[str, tuple[str, ...]] = {
    "arbuzas": (),
    "ticket-recovery": (),
    "pixel": (
        "data/local/pixel-stack/conf/runtime/artifacts",
    ),
}

SERVICE_ORDER = [
    "train_bot",
    "train_tunnel",
    "satiksme_bot",
    "satiksme_tunnel",
    "ticket_phone_bridge",
    "ticket_remote_spacetime_sidecar",
    "ticket_remote",
    "ticket_remote_tunnel",
    "tiny_vless",
]


def eprint(message: str) -> None:
    print(message, file=sys.stderr)


def sha256_file(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def is_host_history_file(rel: str) -> bool:
    """Return whether a host env path is an unmanaged historical copy."""
    rel = rel.strip("/")
    if not rel.startswith("etc/arbuzas/env/"):
        return False
    name = pathlib.PurePosixPath(rel).name
    return (
        ".bak" in name
        or ".before-" in name
        or ".retired-" in name
        or name.endswith("~")
    )


def required_private_mode(rel: str) -> int | None:
    """Return the local/remote mode required for credential-bearing mirrors."""
    rel = rel.strip("/")
    if rel == "etc/arbuzas/current/release.env":
        return 0o600
    if rel.startswith(
        (
            "etc/arbuzas/env/",
            "etc/arbuzas/secrets/",
            "etc/arbuzas/cloudflared/",
        )
    ):
        return 0o600
    return None


def metadata_fingerprint(
    metadata: dict[str, object] | None,
    *,
    include_mode: bool = True,
) -> tuple[object, ...] | None:
    if metadata is None:
        return None
    if include_mode:
        return metadata.get("sha256"), metadata.get("mode")
    return (metadata.get("sha256"),)


def is_excluded(profile: str, rel: str) -> bool:
    rel = rel.strip("/")
    if profile in ("arbuzas", "ticket-recovery") and is_host_history_file(rel):
        return True
    if profile == "pixel" and rel.startswith("data/local/pixel-stack/conf/runtime/components/"):
        parts = pathlib.PurePosixPath(rel).parts
        if "artifacts" in parts:
            return True
    for excluded in EXCLUDES[profile]:
        if rel == excluded or rel.startswith(excluded + "/"):
            return True
    return False


def matching_entry(profile: str, rel: str) -> Entry | None:
    rel = rel.strip("/")
    if rel == MANIFEST_NAME or is_excluded(profile, rel):
        return None
    for entry in PROFILES[profile]:
        if entry.kind == "file" and rel == entry.rel:
            return entry
        if entry.kind == "tree" and (rel == entry.rel or rel.startswith(entry.rel + "/")):
            return entry
    return None


def is_allowed(profile: str, rel: str, *, pushable_only: bool = False) -> bool:
    entry = matching_entry(profile, rel)
    return entry is not None and (entry.pushable or not pushable_only)


def is_pull_only(profile: str, rel: str) -> bool:
    entry = matching_entry(profile, rel)
    return entry is not None and not entry.pushable


def clean_empty_dirs(root: pathlib.Path) -> None:
    if not root.exists():
        return
    dirs = sorted((p for p in root.rglob("*") if p.is_dir()), reverse=True)
    for path in dirs:
        try:
            path.rmdir()
        except OSError:
            pass


def harden_local_private_modes(root: pathlib.Path, profile: str) -> None:
    """Repair checkout-default modes before inspecting or publishing a mirror."""
    if not root.exists():
        return
    for entry in PROFILES[profile]:
        base = root / entry.rel
        if entry.kind == "file":
            candidates = [base]
        elif base.exists():
            candidates = list(base.rglob("*"))
        else:
            candidates = []
        for path in candidates:
            if not path.is_file() or path.is_symlink():
                continue
            rel = path.relative_to(root).as_posix()
            if not is_allowed(profile, rel):
                continue
            private_mode = required_private_mode(rel)
            if private_mode is not None and stat.S_IMODE(path.stat().st_mode) != private_mode:
                path.chmod(private_mode)
    manifest = manifest_path(root)
    if manifest.is_file() and not manifest.is_symlink():
        manifest.chmod(0o600)


def scan_files(
    root: pathlib.Path,
    profile: str,
    *,
    pushable_only: bool = False,
) -> dict[str, dict[str, object]]:
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
            if not is_allowed(profile, rel, pushable_only=pushable_only):
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


def managed_manifest_files(
    manifest: dict[str, object],
    profile: str,
) -> dict[str, dict[str, object]]:
    files = manifest.get("files") or {}
    if not isinstance(files, dict):
        raise SystemExit("host mirror manifest has an invalid files object")
    return {
        rel: metadata
        for rel, metadata in files.items()
        if isinstance(rel, str)
        and isinstance(metadata, dict)
        and is_allowed(profile, rel)
    }


def write_manifest(mirror_root: pathlib.Path, profile: str, files: dict[str, dict[str, object]]) -> None:
    mirror_root.mkdir(parents=True, exist_ok=True)
    payload = {
        "profile": profile,
        "updatedEpochSeconds": int(time.time()),
        "files": files,
    }
    path = manifest_path(mirror_root)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    path.chmod(0o600)


def classify(
    baseline: dict[str, dict[str, object]],
    local_files: dict[str, dict[str, object]],
    remote_files: dict[str, dict[str, object]],
    *,
    content_only_paths: set[str] | None = None,
) -> tuple[list[str], list[str], list[str]]:
    local_changed: list[str] = []
    remote_changed: list[str] = []
    conflicts: list[str] = []
    content_only_paths = content_only_paths or set()
    all_paths = sorted(set(baseline) | set(local_files) | set(remote_files))
    for rel in all_paths:
        include_mode = rel not in content_only_paths
        base_metadata = baseline.get(rel)
        local_metadata = local_files.get(rel)
        remote_metadata = remote_files.get(rel)
        base_fingerprint = metadata_fingerprint(base_metadata, include_mode=include_mode)
        local_fingerprint = metadata_fingerprint(local_metadata, include_mode=include_mode)
        remote_fingerprint = metadata_fingerprint(remote_metadata, include_mode=include_mode)
        local_is_changed = local_fingerprint != base_fingerprint
        remote_is_changed = remote_fingerprint != base_fingerprint

        if local_is_changed and remote_is_changed and local_fingerprint == remote_fingerprint:
            # Both sides already converged on the same state, so there is no
            # endpoint drift to reconcile. Ignoring the stale baseline here
            # lets an unrelated safe local change proceed instead of forcing a
            # pull that would overwrite it.
            continue

        has_conflict = False
        if local_is_changed and remote_is_changed and local_fingerprint != remote_fingerprint:
            if base_metadata is None or local_metadata is None or remote_metadata is None:
                has_conflict = True
            else:
                local_content_changed = local_metadata.get("sha256") != base_metadata.get("sha256")
                remote_content_changed = remote_metadata.get("sha256") != base_metadata.get("sha256")
                content_conflict = (
                    local_content_changed
                    and remote_content_changed
                    and local_metadata.get("sha256") != remote_metadata.get("sha256")
                )
                mode_conflict = False
                if include_mode:
                    local_mode_changed = local_metadata.get("mode") != base_metadata.get("mode")
                    remote_mode_changed = remote_metadata.get("mode") != base_metadata.get("mode")
                    mode_conflict = (
                        local_mode_changed
                        and remote_mode_changed
                        and local_metadata.get("mode") != remote_metadata.get("mode")
                    )
                has_conflict = content_conflict or mode_conflict
        if has_conflict:
            conflicts.append(rel)
        else:
            remote_change_is_in_local = False
            local_change_is_in_remote = False
            if local_is_changed and remote_is_changed:
                # Content and mode are independent dimensions. A remote change
                # is safe to subsume only when the local desired state already
                # contains that exact change (and vice versa). This permits a
                # local content migration after both sides converged from 0644
                # to 0600, without overlooking different remote content/modes.
                assert base_metadata is not None
                assert local_metadata is not None
                assert remote_metadata is not None
                remote_change_is_in_local = (
                    remote_metadata.get("sha256") == base_metadata.get("sha256")
                    or local_metadata.get("sha256") == remote_metadata.get("sha256")
                ) and (
                    not include_mode
                    or remote_metadata.get("mode") == base_metadata.get("mode")
                    or local_metadata.get("mode") == remote_metadata.get("mode")
                )
                local_change_is_in_remote = (
                    local_metadata.get("sha256") == base_metadata.get("sha256")
                    or remote_metadata.get("sha256") == local_metadata.get("sha256")
                ) and (
                    not include_mode
                    or local_metadata.get("mode") == base_metadata.get("mode")
                    or remote_metadata.get("mode") == local_metadata.get("mode")
                )

            if local_is_changed and not (
                local_change_is_in_remote and not remote_change_is_in_local
            ):
                local_changed.append(rel)
            if remote_is_changed and not remote_change_is_in_local:
                remote_changed.append(rel)
    return local_changed, remote_changed, conflicts


def ssh_base_args(args: argparse.Namespace) -> list[str]:
    command = ["ssh"]
    if args.ssh_known_hosts_file:
        command += [
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            f"UserKnownHostsFile={args.ssh_known_hosts_file}",
        ]
    if args.ssh_port:
        command += ["-p", args.ssh_port]
    command.append(args.ssh_target)
    return command


def scp_base_args(args: argparse.Namespace) -> list[str]:
    command = ["scp"]
    if args.ssh_known_hosts_file:
        command += [
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            f"UserKnownHostsFile={args.ssh_known_hosts_file}",
        ]
    if args.ssh_port:
        command += ["-P", args.ssh_port]
    return command


def remote_tar_command(profile: str) -> str:
    if os.environ.get("ARBUZAS_HOST_MIRROR_PRIVILEGED") == "1":
        tar_parts = ["sudo", "-n", "tar", "-C", "/", "--ignore-failed-read"]
    else:
        tar_parts = ["tar", "-C", "/", "--ignore-failed-read"]
    for excluded in EXCLUDES[profile]:
        tar_parts.append(f"--exclude={excluded}")
    if profile in ("arbuzas", "ticket-recovery"):
        for excluded in HOST_HISTORY_TAR_EXCLUDES:
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
    if profile in ("arbuzas", "ticket-recovery"):
        for path in mirror_root.joinpath("etc/arbuzas/env").glob("*"):
            if path.is_file() and is_host_history_file(path.relative_to(mirror_root).as_posix()):
                path.unlink()
    for rel in list(scan_files(mirror_root, profile)):
        path = mirror_root / rel
        if path.exists():
            path.unlink()
    for rel in scan_files(remote_root, profile):
        src = remote_root / rel
        dst = mirror_root / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
        private_mode = required_private_mode(rel)
        if private_mode is not None:
            dst.chmod(private_mode)
    clean_empty_dirs(mirror_root)
    return scan_files(mirror_root, profile)


def ensure_no_pull_overwrite(
    profile: str,
    manifest: dict[str, object] | None,
    local_files: dict[str, dict[str, object]],
    remote_files: dict[str, dict[str, object]],
) -> None:
    if manifest is None:
        if local_files:
            raise SystemExit("local mirror already has files but no manifest; move it aside or run with a clean mirror")
        return
    baseline = managed_manifest_files(manifest, profile)
    content_only_paths = {
        rel for rel in set(baseline) | set(local_files) | set(remote_files) if is_pull_only(profile, rel)
    }
    local_changed, _remote_changed, conflicts = classify(  # type: ignore[arg-type]
        baseline,
        local_files,
        remote_files,
        content_only_paths=content_only_paths,
    )
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
    harden_local_private_modes(mirror_root, profile)
    with tempfile.TemporaryDirectory() as tmp:
        remote_root = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile)
        remote_files = scan_files(remote_root, profile)
        ensure_no_pull_overwrite(profile, load_manifest(mirror_root), local_files, remote_files)
        copied = copy_remote_to_mirror(remote_root, mirror_root, profile)
        # Keep the actual remote metadata as the baseline. If pull hardened a
        # local mode, the next audit/push correctly reports that permission-only
        # change instead of treating the insecure host mode as desired state.
        write_manifest(mirror_root, profile, remote_files)
    print(f"pulled {len(copied)} files into {mirror_root}")
    return 0


def command_audit(args: argparse.Namespace) -> int:
    profile = args.profile
    mirror_root = pathlib.Path(args.mirror_root)
    harden_local_private_modes(mirror_root, profile)
    manifest = load_manifest(mirror_root)
    if manifest is None:
        eprint("local mirror has no manifest; run mirror-pull first")
        return 3
    with tempfile.TemporaryDirectory() as tmp:
        remote_root = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile)
        remote_files = scan_files(remote_root, profile)
        baseline = managed_manifest_files(manifest, profile)
        content_only_paths = {
            rel for rel in set(baseline) | set(local_files) | set(remote_files) if is_pull_only(profile, rel)
        }
        local_changed, remote_changed, conflicts = classify(  # type: ignore[arg-type]
            baseline,
            local_files,
            remote_files,
            content_only_paths=content_only_paths,
        )
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
        tar_path.chmod(0o600)

        remote_staging_dir = f"/tmp/host-mirror-push-{secrets.token_hex(16)}"
        remote_tar = f"{remote_staging_dir}/payload.tar"
        staging_dir_q = shlex.quote(remote_staging_dir)
        remote_tar_q = shlex.quote(remote_tar)
        upload_script = f"""
set -eu
umask 077
staging_dir={staging_dir_q}
tar_path={remote_tar_q}
if [ -e "$staging_dir" ] || [ -L "$staging_dir" ]; then
  echo "refusing existing host-mirror staging path: $staging_dir" >&2
  exit 1
fi
mkdir -m 0700 -- "$staging_dir"
: > "$tar_path"
chmod 0600 "$tar_path"
cat > "$tar_path"
python3 - "$staging_dir" "$tar_path" <<'PY'
import pathlib
import stat
import sys

directory = pathlib.Path(sys.argv[1])
payload = pathlib.Path(sys.argv[2])
directory_stat = directory.lstat()
payload_stat = payload.lstat()
if not stat.S_ISDIR(directory_stat.st_mode) or stat.S_IMODE(directory_stat.st_mode) != 0o700:
    raise SystemExit("host-mirror staging directory is unsafe")
if not stat.S_ISREG(payload_stat.st_mode) or stat.S_IMODE(payload_stat.st_mode) != 0o600:
    raise SystemExit("host-mirror staging archive is unsafe")
PY
"""
        cleanup_script = f"""
staging_dir={staging_dir_q}
tar_path={remote_tar_q}
if [ -d "$staging_dir" ] && [ ! -L "$staging_dir" ]; then
  rm -f -- "$tar_path"
  rmdir -- "$staging_dir" 2>/dev/null || true
fi
"""
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
staging_dir = tar_path.parent
staging_stat = staging_dir.lstat()
tar_stat = tar_path.lstat()
if not stat.S_ISDIR(staging_stat.st_mode) or stat.S_IMODE(staging_stat.st_mode) != 0o700:
    raise SystemExit("refusing unsafe host-mirror staging directory")
if not stat.S_ISREG(tar_stat.st_mode) or stat.S_IMODE(tar_stat.st_mode) != 0o600:
    raise SystemExit("refusing unsafe host-mirror staging archive")
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
'''
        try:
            with tar_path.open("rb") as handle:
                subprocess.run(ssh_base_args(args) + [upload_script], stdin=handle, check=True)
            command = ssh_base_args(args) + [
                "set -eu; "
                f"staging_dir={staging_dir_q}; tar_path={remote_tar_q}; "
                "[ -d \"$staging_dir\" ] && [ ! -L \"$staging_dir\" ] && "
                "[ -f \"$tar_path\" ] && [ ! -L \"$tar_path\" ] || { "
                "echo 'refusing unsafe host-mirror staging path' >&2; exit 1; }; "
                "if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then "
                "sudo -n python3 - \"$tar_path\"; else python3 - \"$tar_path\"; fi"
            ]
            subprocess.run(command, input=remote_script, text=True, check=True)
        finally:
            subprocess.run(
                ssh_base_args(args) + [cleanup_script],
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )


def write_changed_paths(path: str | None, changed: list[str]) -> None:
    if not path:
        return
    pathlib.Path(path).write_text("".join(f"{rel}\n" for rel in changed), encoding="utf-8")


def command_push(args: argparse.Namespace) -> int:
    profile = args.profile
    mirror_root = pathlib.Path(args.mirror_root)
    harden_local_private_modes(mirror_root, profile)
    manifest = load_manifest(mirror_root)
    if manifest is None:
        eprint("local mirror has no manifest; run mirror-pull first")
        return 3
    baseline_all = managed_manifest_files(manifest, profile)
    local_files_all = scan_files(mirror_root, profile)
    locally_changed_pull_only = sorted(
        rel
        for rel in set(baseline_all) | set(local_files_all)
        if is_pull_only(profile, rel)
        # Pull-only snapshots may be locally mode-hardened even when an older
        # host release is still 0644. Block content edits, but let the dedicated
        # release permission repair own the host-side mode transition.
        and (baseline_all.get(rel) or {}).get("sha256")
        != (local_files_all.get(rel) or {}).get("sha256")
    )
    if locally_changed_pull_only:
        for rel in locally_changed_pull_only:
            eprint(f"pull-only local changed: {rel}")
        eprint("push blocked because pull-only migration snapshots cannot be written to the host")
        return 3
    with tempfile.TemporaryDirectory() as tmp:
        remote_snapshot = fetch_remote_tree(args, profile, pathlib.Path(tmp) / "remote")
        local_files = scan_files(mirror_root, profile, pushable_only=True)
        remote_files = scan_files(remote_snapshot, profile, pushable_only=True)
        baseline = {
            rel: metadata
            for rel, metadata in baseline_all.items()
            if is_allowed(profile, rel, pushable_only=True)
        }
        local_changed, remote_changed, conflicts = classify(baseline, local_files, remote_files)  # type: ignore[arg-type]
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
    write_manifest(mirror_root, profile, local_files_all)
    write_changed_paths(args.changed_paths_file, local_changed)
    for rel in local_changed:
        print(f"pushed: {rel}")
    return 0


def add_service(services: set[str], name: str) -> None:
    services.add(name)


def affected_services_for_path(rel: str) -> set[str]:
    services: set[str] = set()
    if rel == "etc/arbuzas/env/tiny-vless.env":
        add_service(services, "tiny_vless")
    elif rel.startswith("etc/arbuzas/secrets/tiny-vless/"):
        add_service(services, "tiny_vless")
    elif rel == "etc/arbuzas/env/train-bot.env":
        add_service(services, "train_bot")
    elif rel == "etc/arbuzas/env/satiksme-bot.env":
        add_service(services, "satiksme_bot")
    elif rel == "etc/arbuzas/env/ticket-remote.env":
        add_service(services, "ticket_remote_spacetime_sidecar")
        add_service(services, "ticket_remote")
    elif rel == "etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret":
        add_service(services, "ticket_remote_spacetime_sidecar")
        add_service(services, "ticket_remote")
    elif rel == "etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem":
        add_service(services, "ticket_remote_spacetime_sidecar")
    elif rel.startswith("etc/arbuzas/secrets/ticket-remote/"):
        add_service(services, "ticket_remote_spacetime_sidecar")
        add_service(services, "ticket_remote")
    elif rel.startswith("etc/arbuzas/secrets/android-adb/"):
        add_service(services, "ticket_phone_bridge")
    elif rel.startswith("etc/arbuzas/secrets/satiksme-chat-analyzer/"):
        add_service(services, "satiksme_bot")
    elif rel in {
        "etc/arbuzas/secrets/satiksme-bot-spacetime.key",
        "etc/arbuzas/secrets/satiksme-bot-web-session-secret",
        "etc/arbuzas/secrets/satiksme-telegram-client.secret",
    }:
        add_service(services, "satiksme_bot")
    elif rel.startswith("etc/arbuzas/secrets/train-bot-"):
        add_service(services, "train_bot")
    elif rel.startswith("etc/arbuzas/secrets/"):
        services.update({"train_bot", "satiksme_bot", "ticket_remote"})
    elif rel.startswith("etc/arbuzas/cloudflared/"):
        name = pathlib.PurePosixPath(rel).name
        tunnel_map = {
            "train-bot.json": "train_tunnel",
            "satiksme-bot.json": "satiksme_tunnel",
            "ticket-remote.json": "ticket_remote_tunnel",
        }
        if name in tunnel_map:
            add_service(services, tunnel_map[name])
    return services


def command_affected(args: argparse.Namespace) -> int:
    if args.profile not in ("arbuzas", "ticket-recovery"):
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
    parser.add_argument("--ssh-known-hosts-file", default="")
    parser.add_argument("--changed-paths-file", default="")
    args = parser.parse_args(argv)

    if args.ssh_known_hosts_file:
        known_hosts = pathlib.Path(args.ssh_known_hosts_file)
        if not known_hosts.is_absolute() or not known_hosts.is_file() or not os.access(known_hosts, os.R_OK):
            parser.error("--ssh-known-hosts-file must name a readable absolute file")

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
