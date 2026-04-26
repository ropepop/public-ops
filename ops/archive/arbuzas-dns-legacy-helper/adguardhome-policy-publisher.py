#!/usr/bin/env python3
"""AdGuard policy publisher helper for Arbuzas DNS."""

from __future__ import annotations

import argparse
import contextlib
import copy
import dataclasses
import datetime as dt
import fcntl
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from http.cookiejar import CookieJar
from pathlib import Path
from typing import Any

import yaml

MANAGED_POLICY_KEYS = ("filters", "whitelist_filters", "user_rules")
POLICY_TOP_LEVEL_KEYS = ("schema_version",) + MANAGED_POLICY_KEYS
FILTER_HASH_IGNORED_KEYS = {"id", "last_updated", "rules_count"}


class PolicyError(RuntimeError):
    """Raised when the public DNS policy is invalid."""

    def __init__(self, message: str, *, category: str = "config") -> None:
        super().__init__(message)
        self.category = category


class LockError(PolicyError):
    def __init__(self, message: str) -> None:
        super().__init__(message, category="lock")


class GitError(PolicyError):
    def __init__(self, message: str) -> None:
        super().__init__(message, category="git")


class AdGuardApiError(PolicyError):
    def __init__(self, message: str) -> None:
        super().__init__(message, category="adguard-api")


class TransientPolicyReadError(PolicyError):
    def __init__(self, message: str) -> None:
        super().__init__(message, category="transient-read")


def utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def utc_now_iso() -> str:
    return utc_now().isoformat().replace("+00:00", "Z")


def read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def normalize_text(text: str) -> str:
    lines = [line.rstrip() for line in text.splitlines()]
    return "\n".join(lines) + "\n"


def write_text_atomic(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    normalized = normalize_text(text)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=str(path.parent), delete=False) as handle:
        handle.write(normalized)
        temp_path = Path(handle.name)
    temp_path.replace(path)


def write_json_atomic(path: Path, payload: dict[str, Any]) -> None:
    write_text_atomic(path, json.dumps(payload, indent=2, sort_keys=True))


def read_json_file(path: Path, default: dict[str, Any] | None = None) -> dict[str, Any]:
    if not path.exists():
        return copy.deepcopy(default or {})
    try:
        raw = json.loads(read_text(path))
    except json.JSONDecodeError as exc:
        raise PolicyError(f"invalid json in {path}: {exc}", category="config") from exc
    if not isinstance(raw, dict):
        raise PolicyError(f"json file must contain an object: {path}")
    return raw


def load_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    env: dict[str, str] = {}
    for raw_line in read_text(path).splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        env[key.strip()] = value.strip()
    return env


def ensure_directory_writable(path: Path, *, label: str) -> None:
    path.mkdir(parents=True, exist_ok=True)
    try:
        with tempfile.NamedTemporaryFile(dir=str(path), prefix=".write-test-", delete=True):
            pass
    except OSError as exc:
        raise PolicyError(f"{label} is not writable: {path} ({exc})", category="config") from exc


def require_readable_file(path: Path, *, label: str) -> None:
    if not path.exists():
        raise PolicyError(f"{label} is missing: {path}", category="config")
    if not path.is_file():
        raise PolicyError(f"{label} is not a file: {path}", category="config")
    if not os.access(path, os.R_OK):
        raise PolicyError(f"{label} is not readable: {path}", category="config")


def default_state(config: "Config") -> dict[str, Any]:
    return {
        "branch": config.repo_branch,
        "last_published_hash": "",
        "last_published_commit": "",
        "last_clean_timestamp": "",
        "last_source": "",
        "last_stable_live_hash": "",
        "last_stable_live_timestamp": "",
    }


def default_drift() -> dict[str, Any]:
    return {
        "active": False,
        "failed_hash": "",
        "first_failure_timestamp": "",
        "last_attempt_timestamp": "",
        "last_error": "",
    }


