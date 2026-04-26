from __future__ import annotations

import json
import logging
import os
from contextlib import contextmanager
from copy import deepcopy
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

import fcntl


DEFAULT_STATE: dict[str, Any] = {
    "enabled": False,
    "last_unread": None,
    "last_unread_message_fingerprints": [],
    "last_unread_previews": [],
    "last_latest_unread_preview_text": None,
    "last_check_ts": None,
    "last_success_ts": None,
    "last_check_result": None,
    "last_error_message": None,
    "last_parse_source": None,
    "last_parse_confidence": None,
    "low_confidence_streak": 0,
    "consecutive_errors": 0,
    "paused_reason": "manual_off",
    "last_error_alert_ts": None,
    "telegram_update_offset": None,
    "telegram_poll_error_count": 0,
    "daemon_started_ts": None,
    "runtime_selinux_context": None,
    "runtime_context_warning": None,
    "daemon_last_heartbeat_ts": None,
    "telegram_last_heartbeat_ts": None,
    "scheduler_last_heartbeat_ts": None,
    "command_worker_last_heartbeat_ts": None,
    "last_watchdog_reason": None,
    "daemon_restart_count": 0,
    "last_restart_ts": None,
    "current_check_interval_sec": None,
    "next_check_due_ts": None,
    "resolved_check_url": None,
    "route_discovery_last_ts": None,
    "route_discovery_last_result": None,
    "route_discovery_last_candidates": None,
    "resolved_preview_url": None,
    "preview_route_discovery_last_ts": None,
    "preview_route_discovery_last_result": None,
    "preview_route_discovery_last_candidates": None,
    "last_preview_fetch_ts": None,
    "last_preview_route": None,
    "last_preview_route_result": None,
    "last_preview_unread_count": None,
    "last_preview_error_message": None,
    "last_preview_source": "none",
    "last_preview_item_count": 0,
    "last_notification_sent_ts": None,
    "last_notification_open_url": None,
    "last_notification_had_link_preview_requested": None,
    "last_command_latency_ms": None,
    "command_queue_depth": 0,
    "command_latency_histogram_ms": {},
    "telegram_getupdates_latency_ms": None,
    "telegram_send_latency_ms": None,
    "last_telegram_api_error_code": None,
    "fetch_duration_ms": None,
    "parse_duration_ms": None,
    "check_duration_ms": None,
}

LOG = logging.getLogger(__name__)


class StateStore:
    def __init__(self, path: Path):
        self.path = path
        self.lock_path = path.with_suffix(path.suffix + ".lock")

    @contextmanager
    def _locked(self):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        lock_fd = os.open(self.lock_path, os.O_CREAT | os.O_RDWR, 0o600)
        with os.fdopen(lock_fd, "r+") as lock_file:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
            try:
                yield
            finally:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)

    def _read_unlocked(self) -> dict[str, Any]:
        if not self.path.exists():
            return deepcopy(DEFAULT_STATE)
        try:
            raw_bytes = self.path.read_bytes()
        except OSError as exc:
            return self._recover_corrupt_state(raw_bytes=None, reason=f"{exc.__class__.__name__}: {exc}")

        try:
            data = json.loads(raw_bytes.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError, ValueError, TypeError) as exc:
            return self._recover_corrupt_state(raw_bytes=raw_bytes, reason=f"{exc.__class__.__name__}: {exc}")

        if not isinstance(data, dict):
            return self._recover_corrupt_state(
                raw_bytes=raw_bytes,
                reason=f"ValueError: expected top-level JSON object, got {type(data).__name__}",
            )

        merged = deepcopy(DEFAULT_STATE)
        merged.update(data)
        return merged

    def _recover_corrupt_state(self, *, raw_bytes: bytes | None, reason: str) -> dict[str, Any]:
        corrupt_path = self._next_corrupt_path()
        preserved = False

        if self.path.exists():
            try:
                os.replace(self.path, corrupt_path)
                preserved = True
            except OSError:
                if raw_bytes is not None:
                    try:
                        corrupt_path.write_bytes(raw_bytes)
                        self.path.unlink(missing_ok=True)
                        preserved = True
                    except OSError:
                        preserved = False

        recovered = deepcopy(DEFAULT_STATE)
        recovered["last_watchdog_reason"] = "state_recovered"
        recovered["last_error_message"] = (
            f"Recovered corrupt state ({reason})"
            if not preserved
            else f"Recovered corrupt state ({reason}); preserved={corrupt_path.name}"
        )
        self._write_unlocked(recovered)
        LOG.warning(
            "Recovered corrupt notifier state at %s; preserved=%s; reason=%s",
            self.path,
            corrupt_path.name if preserved else "no",
            reason,
        )
        return recovered

    def _next_corrupt_path(self) -> Path:
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        candidate = self.path.with_name(f"{self.path.name}.corrupt.{stamp}")
        suffix = 1
        while candidate.exists():
            candidate = self.path.with_name(f"{self.path.name}.corrupt.{stamp}.{suffix}")
            suffix += 1
        return candidate

    def _write_unlocked(self, state: dict[str, Any]) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        temp_path = self.path.with_suffix(self.path.suffix + ".tmp")
        with temp_path.open("w", encoding="utf-8") as fh:
            json.dump(state, fh, indent=2, sort_keys=True)
            fh.write("\n")
        os.replace(temp_path, self.path)

    def load(self) -> dict[str, Any]:
        with self._locked():
            return self._read_unlocked()

    def save(self, state: dict[str, Any]) -> dict[str, Any]:
        with self._locked():
            merged = deepcopy(DEFAULT_STATE)
            merged.update(state)
            self._write_unlocked(merged)
            return merged

    def patch(self, values: dict[str, Any]) -> dict[str, Any]:
        with self._locked():
            state = self._read_unlocked()
            state.update(values)
            self._write_unlocked(state)
            return state

    def mutate(self, mutator: Callable[[dict[str, Any]], dict[str, Any] | None]) -> dict[str, Any]:
        with self._locked():
            state = self._read_unlocked()
            maybe_new_state = mutator(deepcopy(state))
            if maybe_new_state is not None:
                state = maybe_new_state
            self._write_unlocked(state)
            return state
