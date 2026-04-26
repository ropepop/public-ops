from __future__ import annotations

from datetime import datetime, timedelta, timezone
from pathlib import Path
import socket

import pytest

from app import (
    _parse_static_dns_fallbacks,
    _increment_telegram_poll_error,
    DAEMON_EXIT_HEARTBEAT_STALE,
    DAEMON_EXIT_LOCK_HELD,
    DAEMON_EXIT_ORCHESTRATOR_OWNERSHIP_REQUIRED,
    DAEMON_EXIT_THREAD_DIED,
    build_runtime_context_warning,
    evaluate_watchdog,
    is_orchestrator_owned_runtime,
    run_daemon,
    run_diag_telegram,
    run_healthcheck,
    install_static_dns_fallbacks,
)
from config import Config
from process_lock import ProcessLock
from state_store import StateStore
from telegram_control import TelegramApiError


def _make_config(tmp_path: Path) -> Config:
    return Config(
        telegram_bot_token="token",
        telegram_chat_id=111,
        gribu_base_url="https://www.gribu.lv",
        gribu_check_url="/lv/messages",
        gribu_check_url_candidates=("/lv/messages",),
        gribu_preview_url="/lv/messages",
        gribu_preview_url_candidates=("/lv/messages",),
        gribu_login_id="demo@example.com",
        gribu_login_password="secret",
        gribu_login_path="/pieslegties",
        gribu_cookie_header="",
        check_interval_sec=60,
        check_interval_fast_sec=20,
        check_interval_idle_sec=60,
        check_interval_error_backoff_max_sec=180,
        state_file=(tmp_path / "state.json"),
        http_timeout_sec=5,
        error_alert_cooldown_sec=1800,
        telegram_api_base_url="https://api.telegram.org",
        telegram_nav_buttons_enabled=True,
        env_file_path=(tmp_path / ".env"),
        daemon_lock_file=(tmp_path / "daemon.lock"),
        watchdog_check_sec=10,
        watchdog_stale_sec=120,
        supervisor_restart_base_sec=2,
        supervisor_restart_max_sec=30,
        parse_low_confidence_delta_limit=20,
        route_discovery_ttl_sec=21600,
        preview_route_discovery_ttl_sec=21600,
        parse_min_confidence_baseline=0.8,
        parse_min_confidence_update=0.7,
        parse_min_confidence_route_selection=0.7,
    )