@dataclasses.dataclass(frozen=True)
class Config:
    runtime_env_file: Path
    base_config_file: Path
    source_config_file: Path
    rendered_config_file: Path
    state_dir: Path
    state_file: Path
    drift_file: Path
    health_file: Path
    lock_file: Path
    published_policy_snapshot_file: Path
    generated_source_snapshot_file: Path
    live_policy_snapshot_file: Path
    repo_checkout_dir: Path
    repo_fetch_url: str
    repo_push_url: str
    repo_branch: str
    repo_ssh_key_file: str
    repo_author_name: str
    repo_author_email: str
    adguard_base_url: str
    adguard_username: str
    adguard_password_file: Path
    lock_wait_seconds: float
    stable_read_attempts: int
    stable_read_delay_seconds: float

    @classmethod
    def from_env(cls) -> "Config":
        runtime_env_file = Path(
            os.environ.get("ARBUZAS_DNS_RUNTIME_ENV_FILE", "/etc/arbuzas/dns/runtime.env")
        )
        runtime_env = load_env_file(runtime_env_file)

        def env_value(name: str, default: str = "") -> str:
            value = os.environ.get(name)
            if value is not None:
                return value
            return runtime_env.get(name, default)

        state_dir = Path(env_value("ARBUZAS_DNS_POLICY_STATE_DIR", "/srv/arbuzas/dns/state"))
        web_host = env_value("PIHOLE_WEB_HOST", env_value("ADGUARDHOME_WEB_HOST", "127.0.0.1")).strip() or "127.0.0.1"
        web_port = env_value("PIHOLE_WEB_PORT", env_value("ADGUARDHOME_WEB_PORT", "8080")).strip() or "8080"
        adguard_base_url = env_value("ARBUZAS_DNS_POLICY_ADGUARD_BASE_URL", f"http://{web_host}:{web_port}")

        return cls(
            runtime_env_file=runtime_env_file,
            base_config_file=Path(env_value("ARBUZAS_DNS_BASE_CONFIG_FILE", "/etc/arbuzas/dns/AdGuardHome.base.yaml")),
            source_config_file=Path(env_value("ARBUZAS_DNS_SOURCE_CONFIG_FILE", "/etc/arbuzas/dns/AdGuardHome.source.yaml")),
            rendered_config_file=Path(
                env_value("ARBUZAS_DNS_RENDERED_CONFIG_FILE", "/srv/arbuzas/dns/adguardhome/conf/AdGuardHome.yaml")
            ),
            state_dir=state_dir,
            state_file=state_dir / "policy-publisher-state.json",
            drift_file=state_dir / "policy-publisher-drift.json",
            health_file=state_dir / "policy-publisher-health.json",
            lock_file=state_dir / "policy-publisher.lock",
            published_policy_snapshot_file=state_dir / "policy-publisher-last-published-policy.yaml",
            generated_source_snapshot_file=state_dir / "policy-publisher-last-generated-source.yaml",
            live_policy_snapshot_file=state_dir / "policy-publisher-last-live-policy.yaml",
            repo_checkout_dir=Path(
                env_value("ARBUZAS_DNS_POLICY_REPO_CHECKOUT_DIR", str(state_dir / "policy-publisher-repo"))
            ),
            repo_fetch_url=env_value("ARBUZAS_DNS_POLICY_REPO_FETCH_URL"),
            repo_push_url=env_value("ARBUZAS_DNS_POLICY_REPO_PUSH_URL"),
            repo_branch=env_value("ARBUZAS_DNS_POLICY_REPO_BRANCH", "main"),
            repo_ssh_key_file=env_value("ARBUZAS_DNS_POLICY_REPO_SSH_KEY_FILE"),
            repo_author_name=env_value("ARBUZAS_DNS_POLICY_REPO_AUTHOR_NAME", "Arbuzas DNS Policy Publisher"),
            repo_author_email=env_value(
                "ARBUZAS_DNS_POLICY_REPO_AUTHOR_EMAIL",
                "dns-policy-publisher@arbuzas.local",
            ),
            adguard_base_url=adguard_base_url.rstrip("/"),
            adguard_username=env_value("ADGUARDHOME_ADMIN_USERNAME", "pihole"),
            adguard_password_file=Path(
                env_value("ADGUARDHOME_ADMIN_PASSWORD_FILE", "/etc/arbuzas/dns/secrets/admin-password")
            ),
            lock_wait_seconds=max(0.1, float(env_value("ARBUZAS_DNS_POLICY_LOCK_WAIT_SECONDS", "3"))),
            stable_read_attempts=max(2, int(env_value("ARBUZAS_DNS_POLICY_STABLE_READ_ATTEMPTS", "3"))),
            stable_read_delay_seconds=max(
                0.05, float(env_value("ARBUZAS_DNS_POLICY_STABLE_READ_DELAY_SECONDS", "0.2"))
            ),
        )


def load_yaml_file(path: Path, *, allow_missing: bool = False) -> Any:
    if allow_missing and not path.exists():
        return {}
    if not path.exists():
        raise PolicyError(f"missing yaml file: {path}")
    try:
        data = yaml.safe_load(read_text(path))
    except yaml.YAMLError as exc:
        raise PolicyError(f"invalid yaml in {path}: {exc}", category="config") from exc
    return {} if data is None else data


def dump_yaml_text(payload: Any) -> str:
    return normalize_text(
        yaml.safe_dump(
            payload,
            sort_keys=False,
            default_flow_style=False,
            allow_unicode=False,
        )
    )


def write_yaml_atomic(path: Path, payload: Any) -> None:
    write_text_atomic(path, dump_yaml_text(payload))


def require_mapping(payload: Any, *, label: str) -> dict[str, Any]:
    if not isinstance(payload, dict):
        raise PolicyError(f"{label} must be a YAML object")
    return payload


def normalize_rule(rule: Any) -> str:
    if not isinstance(rule, str):
        raise PolicyError("user_rules entries must be strings")
    return rule.rstrip(" \t\r\n")


