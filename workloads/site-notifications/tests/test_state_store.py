from pathlib import Path

import pytest

from state_store import StateStore


def test_default_state_and_on_off_transition(tmp_path: Path):
    path = tmp_path / "state.json"
    store = StateStore(path)

    state = store.load()
    assert state["enabled"] is False
    assert state["paused_reason"] == "manual_off"
    assert state["last_unread"] is None
    assert state["last_unread_message_fingerprints"] == []
    assert state["last_unread_previews"] == []
    assert state["last_latest_unread_preview_text"] is None
    assert state["resolved_check_url"] is None
    assert state["route_discovery_last_ts"] is None
    assert state["route_discovery_last_result"] is None
    assert state["route_discovery_last_candidates"] is None
    assert state["resolved_preview_url"] is None
    assert state["preview_route_discovery_last_ts"] is None
    assert state["preview_route_discovery_last_result"] is None
    assert state["preview_route_discovery_last_candidates"] is None
    assert state["last_preview_fetch_ts"] is None
    assert state["last_preview_route"] is None
    assert state["last_preview_route_result"] is None
    assert state["last_preview_unread_count"] is None
    assert state["last_preview_error_message"] is None
    assert state["last_preview_source"] == "none"
    assert state["last_preview_item_count"] == 0
    assert state["last_notification_open_url"] is None
    assert state["last_notification_had_link_preview_requested"] is None
    assert state["runtime_selinux_context"] is None
    assert state["runtime_context_warning"] is None
    assert state["command_queue_depth"] == 0
    assert state["command_latency_histogram_ms"] == {}
    assert state["telegram_getupdates_latency_ms"] is None
    assert state["telegram_send_latency_ms"] is None
    assert state["last_telegram_api_error_code"] is None
    assert state["fetch_duration_ms"] is None
    assert state["parse_duration_ms"] is None

    state = store.patch({"enabled": True, "paused_reason": "none"})
    assert state["enabled"] is True
    assert state["paused_reason"] == "none"

    state = store.patch({"enabled": False, "paused_reason": "manual_off"})
    assert state["enabled"] is False
    assert state["paused_reason"] == "manual_off"


def test_load_recovers_zero_filled_state_file(tmp_path: Path):
    path = tmp_path / "state.json"
    raw_bytes = b"\0" * 32
    path.write_bytes(raw_bytes)
    store = StateStore(path)

    state = store.load()

    assert state["enabled"] is False
    assert state["last_watchdog_reason"] == "state_recovered"
    assert "Recovered corrupt state" in str(state["last_error_message"])
    corrupt_paths = sorted(tmp_path.glob("state.json.corrupt.*"))
    assert len(corrupt_paths) == 1
    assert corrupt_paths[0].read_bytes() == raw_bytes
    assert path.read_text(encoding="utf-8").strip().startswith("{")


def test_load_recovers_invalid_json_state_file(tmp_path: Path):
    path = tmp_path / "state.json"
    raw = b'{"enabled": true'
    path.write_bytes(raw)
    store = StateStore(path)

    state = store.load()

    assert state["last_watchdog_reason"] == "state_recovered"
    corrupt_paths = sorted(tmp_path.glob("state.json.corrupt.*"))
    assert len(corrupt_paths) == 1
    assert corrupt_paths[0].read_bytes() == raw


def test_load_recovers_when_state_file_cannot_be_read(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    path = tmp_path / "state.json"
    raw = b'{"enabled": true, "paused_reason": "none"}'
    path.write_bytes(raw)
    store = StateStore(path)
    original_read_bytes = Path.read_bytes

    def fake_read_bytes(self: Path) -> bytes:
        if self == path:
            raise PermissionError("simulated permission failure")
        return original_read_bytes(self)

    monkeypatch.setattr(Path, "read_bytes", fake_read_bytes)

    state = store.load()

    assert state["last_watchdog_reason"] == "state_recovered"
    corrupt_paths = sorted(tmp_path.glob("state.json.corrupt.*"))
    assert len(corrupt_paths) == 1
    assert corrupt_paths[0].read_bytes() == raw