def test_run_daemon_exits_when_lock_already_held(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setenv("RUNTIME_CONTEXT_POLICY", "managed_service")
    config = _make_config(tmp_path)
    lock = ProcessLock(config.daemon_lock_file)
    lock.acquire()
    try:
        with pytest.raises(SystemExit) as exc:
            run_daemon(config)
        assert exc.value.code == DAEMON_EXIT_LOCK_HELD
    finally:
        lock.release()


def test_is_orchestrator_owned_runtime_accepts_managed_policy():
    owned, reason = is_orchestrator_owned_runtime(runtime_context_policy="managed_service")
    assert owned is True
    assert reason == "managed_runtime_confirmed"


def test_run_daemon_rejects_when_policy_missing(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.delenv("RUNTIME_CONTEXT_POLICY", raising=False)
    config = _make_config(tmp_path)
    with pytest.raises(SystemExit) as exc:
        run_daemon(config)
    assert exc.value.code == DAEMON_EXIT_ORCHESTRATOR_OWNERSHIP_REQUIRED


def test_run_daemon_rejects_when_policy_not_orchestrator(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
):
    monkeypatch.setenv("RUNTIME_CONTEXT_POLICY", "manual")
    config = _make_config(tmp_path)
    with pytest.raises(SystemExit) as exc:
        run_daemon(config)
    assert exc.value.code == DAEMON_EXIT_ORCHESTRATOR_OWNERSHIP_REQUIRED


def test_watchdog_detects_dead_worker_thread():
    now_dt = datetime.now(timezone.utc)
    exit_code, reason = evaluate_watchdog(
        now_dt=now_dt,
        stale_sec=120,
        thread_alive={"telegram": False, "scheduler": True, "command_worker": True},
        heartbeats={
            "telegram_last_heartbeat_ts": now_dt.isoformat(),
            "scheduler_last_heartbeat_ts": now_dt.isoformat(),
            "command_worker_last_heartbeat_ts": now_dt.isoformat(),
        },
    )
    assert exit_code == DAEMON_EXIT_THREAD_DIED
    assert reason == "telegram_thread_dead"


def test_watchdog_detects_stale_heartbeat():
    now_dt = datetime.now(timezone.utc)
    stale_ts = (now_dt - timedelta(seconds=121)).isoformat()
    exit_code, reason = evaluate_watchdog(
        now_dt=now_dt,
        stale_sec=120,
        thread_alive={"telegram": True, "scheduler": True, "command_worker": True},
        heartbeats={
            "telegram_last_heartbeat_ts": stale_ts,
            "scheduler_last_heartbeat_ts": now_dt.isoformat(),
            "command_worker_last_heartbeat_ts": now_dt.isoformat(),
        },
    )
    assert exit_code == DAEMON_EXIT_HEARTBEAT_STALE
    assert reason.startswith("telegram_heartbeat_stale")


def test_healthcheck_fails_for_uninitialized_state(tmp_path: Path, capsys):
    config = _make_config(tmp_path)
    assert run_healthcheck(config) == 1
    captured = capsys.readouterr().out
    assert "unhealthy" in captured


def test_healthcheck_stays_unhealthy_after_corrupt_state_recovery(tmp_path: Path, capsys):
    config = _make_config(tmp_path)
    config.state_file.write_bytes(b"\0" * 24)

    assert run_healthcheck(config) == 1

    captured = capsys.readouterr().out
    assert "daemon_not_initialized" in captured
    corrupt_paths = sorted(tmp_path.glob("state.json.corrupt.*"))
    assert len(corrupt_paths) == 1


def test_healthcheck_fails_for_stale_heartbeat(tmp_path: Path, capsys):
    config = _make_config(tmp_path)
    state_store = StateStore(config.state_file)
    stale_ts = (datetime.now(timezone.utc) - timedelta(seconds=121)).replace(microsecond=0).isoformat()
    state_store.patch(
        {
            "daemon_started_ts": stale_ts,
            "daemon_last_heartbeat_ts": stale_ts,
            "telegram_last_heartbeat_ts": stale_ts,
            "scheduler_last_heartbeat_ts": stale_ts,
            "command_worker_last_heartbeat_ts": stale_ts,
        }
    )
    assert run_healthcheck(config) == 1
    captured = capsys.readouterr().out
    assert "heartbeat_stale" in captured


def test_runtime_context_warning_detects_magisk_context():
    warning = build_runtime_context_warning("u:r:magisk:s0")
    assert warning is not None
    assert "managed Arbuzas service" in warning


def test_runtime_context_warning_not_set_for_normal_context():
    assert build_runtime_context_warning("u:r:untrusted_app:s0:c123,c456") is None


def test_runtime_context_warning_disabled_for_managed_policy():
    assert (
        build_runtime_context_warning(
            "u:r:magisk:s0",
            runtime_context_policy="managed_service",
        )
        is None
    )


def test_increment_telegram_poll_error_records_error_code(tmp_path: Path):
    config = _make_config(tmp_path)
    state_store = StateStore(config.state_file)
    error = TelegramApiError("too many requests", error_code=429, method="getUpdates")
    _increment_telegram_poll_error(state_store, error)
    state = state_store.load()
    assert state["telegram_poll_error_count"] == 1
    assert state["last_telegram_api_error_code"] == 429


def test_run_diag_telegram_prints_report(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys):
    config = _make_config(tmp_path)
    monkeypatch.setattr("app.TelegramClient.get_me", lambda _self: {"id": 123, "username": "bot"})
    monkeypatch.setattr(
        "app.TelegramClient.get_webhook_info",
        lambda _self: {"url": "", "pending_update_count": 0},
    )

    assert run_diag_telegram(config) == 0
    output = capsys.readouterr().out
    assert '"get_me"' in output
    assert '"get_webhook_info"' in output


def test_parse_static_dns_fallbacks():
    parsed = _parse_static_dns_fallbacks("api.telegram.org=149.154.166.110,149.154.167.220;foo=1.2.3.4")
    assert parsed["api.telegram.org"] == ["149.154.166.110", "149.154.167.220"]
    assert parsed["foo"] == ["1.2.3.4"]


def test_install_static_dns_fallbacks_uses_fallback(monkeypatch: pytest.MonkeyPatch):
    calls: list[str] = []

    def fake_getaddrinfo(host, *args, **kwargs):
        calls.append(str(host))
        if str(host) == "api.telegram.org":
            raise socket.gaierror("dns fail")
        if str(host) == "149.154.166.110":
            return [("ok",)]
        raise socket.gaierror("unexpected")

    monkeypatch.setenv("SITE_NOTIFIER_STATIC_DNS_FALLBACKS", "api.telegram.org=149.154.166.110")
    monkeypatch.setattr("app._DNS_FALLBACK_INSTALLED", False)
    monkeypatch.setattr("app.socket.getaddrinfo", fake_getaddrinfo)

    install_static_dns_fallbacks()
    import app

    result = app.socket.getaddrinfo("api.telegram.org", 443)
    assert result == [("ok",)]
    assert "149.154.166.110" in calls