def normalize_filter_entry(entry: Any) -> dict[str, Any]:
    if not isinstance(entry, dict):
        raise PolicyError("filter entries must be YAML objects")
    normalized: dict[str, Any] = {}
    for key, value in entry.items():
        if isinstance(value, str):
            normalized[str(key)] = value.rstrip(" \t\r\n")
        else:
            normalized[str(key)] = value
    if "url" not in normalized:
        raise PolicyError("filter entries must include url")
    if "name" not in normalized:
        raise PolicyError("filter entries must include name")
    if "enabled" not in normalized:
        raise PolicyError("filter entries must include enabled")
    return normalized


def normalize_policy(raw_policy: Any) -> dict[str, Any]:
    if raw_policy is None:
        raw_policy = {}
    payload = require_mapping(raw_policy, label="policy")
    unknown = [key for key in payload.keys() if key not in POLICY_TOP_LEVEL_KEYS]
    if unknown:
        raise PolicyError(f"policy contains unsupported top-level keys: {', '.join(sorted(map(str, unknown)))}")

    schema_version = payload.get("schema_version", 1)
    if schema_version != 1:
        raise PolicyError(f"policy schema_version must be 1 (got {schema_version!r})")

    normalized: dict[str, Any] = {"schema_version": 1}
    for key in ("filters", "whitelist_filters"):
        entries = payload.get(key, [])
        if entries is None:
            entries = []
        if not isinstance(entries, list):
            raise PolicyError(f"policy key {key} must be a list")
        normalized[key] = [normalize_filter_entry(entry) for entry in entries]

    rules = payload.get("user_rules", [])
    if rules is None:
        rules = []
    if not isinstance(rules, list):
        raise PolicyError("policy key user_rules must be a list")
    normalized["user_rules"] = [normalize_rule(rule) for rule in rules]
    return normalized


def policy_to_yaml_text(policy: dict[str, Any]) -> str:
    ordered = {
        "schema_version": 1,
        "filters": copy.deepcopy(policy["filters"]),
        "whitelist_filters": copy.deepcopy(policy["whitelist_filters"]),
        "user_rules": copy.deepcopy(policy["user_rules"]),
    }
    return dump_yaml_text(ordered)


def policy_hash(policy: dict[str, Any]) -> str:
    hashable = normalize_policy(policy)
    for key in ("filters", "whitelist_filters"):
        for entry in hashable[key]:
            for ignored_key in FILTER_HASH_IGNORED_KEYS:
                entry.pop(ignored_key, None)
    return hashlib.sha256(policy_to_yaml_text(hashable).encode("utf-8")).hexdigest()


def extract_policy_from_config(config_payload: Any) -> dict[str, Any]:
    config = require_mapping(config_payload, label="AdGuard config")
    extracted = {
        "schema_version": 1,
        "filters": copy.deepcopy(config.get("filters", [])),
        "whitelist_filters": copy.deepcopy(config.get("whitelist_filters", [])),
        "user_rules": copy.deepcopy(config.get("user_rules", [])),
    }
    return normalize_policy(extracted)


def strip_policy_from_config(config_payload: Any) -> dict[str, Any]:
    config = require_mapping(config_payload, label="AdGuard config")
    stripped: dict[str, Any] = {}
    for key, value in config.items():
        if key not in MANAGED_POLICY_KEYS:
            stripped[key] = copy.deepcopy(value)
    return stripped


def merge_base_with_policy(base_payload: Any, policy: dict[str, Any]) -> dict[str, Any]:
    base = strip_policy_from_config(base_payload)
    merged: dict[str, Any] = {}
    inserted = False
    for key, value in base.items():
        if not inserted and key in ("log", "schema_version"):
            for managed_key in MANAGED_POLICY_KEYS:
                merged[managed_key] = copy.deepcopy(policy[managed_key])
            inserted = True
        merged[key] = copy.deepcopy(value)
    if not inserted:
        for managed_key in MANAGED_POLICY_KEYS:
            merged[managed_key] = copy.deepcopy(policy[managed_key])
    return merged


def ensure_bootstrap_ready(config: Config) -> None:
    if config.base_config_file.exists():
        return
    if not config.source_config_file.exists():
        raise PolicyError(
            f"missing both base and source config files: {config.base_config_file} / {config.source_config_file}"
        )
    source_config = load_yaml_file(config.source_config_file)
    write_yaml_atomic(config.base_config_file, strip_policy_from_config(source_config))


def read_state(config: Config) -> dict[str, Any]:
    payload = default_state(config)
    payload.update(read_json_file(config.state_file, default=default_state(config)))
    return payload


def read_drift(config: Config) -> dict[str, Any]:
    payload = default_drift()
    payload.update(read_json_file(config.drift_file, default=default_drift()))
    return payload


def write_state_update(config: Config, **updates: Any) -> dict[str, Any]:
    payload = read_state(config)
    payload.update(updates)
    payload["branch"] = config.repo_branch
    write_json_atomic(config.state_file, payload)
    return payload


def write_state(config: Config, *, policy_hash_value: str, commit: str, source: str) -> None:
    write_state_update(
        config,
        last_published_hash=policy_hash_value,
        last_published_commit=commit,
        last_clean_timestamp=utc_now_iso(),
        last_source=source,
    )


def update_state_from_existing(config: Config, *, policy_hash_value: str, source: str) -> None:
    state = read_state(config)
    write_state(
        config,
        policy_hash_value=policy_hash_value,
        commit=str(state.get("last_published_commit", "")),
        source=source,
    )


def clear_drift(config: Config) -> None:
    payload = default_drift()
    payload["last_attempt_timestamp"] = utc_now_iso()
    write_json_atomic(config.drift_file, payload)


def set_drift(config: Config, *, failed_hash: str, error: str) -> None:
    existing = read_drift(config)
    first_failure = existing.get("first_failure_timestamp") or utc_now_iso()
    write_json_atomic(
        config.drift_file,
        {
            "active": True,
            "failed_hash": failed_hash,
            "first_failure_timestamp": first_failure,
            "last_attempt_timestamp": utc_now_iso(),
            "last_error": error,
        },
    )


def write_health(
    config: Config,
    *,
    mode: str,
    live_policy_hash: str = "",
    last_stable_live_policy_hash: str = "",
    message: str = "",
    failure_category: str = "",
) -> None:
    drift = read_drift(config)
    state = read_state(config)
    payload = {
        "heartbeat_timestamp": utc_now_iso(),
        "mode": mode,
        "live_policy_hash": live_policy_hash,
        "last_published_hash": str(state.get("last_published_hash", "")).strip(),
        "last_published_commit": str(state.get("last_published_commit", "")).strip(),
        "last_stable_live_policy_hash": last_stable_live_policy_hash
        or str(state.get("last_stable_live_hash", "")).strip(),
        "drift_active": bool(drift.get("active")),
        "retrying": mode == "retrying",
        "failure_category": failure_category,
        "message": message,
    }
    write_json_atomic(config.health_file, payload)


def safe_write_health(
    config: Config,
    *,
    mode: str,
    live_policy_hash: str = "",
    last_stable_live_policy_hash: str = "",
    message: str = "",
    failure_category: str = "",
) -> None:
    try:
        write_health(
            config,
            mode=mode,
            live_policy_hash=live_policy_hash,
            last_stable_live_policy_hash=last_stable_live_policy_hash,
            message=message,
            failure_category=failure_category,
        )
    except Exception:
        return


def require_clean_drift(config: Config) -> None:
    drift = read_drift(config)
    if drift.get("active"):
        failed_hash = str(drift.get("failed_hash", "")).strip()
        last_error = str(drift.get("last_error", "")).strip()
        detail = []
        if failed_hash:
            detail.append(f"hash={failed_hash}")
        if last_error:
            detail.append(last_error)
        suffix = f" ({'; '.join(detail)})" if detail else ""
        raise PolicyError(f"policy publisher is frozen in drift state{suffix}")


@contextlib.contextmanager
def mutation_lock(config: Config, *, operation: str) -> Any:
    ensure_directory_writable(config.state_dir, label="policy state directory")
    deadline = time.monotonic() + config.lock_wait_seconds
    fd = os.open(config.lock_file, os.O_CREAT | os.O_RDWR, 0o600)
    with os.fdopen(fd, "r+", encoding="utf-8", closefd=True) as handle:
        while True:
            try:
                fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise LockError(
                        f"timed out waiting for policy mutation lock after {config.lock_wait_seconds:.1f}s"
                    )
                time.sleep(0.1)
        try:
            handle.seek(0)
            handle.truncate()
            handle.write(json.dumps({"operation": operation, "pid": os.getpid(), "timestamp": utc_now_iso()}))
            handle.flush()
            yield
        finally:
            handle.seek(0)
            handle.truncate()
            handle.flush()
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


def record_published_policy_snapshot(config: Config, policy: dict[str, Any]) -> None:
    write_text_atomic(config.published_policy_snapshot_file, policy_to_yaml_text(policy))


def record_generated_source_snapshot(config: Config, rendered_source: dict[str, Any]) -> None:
    write_text_atomic(config.generated_source_snapshot_file, dump_yaml_text(rendered_source))


def record_live_policy_snapshot(config: Config, policy: dict[str, Any], *, policy_hash_value: str) -> None:
    write_text_atomic(config.live_policy_snapshot_file, policy_to_yaml_text(policy))


def local_repo_path(value: str) -> Path | None:
    if not value:
        return None
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme == "file":
        return Path(parsed.path)
    if parsed.scheme:
        return None
    if value.startswith(("git@", "ssh://", "http://", "https://")):
        return None
    if value.startswith(("/", "./", "../")):
        return Path(value)
    return Path(value) if Path(value).is_absolute() else None


def push_url_requires_ssh_key(value: str) -> bool:
    return value.startswith("git@") or value.startswith("ssh://")


def validate_bootstrap_inputs(config: Config) -> None:
    has_base = config.base_config_file.exists()
    has_source = config.source_config_file.exists()
    if not has_base and not has_source:
        raise PolicyError(
            f"missing both base and source config files: {config.base_config_file} / {config.source_config_file}",
            category="config",
        )
    if has_base:
        require_readable_file(config.base_config_file, label="AdGuard base config")
        load_yaml_file(config.base_config_file)
    if has_source:
        require_readable_file(config.source_config_file, label="AdGuard source config")
        load_yaml_file(config.source_config_file)


def validate_runtime(config: Config, *, operation: str) -> dict[str, Any]:
    ensure_directory_writable(config.state_dir, label="policy state directory")
    validate_bootstrap_inputs(config)

    result: dict[str, Any] = {}
    if operation in {"deploy-sync", "restore-published"}:
        if not config.repo_fetch_url.strip():
            raise PolicyError("ARBUZAS_DNS_POLICY_REPO_FETCH_URL is not configured", category="config")
        result["policy"] = fetch_policy(config)

    if operation in {"watch", "publish-current"}:
        if not config.repo_push_url.strip():
            raise PolicyError("ARBUZAS_DNS_POLICY_REPO_PUSH_URL is not configured", category="config")
        repo_path = local_repo_path(config.repo_push_url)
        if repo_path is not None:
            if not repo_path.exists():
                raise PolicyError(f"policy repo path does not exist: {repo_path}", category="config")
        else:
            if push_url_requires_ssh_key(config.repo_push_url):
                if not config.repo_ssh_key_file.strip():
                    raise PolicyError("ARBUZAS_DNS_POLICY_REPO_SSH_KEY_FILE is not configured", category="config")
                require_readable_file(Path(config.repo_ssh_key_file), label="policy repo SSH key")
        ensure_directory_writable(config.repo_checkout_dir.parent, label="policy repo checkout parent")

    if operation == "restore-published":
        require_readable_file(config.adguard_password_file, label="AdGuard admin password file")

    if operation == "publish-current":
        if not config.rendered_config_file.exists():
            raise PolicyError(f"live AdGuard config is missing: {config.rendered_config_file}", category="config")

    return result


def render_source_from_policy(config: Config, policy: dict[str, Any]) -> dict[str, Any]:
    ensure_bootstrap_ready(config)
    base_config = load_yaml_file(config.base_config_file)
    merged = merge_base_with_policy(base_config, policy)
    write_yaml_atomic(config.source_config_file, merged)
    return merged


def fetch_policy_text(config: Config) -> str:
    if not config.repo_fetch_url:
        raise PolicyError("ARBUZAS_DNS_POLICY_REPO_FETCH_URL is not configured")
    parsed = urllib.parse.urlparse(config.repo_fetch_url)
    if parsed.scheme in ("", "file"):
        file_path = Path(parsed.path if parsed.scheme == "file" else config.repo_fetch_url)
        try:
            return read_text(file_path)
        except OSError as exc:
            raise GitError(f"failed to read published policy: {exc}") from exc
    try:
        with urllib.request.urlopen(config.repo_fetch_url, timeout=15) as response:
            return response.read().decode("utf-8", errors="replace")
    except OSError as exc:
        raise GitError(f"failed to fetch published policy: {exc}") from exc


def fetch_policy(config: Config) -> dict[str, Any]:
    return normalize_policy(yaml.safe_load(fetch_policy_text(config)))


def git_env(config: Config, extra_env: dict[str, str] | None = None) -> dict[str, str]:
    env = os.environ.copy()
    if config.repo_ssh_key_file:
        env["GIT_SSH_COMMAND"] = (
            f"ssh -i {config.repo_ssh_key_file} -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
        )
    if extra_env:
        env.update(extra_env)
    return env


def run_git(config: Config, *args: str, cwd: Path, extra_env: dict[str, str] | None = None) -> str:
    process = subprocess.run(
        ["git", *args],
        cwd=str(cwd),
        env=git_env(config, extra_env),
        capture_output=True,
        text=True,
        check=False,
    )
    if process.returncode != 0:
        detail = (process.stderr or process.stdout or "").strip()
        raise GitError(f"git {' '.join(args)} failed: {detail}")
    return process.stdout.strip()


def ensure_repo_checkout(config: Config) -> Path:
    if not config.repo_push_url:
        raise PolicyError("ARBUZAS_DNS_POLICY_REPO_PUSH_URL is not configured")
    repo_dir = config.repo_checkout_dir
    repo_dir.parent.mkdir(parents=True, exist_ok=True)
    if not (repo_dir / ".git").exists():
        if repo_dir.exists():
            shutil.rmtree(repo_dir)
        run_git(
            config,
            "clone",
            "--branch",
            config.repo_branch,
            "--single-branch",
            config.repo_push_url,
            str(repo_dir),
            cwd=repo_dir.parent,
        )
        return repo_dir

    run_git(config, "remote", "set-url", "origin", config.repo_push_url, cwd=repo_dir)
    try:
        run_git(config, "checkout", config.repo_branch, cwd=repo_dir)
    except PolicyError:
        run_git(config, "checkout", "-B", config.repo_branch, cwd=repo_dir)
    run_git(config, "fetch", "origin", config.repo_branch, cwd=repo_dir)
    run_git(config, "reset", "--hard", f"origin/{config.repo_branch}", cwd=repo_dir)
    return repo_dir


def publish_policy(config: Config, policy: dict[str, Any], *, source: str) -> dict[str, Any]:
    ensure_bootstrap_ready(config)
    repo_dir = ensure_repo_checkout(config)
    policy_text = policy_to_yaml_text(policy)
    policy_path = repo_dir / "policy.yaml"
    write_text_atomic(policy_path, policy_text)
    run_git(config, "add", "policy.yaml", cwd=repo_dir)

    diff = subprocess.run(
        ["git", "diff", "--cached", "--quiet"],
        cwd=str(repo_dir),
        env=git_env(config),
        capture_output=True,
        text=True,
        check=False,
    )
    commit = run_git(config, "rev-parse", "HEAD", cwd=repo_dir) if (repo_dir / ".git").exists() else ""
    changed = diff.returncode != 0
    if changed:
        author_env = {
            "GIT_AUTHOR_NAME": config.repo_author_name,
            "GIT_AUTHOR_EMAIL": config.repo_author_email,
            "GIT_COMMITTER_NAME": config.repo_author_name,
            "GIT_COMMITTER_EMAIL": config.repo_author_email,
        }
        run_git(
            config,
            "commit",
            "-m",
            f"Publish AdGuard policy {utc_now_iso()}",
            cwd=repo_dir,
            extra_env=author_env,
        )
        run_git(config, "push", "origin", config.repo_branch, cwd=repo_dir)
        commit = run_git(config, "rev-parse", "HEAD", cwd=repo_dir)

    merged_source = render_source_from_policy(config, policy)
    record_published_policy_snapshot(config, policy)
    record_generated_source_snapshot(config, merged_source)
    clear_drift(config)
    write_state(config, policy_hash_value=policy_hash(policy), commit=commit, source=source)
    write_health(
        config,
        mode="clean",
        live_policy_hash=policy_hash(policy),
        last_stable_live_policy_hash=policy_hash(policy),
        message="publish succeeded",
    )
    return {"changed": changed, "commit": commit, "hash": policy_hash(policy)}


def sanitize_filter_for_apply(entry: dict[str, Any]) -> dict[str, Any]:
    return {
        "enabled": bool(entry.get("enabled", True)),
        "name": str(entry.get("name", "")).strip(),
        "url": str(entry.get("url", "")).strip(),
    }


class AdGuardApiClient:
    def __init__(self, config: Config) -> None:
        self.base_url = config.adguard_base_url.rstrip("/")
        self.username = config.adguard_username
        self.password_file = config.adguard_password_file
        self.cookie_jar = CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookie_jar))

    def _load_password(self) -> str:
        if not self.password_file.exists():
            raise AdGuardApiError(f"missing AdGuard password file: {self.password_file}")
        password = read_text(self.password_file).strip()
        if not password:
            raise AdGuardApiError(f"AdGuard password file is empty: {self.password_file}")
        return password

    def request(self, method: str, path: str, payload: Any | None = None) -> tuple[int, str]:
        url = f"{self.base_url}{path}"
        data = None
        headers = {"Accept": "application/json"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(url=url, method=method, data=data, headers=headers)
        try:
            with self.opener.open(req, timeout=15) as response:
                return int(getattr(response, "status", 200)), response.read().decode("utf-8", errors="replace")
        except urllib.error.HTTPError as exc:
            return int(exc.code), exc.read().decode("utf-8", errors="replace")

    def request_json(self, method: str, path: str, payload: Any | None = None) -> Any:
        status, body = self.request(method, path, payload)
        if status != 200:
            raise AdGuardApiError(f"AdGuard API {method} {path} failed with status={status}: {body[:200]}")
        if not body.strip():
            return {}
        return json.loads(body)

    def login(self) -> None:
        status, body = self.request(
            "POST",
            "/control/login",
            {"name": self.username, "password": self._load_password()},
        )
        if status != 200:
            raise AdGuardApiError(f"AdGuard login failed with status={status}: {body[:200]}")

    def get_filtering_status(self) -> dict[str, Any]:
        status = self.request_json("GET", "/control/filtering/status")
        return require_mapping(status, label="filtering status")

    def replace_filter_list(self, *, current: list[dict[str, Any]], desired: list[dict[str, Any]], whitelist: bool) -> None:
        current_normalized = [sanitize_filter_for_apply(entry) for entry in current]
        desired_normalized = [sanitize_filter_for_apply(entry) for entry in desired]
        if current_normalized == desired_normalized:
            return
        current_urls = [str(entry.get("url", "")).strip() for entry in current if str(entry.get("url", "")).strip()]
        for url in current_urls:
            self.request_json("POST", "/control/filtering/remove_url", {"url": url, "whitelist": whitelist})

        for cleaned in desired_normalized:
            self.request_json(
                "POST",
                "/control/filtering/add_url",
                {"name": cleaned["name"], "url": cleaned["url"], "whitelist": whitelist},
            )
            if cleaned["enabled"] is False:
                self.request_json(
                    "POST",
                    "/control/filtering/set_url",
                    {
                        "url": cleaned["url"],
                        "whitelist": whitelist,
                        "data": cleaned,
                    },
                )
        if current_urls or desired:
            self.request_json("POST", "/control/filtering/refresh", {"whitelist": whitelist})

    def set_rules(self, rules: list[str]) -> None:
        self.request_json("POST", "/control/filtering/set_rules", {"rules": list(rules)})


def apply_policy_to_live_adguard(config: Config, policy: dict[str, Any]) -> None:
    client = AdGuardApiClient(config)
    client.login()
    status = client.get_filtering_status()
    current_filters = status.get("filters", [])
    current_whitelist_filters = status.get("whitelist_filters", [])
    current_rules = status.get("user_rules", [])
    if (
        not isinstance(current_filters, list)
        or not isinstance(current_whitelist_filters, list)
        or not isinstance(current_rules, list)
    ):
        raise PolicyError("AdGuard filtering status returned malformed filtering data")
    client.replace_filter_list(
        current=[require_mapping(entry, label="filter status entry") for entry in current_filters],
        desired=policy["filters"],
        whitelist=False,
    )
    client.replace_filter_list(
        current=[require_mapping(entry, label="whitelist status entry") for entry in current_whitelist_filters],
        desired=policy["whitelist_filters"],
        whitelist=True,
    )
    if current_rules != policy["user_rules"]:
        client.set_rules(policy["user_rules"])


def live_policy(config: Config) -> dict[str, Any]:
    if not config.rendered_config_file.exists():
        raise FileNotFoundError(config.rendered_config_file)
    rendered_config = load_yaml_file(config.rendered_config_file)
    return extract_policy_from_config(rendered_config)


def read_stable_live_policy(config: Config) -> tuple[dict[str, Any], str]:
    last_policy: dict[str, Any] | None = None
    last_hash = ""
    last_error = ""
    for attempt in range(1, config.stable_read_attempts + 1):
        try:
            policy = live_policy(config)
        except FileNotFoundError:
            raise
        except PolicyError as exc:
            last_error = str(exc)
            if attempt < config.stable_read_attempts:
                time.sleep(config.stable_read_delay_seconds)
                continue
            raise TransientPolicyReadError(last_error) from exc
        except Exception as exc:
            last_error = str(exc)
            if attempt < config.stable_read_attempts:
                time.sleep(config.stable_read_delay_seconds)
                continue
            raise TransientPolicyReadError(last_error) from exc

        current_hash = policy_hash(policy)
        if last_policy is not None and current_hash == last_hash:
            return policy, current_hash
        last_policy = policy
        last_hash = current_hash
        if attempt < config.stable_read_attempts:
            time.sleep(config.stable_read_delay_seconds)

    if last_hash:
        raise TransientPolicyReadError("live AdGuard policy changed while being read; retrying")
    raise TransientPolicyReadError(last_error or "live AdGuard policy is not stable yet")


def command_bootstrap(args: argparse.Namespace, config: Config) -> int:
    source_path = Path(args.source or config.source_config_file)
    base_path = Path(args.base_out or config.base_config_file)
    policy_path = Path(args.policy_out or "policy.yaml")
    source_config = load_yaml_file(source_path)
    write_yaml_atomic(base_path, strip_policy_from_config(source_config))
    write_text_atomic(policy_path, policy_to_yaml_text(extract_policy_from_config(source_config)))
    return 0


def command_deploy_sync(_: argparse.Namespace, config: Config) -> int:
    validated = validate_runtime(config, operation="deploy-sync")
    with mutation_lock(config, operation="deploy-sync"):
        require_clean_drift(config)
        policy = validated["policy"]
        merged_source = render_source_from_policy(config, policy)
        record_published_policy_snapshot(config, policy)
        record_generated_source_snapshot(config, merged_source)
        update_state_from_existing(config, policy_hash_value=policy_hash(policy), source="deploy-sync")
        write_health(
            config,
            mode="sync",
            live_policy_hash=policy_hash(policy),
            message="deploy-sync complete",
        )
    return 0


def command_publish_current(_: argparse.Namespace, config: Config) -> int:
    validate_runtime(config, operation="publish-current")
    policy, stable_hash = read_stable_live_policy(config)
    record_live_policy_snapshot(config, policy, policy_hash_value=stable_hash)
    with mutation_lock(config, operation="publish-current"):
        publish_policy(config, policy, source="publish-current")
    return 0


def command_restore_published(_: argparse.Namespace, config: Config) -> int:
    validated = validate_runtime(config, operation="restore-published")
    with mutation_lock(config, operation="restore-published"):
        policy = validated["policy"]
        apply_policy_to_live_adguard(config, policy)
        merged_source = render_source_from_policy(config, policy)
        record_published_policy_snapshot(config, policy)
        record_generated_source_snapshot(config, merged_source)
        clear_drift(config)
        update_state_from_existing(config, policy_hash_value=policy_hash(policy), source="restore-published")
        write_health(
            config,
            mode="clean",
            live_policy_hash=policy_hash(policy),
            last_stable_live_policy_hash=policy_hash(policy),
            message="restore-published complete",
        )
    return 0


def command_watch(args: argparse.Namespace, config: Config) -> int:
    validate_runtime(config, operation="watch")
    ensure_bootstrap_ready(config)
    interval_seconds = max(1, int(args.interval_seconds))
    max_iterations = int(args.max_iterations) if args.max_iterations is not None else None
    iteration = 0
    while True:
        iteration += 1
        try:
            policy, live_hash = read_stable_live_policy(config)
            record_live_policy_snapshot(config, policy, policy_hash_value=live_hash)
            drift = read_drift(config)
            if drift.get("active"):
                write_health(
                    config,
                    mode="drift",
                    live_policy_hash=live_hash,
                    last_stable_live_policy_hash=live_hash,
                    message="publisher frozen in drift",
                )
            else:
                state = read_state(config)
                published_hash = str(state.get("last_published_hash", "")).strip()
                if live_hash == published_hash:
                    write_health(
                        config,
                        mode="clean",
                        live_policy_hash=live_hash,
                        last_stable_live_policy_hash=live_hash,
                        message="no policy change",
                    )
                else:
                    try:
                        with mutation_lock(config, operation="watch"):
                            publish_policy(config, policy, source="watch")
                    except LockError as exc:
                        write_health(
                            config,
                            mode="retrying",
                            live_policy_hash=live_hash,
                            last_stable_live_policy_hash=live_hash,
                            message=str(exc),
                            failure_category=exc.category,
                        )
                    except Exception as exc:
                        set_drift(config, failed_hash=live_hash, error=str(exc))
                        write_health(
                            config,
                            mode="drift",
                            live_policy_hash=live_hash,
                            last_stable_live_policy_hash=live_hash,
                            message=str(exc),
                            failure_category=getattr(exc, "category", "error"),
                        )
        except FileNotFoundError:
            write_health(config, mode="waiting", message="live AdGuard config not available yet")
        except TransientPolicyReadError as exc:
            write_health(
                config,
                mode="retrying",
                last_stable_live_policy_hash="",
                message=str(exc),
                failure_category=exc.category,
            )
        except Exception as exc:
            write_health(
                config,
                mode="error",
                message=str(exc),
                failure_category=getattr(exc, "category", "error"),
            )
            if args.fail_fast:
                raise
        if max_iterations is not None and iteration >= max_iterations:
            return 0
        time.sleep(interval_seconds)


def command_healthcheck(args: argparse.Namespace, config: Config) -> int:
    drift = read_drift(config)
    if drift.get("active"):
        print("drift active", file=sys.stderr)
        return 1
    if not config.health_file.exists():
        print("missing heartbeat", file=sys.stderr)
        return 1
    health = read_json_file(config.health_file, default={})
    heartbeat_raw = str(health.get("heartbeat_timestamp", "")).strip()
    if not heartbeat_raw:
        print("missing heartbeat timestamp", file=sys.stderr)
        return 1
    try:
        heartbeat = dt.datetime.fromisoformat(heartbeat_raw.replace("Z", "+00:00"))
    except ValueError:
        print("invalid heartbeat timestamp", file=sys.stderr)
        return 1
    age_seconds = (utc_now() - heartbeat).total_seconds()
    if age_seconds > float(args.max_age_seconds):
        print(f"stale heartbeat ({age_seconds:.1f}s)", file=sys.stderr)
        return 1
    mode = str(health.get("mode", "")).strip()
    if mode == "error":
        print(str(health.get("message", "publisher error")).strip() or "publisher error", file=sys.stderr)
        return 1
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Arbuzas AdGuard policy publisher helper")
    subparsers = parser.add_subparsers(dest="command", required=True)

    bootstrap = subparsers.add_parser("bootstrap", help="split source config into private base and public policy")
    bootstrap.add_argument("--source", help="override source config path")
    bootstrap.add_argument("--base-out", help="override base config output path")
    bootstrap.add_argument("--policy-out", help="override policy output path")
    bootstrap.set_defaults(handler=command_bootstrap)

    deploy_sync = subparsers.add_parser("deploy-sync", help="pull published policy and regenerate source config")
    deploy_sync.set_defaults(handler=command_deploy_sync)

    publish_current = subparsers.add_parser("publish-current", help="publish the current live AdGuard policy")
    publish_current.set_defaults(handler=command_publish_current)

    restore_published = subparsers.add_parser(
        "restore-published",
        help="reapply the published GitHub policy back into live AdGuard",
    )
    restore_published.set_defaults(handler=command_restore_published)

    watch = subparsers.add_parser("watch", help="watch live AdGuard config and publish policy changes")
    watch.add_argument("--interval-seconds", default="5", help="poll interval in seconds")
    watch.add_argument("--max-iterations", help="stop after N iterations (test helper)")
    watch.add_argument("--fail-fast", action="store_true", help="raise helper exceptions instead of swallowing them")
    watch.set_defaults(handler=command_watch)

    healthcheck = subparsers.add_parser("healthcheck", help="verify publisher heartbeat and drift state")
    healthcheck.add_argument("--max-age-seconds", default="20", help="maximum allowed heartbeat age")
    healthcheck.set_defaults(handler=command_healthcheck)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    config = Config.from_env()
    try:
        return int(args.handler(args, config))
    except PolicyError as exc:
        safe_write_health(
            config,
            mode="error",
            message=str(exc),
            failure_category=exc.category,
        )
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
