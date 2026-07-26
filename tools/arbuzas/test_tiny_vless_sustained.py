#!/usr/bin/env python3
"""Secret-safe sustained acceptance for the seven-profile VPN subscription.

The runner intentionally accepts private share links only on stdin or through a
caller-owned regular file with mode 0600.  Links are parsed by
``test_tiny_vless_mobility`` and are never placed in argv, the environment, or
reported output.  Every client configuration and log lives in a mode-0700
temporary workspace that is removed on success, failure, or signal.

Provider endpoints are public, non-secret test infrastructure supplied through
a JSON manifest.  The manifest must contain at least three independent HTTPS
probe, download, and upload providers; at least two in each class must pass a
bounded direct preflight.  A download URL template contains exactly one
``{bytes}`` placeholder.

The external monitor command is supplied as a JSON argv array in a non-secret
file.  Its line protocol is deliberately small and safe:

* ``READY`` once startup checks have passed;
* ``SAMPLE`` once per second while the runner is active;
* ``ABORT <safe-code>`` to stop all client traffic immediately.

Equivalent JSON messages with ``event`` set to ``ready``, ``sample``, or
``abort`` are also accepted.  Raw monitor lines are never echoed.
"""

from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, replace
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import string
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any, Callable, Iterable, Sequence
from urllib.parse import urlsplit

from test_tiny_vless_mobility import (
    TestError as MobilityTestError,
    client_config,
    karing_client_config,
    parse_link,
    unused_local_port,
    wait_for_listener,
)


class RunnerError(RuntimeError):
    """A failure with a fixed, non-sensitive public error code."""

    def __init__(self, code: str):
        if not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,63}", code):
            code = "internal_error"
        super().__init__(code)
        self.code = code


EXPECTED_LABELS = (
    "mobility-hysteria2",
    "original-vless-reality",
    "mobility-vless-xhttp-h3",
    "mobility-wireguard",
    "mobility-vless-xhttp-h2",
    "mobility-vmess-mkcp",
    "karing-singbox-reality-compat",
)
STAGE_KINDS = ("stream", "download", "upload")
PROFILE_COUNT = len(EXPECTED_LABELS)
STAGE_COUNT = PROFILE_COUNT * len(STAGE_KINDS)
KARING_LABEL = "karing-singbox-reality-compat"
XRAY_VERSION = "26.7.11"
KARING_VERSION = "1.12.22.2502"
KARING_REVISION = "9a4babea1056b2ee190d14c28302f5a9fa78b762"

MAX_PRIVATE_INPUT = 128 * 1024
MAX_MANIFEST_INPUT = 256 * 1024
MIN_PROVIDER_ENTRIES = 3
MIN_QUALIFIED_PROVIDERS = 2
MIN_PAYLOAD_SECONDS = 60.0
MAX_CHUNKED_PAYLOAD_SECONDS = 120.0

MIB = 1024 * 1024
STREAM_WORKERS = 2
STREAM_BYTES_PER_WORKER = 36 * MIB
STREAM_RATE_PER_WORKER = 512 * 1024
TRANSFER_WORKERS = 4
TRANSFER_CHUNKS_PER_WORKER = 18
TRANSFER_CHUNK_BYTES = 4 * MIB
TRANSFER_RATE_PER_WORKER = MIB
PREFLIGHT_BYTES = 64 * 1024

CURL_METRIC_PREFIX = "__tiny_vless_metrics__"
CURL_WRITE_OUT = (
    CURL_METRIC_PREFIX
    + "\t%{http_code}\t%{size_download}\t%{size_upload}"
    + "\t%{time_pretransfer}\t%{time_starttransfer}\t%{time_total}"
    + "\t%{speed_download}\t%{speed_upload}"
)
SAFE_NAME = re.compile(r"[a-z0-9][a-z0-9_-]{0,47}")


@dataclass(frozen=True)
class Provider:
    kind: str
    name: str
    independence_key: str
    url: str
    statuses: tuple[int, ...]
    range_request: bool = False

    def request_url(self, byte_count: int = 0) -> str:
        if self.kind == "download" and not self.range_request:
            try:
                return self.url.format(bytes=byte_count)
            except (KeyError, ValueError) as exc:
                raise RunnerError("provider_manifest_invalid") from exc
        return self.url


@dataclass(frozen=True)
class ProviderPools:
    probe: tuple[Provider, ...]
    download: tuple[Provider, ...]
    upload: tuple[Provider, ...]


@dataclass(frozen=True)
class CurlMetrics:
    status: int
    downloaded: int
    uploaded: int
    pretransfer: float
    starttransfer: float
    total: float
    speed_download: float
    speed_upload: float


@dataclass(frozen=True)
class RequestResult:
    first_payload: float
    last_payload: float
    successful_bytes: int
    attempted_bytes: int
    provider_key: str


@dataclass(frozen=True)
class WorkerResult:
    first_payload: float
    last_payload: float
    successful_bytes: int
    attempted_bytes: int
    provider_keys: frozenset[str]


@dataclass(frozen=True)
class StageResult:
    kind: str
    first_payload: float
    last_payload: float
    successful_bytes: int
    attempted_bytes: int
    provider_count: int
    worker_count: int

    @property
    def duration(self) -> float:
        return self.last_payload - self.first_payload

    @property
    def rate_mbps(self) -> float:
        if self.duration <= 0:
            return 0.0
        return self.successful_bytes * 8 / self.duration / 1_000_000


class ProcessRegistry:
    """Own every child process and terminate whole process groups on abort."""

    def __init__(self, cancel: threading.Event):
        self.cancel = cancel
        self._lock = threading.Lock()
        self._processes: set[subprocess.Popen[Any]] = set()

    def add(self, process: subprocess.Popen[Any]) -> None:
        with self._lock:
            self._processes.add(process)

    def discard(self, process: subprocess.Popen[Any]) -> None:
        with self._lock:
            self._processes.discard(process)

    @staticmethod
    def terminate(process: subprocess.Popen[Any]) -> None:
        if process.poll() is not None:
            return
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except (ProcessLookupError, PermissionError):
            try:
                process.terminate()
            except ProcessLookupError:
                return
        try:
            process.wait(timeout=3)
            return
        except subprocess.TimeoutExpired:
            pass
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            try:
                process.kill()
            except ProcessLookupError:
                return
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            pass

    def terminate_all(self) -> None:
        self.cancel.set()
        with self._lock:
            processes = list(self._processes)
        for process in processes:
            self.terminate(process)
            self.discard(process)

    def empty(self) -> bool:
        with self._lock:
            return not self._processes


class PrivateWorkspace:
    def __init__(self):
        self.path: Path | None = None

    def __enter__(self) -> Path:
        self.path = Path(tempfile.mkdtemp(prefix="tiny-vless-sustained-"))
        os.chmod(self.path, 0o700)
        return self.path

    def cleanup(self) -> bool:
        if self.path is None:
            return True
        path = self.path
        if not path.name.startswith("tiny-vless-sustained-"):
            return False
        shutil.rmtree(path, ignore_errors=False)
        return not path.exists()

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.cleanup()


def safe_child_env(workspace: Path) -> dict[str, str]:
    return {
        "HOME": str(workspace),
        "LANG": "C",
        "LC_ALL": "C",
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "TMPDIR": str(workspace),
    }


def monitor_child_env() -> dict[str, str]:
    result = {
        "HOME": os.environ.get("HOME", "/nonexistent"),
        "LANG": "C",
        "LC_ALL": "C",
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
    }
    # SSH agent forwarding may be needed by a read-only remote monitor.  No
    # profile or subscription material is ever placed in this environment.
    if os.environ.get("SSH_AUTH_SOCK"):
        result["SSH_AUTH_SOCK"] = os.environ["SSH_AUTH_SOCK"]
    return result


def read_bounded(path: Path, maximum: int, code: str) -> bytes:
    try:
        info = path.lstat()
    except OSError as exc:
        raise RunnerError(code) from exc
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise RunnerError(code)
    if info.st_size < 1 or info.st_size > maximum:
        raise RunnerError(code)
    try:
        return path.read_bytes()
    except OSError as exc:
        raise RunnerError(code) from exc


def load_private_links(source: str) -> list[str]:
    if source == "-":
        raw = sys.stdin.buffer.read(MAX_PRIVATE_INPUT + 1)
        if not raw or len(raw) > MAX_PRIVATE_INPUT:
            raise RunnerError("private_input_invalid")
    else:
        path = Path(source)
        try:
            info = path.lstat()
        except OSError as exc:
            raise RunnerError("private_input_invalid") from exc
        if (
            stat.S_ISLNK(info.st_mode)
            or not stat.S_ISREG(info.st_mode)
            or stat.S_IMODE(info.st_mode) != 0o600
            or info.st_uid != os.geteuid()
            or info.st_size < 1
            or info.st_size > MAX_PRIVATE_INPUT
        ):
            raise RunnerError("private_input_invalid")
        try:
            raw = path.read_bytes()
        except OSError as exc:
            raise RunnerError("private_input_invalid") from exc
    try:
        value = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RunnerError("private_input_invalid") from exc
    if (
        not isinstance(value, list)
        or len(value) != len(EXPECTED_LABELS)
        or any(not isinstance(item, str) or not item or len(item) > 8192 for item in value)
    ):
        raise RunnerError("profile_set_invalid")
    return list(value)


def validate_https_url(value: str, *, template: bool) -> None:
    candidate = value
    if template:
        formatter = string.Formatter()
        try:
            fields = [field for _, field, _, _ in formatter.parse(value) if field is not None]
        except ValueError as exc:
            raise RunnerError("provider_manifest_invalid") from exc
        if fields != ["bytes"]:
            raise RunnerError("provider_manifest_invalid")
        try:
            candidate = value.format(bytes=1)
        except (KeyError, ValueError) as exc:
            raise RunnerError("provider_manifest_invalid") from exc
    elif "{" in value or "}" in value:
        raise RunnerError("provider_manifest_invalid")
    if len(candidate) > 2048:
        raise RunnerError("provider_manifest_invalid")
    parsed = urlsplit(candidate)
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.fragment
    ):
        raise RunnerError("provider_manifest_invalid")


def parse_provider_list(value: object, kind: str) -> tuple[Provider, ...]:
    if not isinstance(value, list) or len(value) < MIN_PROVIDER_ENTRIES:
        raise RunnerError("provider_manifest_invalid")
    providers: list[Provider] = []
    names: set[str] = set()
    keys: set[str] = set()
    origins: set[tuple[str, str, int]] = set()
    for raw in value:
        if not isinstance(raw, dict):
            raise RunnerError("provider_manifest_invalid")
        name = raw.get("name")
        independence_key = raw.get("independence_key")
        range_request = kind == "download" and raw.get("range_bytes") is True
        if kind == "download":
            url = raw.get("url" if range_request else "url_template")
        else:
            url = raw.get("url")
        statuses_value = raw.get("expected_status", [200])
        if isinstance(statuses_value, int):
            statuses_value = [statuses_value]
        if (
            not isinstance(name, str)
            or not SAFE_NAME.fullmatch(name)
            or not isinstance(independence_key, str)
            or not SAFE_NAME.fullmatch(independence_key)
            or not isinstance(url, str)
            or not isinstance(statuses_value, list)
            or not statuses_value
            or any(not isinstance(item, int) or item < 200 or item > 299 for item in statuses_value)
        ):
            raise RunnerError("provider_manifest_invalid")
        if kind == "upload" and raw.get("acknowledges_full_body") is not True:
            raise RunnerError("provider_manifest_invalid")
        if name in names or independence_key in keys:
            raise RunnerError("provider_manifest_invalid")
        names.add(name)
        keys.add(independence_key)
        validate_https_url(url, template=kind == "download" and not range_request)
        origin_url = (
            url.format(bytes=1)
            if kind == "download" and not range_request
            else url
        )
        parsed_origin = urlsplit(origin_url)
        origin = (
            parsed_origin.scheme,
            str(parsed_origin.hostname).lower(),
            int(parsed_origin.port or 443),
        )
        if origin in origins:
            raise RunnerError("provider_manifest_invalid")
        origins.add(origin)
        providers.append(
            Provider(
                kind=kind,
                name=name,
                independence_key=independence_key,
                url=url,
                statuses=tuple(sorted(set(statuses_value))),
                range_request=range_request,
            )
        )
    return tuple(providers)


def parse_provider_manifest_value(value: object) -> ProviderPools:
    if not isinstance(value, dict) or value.get("version") != 1:
        raise RunnerError("provider_manifest_invalid")
    return ProviderPools(
        probe=parse_provider_list(value.get("probe"), "probe"),
        download=parse_provider_list(value.get("download"), "download"),
        upload=parse_provider_list(value.get("upload"), "upload"),
    )


def load_provider_manifest(path: Path) -> ProviderPools:
    raw = read_bounded(path, MAX_MANIFEST_INPUT, "provider_manifest_invalid")
    try:
        value = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RunnerError("provider_manifest_invalid") from exc
    return parse_provider_manifest_value(value)


def parse_monitor_command(path: Path) -> tuple[str, ...]:
    raw = read_bounded(path, 64 * 1024, "monitor_command_invalid")
    try:
        value = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RunnerError("monitor_command_invalid") from exc
    if isinstance(value, dict):
        value = value.get("argv")
    if (
        not isinstance(value, list)
        or not 1 <= len(value) <= 64
        or any(not isinstance(item, str) or not item or "\x00" in item or len(item) > 4096 for item in value)
    ):
        raise RunnerError("monitor_command_invalid")
    sensitive_words = ("subscription", "password", "passwd", "token", "secret", "uuid")
    for item in value:
        lowered = item.lower()
        if any(word in lowered for word in sensitive_words) or re.search(r"https?://[^/\s]*@", item):
            raise RunnerError("monitor_command_invalid")
    return tuple(value)


class ExternalMonitor:
    def __init__(
        self,
        argv: Sequence[str],
        registry: ProcessRegistry,
        cancel: threading.Event,
        workspace: Path,
    ):
        self.argv = tuple(argv)
        self.registry = registry
        self.cancel = cancel
        self.workspace = workspace
        self.process: subprocess.Popen[str] | None = None
        self._reader: threading.Thread | None = None
        self._ready = threading.Event()
        self._abort = threading.Event()
        self._closing = threading.Event()
        self._lock = threading.Lock()
        self._samples: list[float] = []
        self._last_message = 0.0
        self._stderr: Any = None

    @staticmethod
    def parse_line(line: str) -> tuple[str, str]:
        stripped = line.strip()
        if len(stripped) > 4096:
            return "invalid", ""
        if stripped == "READY":
            return "ready", ""
        if stripped == "SAMPLE":
            return "sample", ""
        if stripped.startswith("ABORT "):
            code = stripped[6:].strip()
            if SAFE_NAME.fullmatch(code):
                return "abort", code
            return "invalid", ""
        try:
            value = json.loads(stripped)
        except json.JSONDecodeError:
            return "invalid", ""
        if not isinstance(value, dict):
            return "invalid", ""
        event = value.get("event")
        if event == "ready":
            return "ready", ""
        if event == "sample" and value.get("ok", True) is True:
            return "sample", ""
        if event == "abort":
            code = value.get("code", "monitor_abort")
            if isinstance(code, str) and SAFE_NAME.fullmatch(code):
                return "abort", code
        return "invalid", ""

    def start(self) -> None:
        stderr_path = self.workspace / "monitor.stderr"
        self._stderr = stderr_path.open("wb")
        os.chmod(stderr_path, 0o600)
        try:
            self.process = subprocess.Popen(
                self.argv,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=self._stderr,
                text=True,
                bufsize=1,
                start_new_session=True,
                env=monitor_child_env(),
            )
        except OSError as exc:
            self._stderr.close()
            raise RunnerError("monitor_start_failed") from exc
        self.registry.add(self.process)
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()
        if not self._ready.wait(timeout=20):
            self._trigger_abort()
            raise RunnerError("monitor_not_ready")
        self.check()

    def _trigger_abort(self) -> None:
        self._abort.set()
        self.cancel.set()

    def _read_loop(self) -> None:
        assert self.process is not None and self.process.stdout is not None
        try:
            for line in self.process.stdout:
                now = time.monotonic()
                kind, _ = self.parse_line(line)
                with self._lock:
                    self._last_message = now
                    if kind == "sample":
                        self._samples.append(now)
                if kind == "ready":
                    self._ready.set()
                elif kind == "abort" or kind == "invalid":
                    self._trigger_abort()
                    break
        finally:
            if not self._closing.is_set():
                self._trigger_abort()

    def check(self) -> None:
        if self._abort.is_set() or self.cancel.is_set():
            raise RunnerError("monitor_abort")
        if self.process is None or self.process.poll() is not None:
            raise RunnerError("monitor_stopped")
        with self._lock:
            last = self._last_message
        if last and time.monotonic() - last > 3.5:
            self._trigger_abort()
            raise RunnerError("monitor_stale")

    def mark(self) -> float:
        self.check()
        return time.monotonic()

    def validate_window(self, started: float, ended: float) -> int:
        self.check()
        duration = ended - started
        with self._lock:
            samples = [item for item in self._samples if started <= item <= ended]
        required = max(1, math.floor(duration) - 3)
        if len(samples) < required:
            self._trigger_abort()
            raise RunnerError("monitor_sample_rate_failed")
        points = [started, *samples, ended]
        if max(b - a for a, b in zip(points, points[1:])) > 3.5:
            self._trigger_abort()
            raise RunnerError("monitor_sample_gap")
        return len(samples)

    def close(self) -> None:
        self._closing.set()
        if self.process is not None:
            self.registry.terminate(self.process)
            self.registry.discard(self.process)
        if self._reader is not None:
            self._reader.join(timeout=3)
        if self._stderr is not None:
            self._stderr.close()


def parse_curl_metrics(output: str) -> CurlMetrics:
    line = output.strip()
    parts = line.split("\t")
    if len(parts) != 9 or parts[0] != CURL_METRIC_PREFIX:
        raise RunnerError("transfer_metrics_invalid")
    try:
        status = int(parts[1])
        downloaded = int(parts[2])
        uploaded = int(parts[3])
        values = [float(item) for item in parts[4:]]
    except ValueError as exc:
        raise RunnerError("transfer_metrics_invalid") from exc
    if (
        status < 0
        or downloaded < 0
        or uploaded < 0
        or any(not math.isfinite(item) or item < 0 for item in values)
    ):
        raise RunnerError("transfer_metrics_invalid")
    return CurlMetrics(status, downloaded, uploaded, *values)


def run_registered_process(
    argv: Sequence[str],
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    timeout: float,
    monitor: ExternalMonitor | None,
    *,
    stdout: Any = subprocess.PIPE,
    stderr: Any = subprocess.DEVNULL,
    text: bool = True,
) -> tuple[int, str]:
    try:
        process = subprocess.Popen(
            tuple(argv),
            stdin=subprocess.DEVNULL,
            stdout=stdout,
            stderr=stderr,
            text=text,
            start_new_session=True,
            env=env,
        )
    except OSError as exc:
        raise RunnerError("child_start_failed") from exc
    registry.add(process)
    deadline = time.monotonic() + timeout
    try:
        while process.poll() is None:
            if cancel.is_set():
                registry.terminate(process)
                raise RunnerError("cancelled")
            if monitor is not None:
                monitor.check()
            if time.monotonic() >= deadline:
                registry.terminate(process)
                raise RunnerError("child_timeout")
            time.sleep(0.1)
        captured = ""
        if process.stdout is not None and stdout == subprocess.PIPE:
            raw = process.stdout.read()
            captured = raw if isinstance(raw, str) else raw.decode(errors="replace")
        return int(process.returncode or 0), captured
    finally:
        registry.discard(process)


def curl_base(curl: Path, timeout: int, output: str) -> list[str]:
    return [
        str(curl),
        "--disable",
        "--silent",
        "--show-error",
        "--http1.1",
        "--globoff",
        "--proto",
        "=https",
        "--tlsv1.2",
        "--connect-timeout",
        "10",
        "--max-time",
        str(timeout),
        "--header",
        "Accept-Encoding: identity",
        "--output",
        output,
        "--write-out",
        CURL_WRITE_OUT,
    ]


def curl_request(
    provider: Provider,
    byte_count: int,
    rate: str | None,
    socks_port: int | None,
    payload_path: Path | None,
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    monitor: ExternalMonitor | None,
    timeout: int,
) -> RequestResult:
    argv = curl_base(curl, timeout, os.devnull)
    if socks_port is not None:
        argv.extend(["--socks5-hostname", f"127.0.0.1:{socks_port}"])
    if rate:
        argv.extend(["--limit-rate", rate])
    if provider.kind == "download" and provider.range_request:
        if byte_count <= 0:
            raise RunnerError("provider_manifest_invalid")
        argv.extend(["--range", f"0-{byte_count - 1}"])
    if provider.kind == "upload":
        if payload_path is None:
            raise RunnerError("upload_payload_missing")
        argv.extend(
            [
                "--request",
                "POST",
                "--header",
                "Content-Type: application/octet-stream",
                "--data-binary",
                f"@{payload_path}",
            ]
        )
    url = provider.request_url(byte_count)
    argv.append(url)
    started = time.monotonic()
    returncode, output = run_registered_process(
        argv,
        registry,
        cancel,
        env,
        timeout + 5,
        monitor,
    )
    metrics = parse_curl_metrics(output)
    expected_upload = byte_count if provider.kind == "upload" else 0
    expected_download = byte_count if provider.kind == "download" else metrics.downloaded
    success = (
        returncode == 0
        and metrics.status in provider.statuses
        and metrics.uploaded == expected_upload
        and metrics.downloaded == expected_download
    )
    if not success:
        raise RunnerError("provider_transfer_failed")
    if provider.kind == "upload":
        first = started + metrics.pretransfer
        last = started + max(metrics.starttransfer, metrics.pretransfer)
        successful = metrics.uploaded
        attempted = metrics.uploaded
    elif provider.kind == "download":
        first = started + metrics.starttransfer
        last = started + metrics.total
        successful = metrics.downloaded
        attempted = metrics.downloaded
    else:
        first = started + metrics.starttransfer
        last = started + metrics.total
        successful = metrics.downloaded
        attempted = metrics.downloaded
    if last < first:
        raise RunnerError("transfer_metrics_invalid")
    return RequestResult(first, last, successful, attempted, provider.independence_key)


def create_payload(path: Path, byte_count: int) -> None:
    block = os.urandom(64 * 1024)
    remaining = byte_count
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    descriptor = os.open(path, flags, 0o600)
    try:
        while remaining:
            piece = block[: min(len(block), remaining)]
            view = memoryview(piece)
            while view:
                written = os.write(descriptor, view)
                if written <= 0:
                    raise RunnerError("payload_creation_failed")
                view = view[written:]
                remaining -= written
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def resilient_curl_request(
    providers: Sequence[Provider],
    start_index: int,
    byte_count: int,
    rate: str | None,
    socks_port: int,
    payload_path: Path | None,
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    monitor: ExternalMonitor,
    timeout: int,
) -> RequestResult:
    """Rotate receiver failures, but stop when direct control proves the receiver healthy."""
    if not providers:
        raise RunnerError("provider_pool_exhausted")
    retriable = {"provider_transfer_failed", "child_timeout"}
    failed_attempted_bytes = 0
    for offset in range(len(providers)):
        provider = providers[(start_index + offset) % len(providers)]
        try:
            result = curl_request(
                provider,
                byte_count,
                rate,
                socks_port,
                payload_path,
                curl,
                registry,
                cancel,
                env,
                monitor,
                timeout,
            )
            return replace(
                result,
                attempted_bytes=result.attempted_bytes + failed_attempted_bytes,
            )
        except RunnerError as proxied_error:
            if proxied_error.code not in retriable:
                raise
            failed_attempted_bytes += byte_count
            try:
                curl_request(
                    provider,
                    byte_count,
                    None,
                    None,
                    payload_path,
                    curl,
                    registry,
                    cancel,
                    env,
                    monitor,
                    timeout,
                )
            except RunnerError as direct_error:
                if direct_error.code not in retriable:
                    raise
                # The receiver failed outside the VPN too; rotate to another
                # prequalified independent origin without blaming the tunnel.
                continue
            # The receiver is healthy directly. One bounded same-origin retry
            # distinguishes a transient request from a reproducible VPN fault.
            try:
                result = curl_request(
                    provider,
                    byte_count,
                    rate,
                    socks_port,
                    payload_path,
                    curl,
                    registry,
                    cancel,
                    env,
                    monitor,
                    timeout,
                )
                return replace(
                    result,
                    attempted_bytes=result.attempted_bytes + failed_attempted_bytes,
                )
            except RunnerError as retry_error:
                if retry_error.code in retriable:
                    raise RunnerError("tunnel_transfer_failed") from retry_error
                raise
    raise RunnerError("provider_pool_exhausted")


def qualify_providers(
    pools: ProviderPools,
    preflight_payload: Path,
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
) -> ProviderPools:
    qualified: dict[str, list[Provider]] = {"probe": [], "download": [], "upload": []}
    for kind, providers in (
        ("probe", pools.probe),
        ("download", pools.download),
        ("upload", pools.upload),
    ):
        for provider in providers:
            try:
                curl_request(
                    provider,
                    PREFLIGHT_BYTES if kind != "probe" else 0,
                    None,
                    None,
                    preflight_payload if kind == "upload" else None,
                    curl,
                    registry,
                    cancel,
                    env,
                    None,
                    25,
                )
            except RunnerError:
                continue
            qualified[kind].append(provider)
        if len({item.independence_key for item in qualified[kind]}) < MIN_QUALIFIED_PROVIDERS:
            raise RunnerError("provider_prequalification_failed")
    return ProviderPools(
        probe=tuple(qualified["probe"]),
        download=tuple(qualified["download"]),
        upload=tuple(qualified["upload"]),
    )


class CoreSession:
    def __init__(
        self,
        label: str,
        raw_link: str,
        outbound: dict[str, Any],
        xray: Path,
        karing: Path,
        workspace: Path,
        registry: ProcessRegistry,
        cancel: threading.Event,
        monitor: ExternalMonitor,
    ):
        self.label = label
        self.raw_link = raw_link
        self.outbound = outbound
        self.xray = xray
        self.karing = karing
        self.workspace = workspace
        self.registry = registry
        self.cancel = cancel
        self.monitor = monitor
        self.port = unused_local_port()
        self.directory: Path | None = None
        self.process: subprocess.Popen[Any] | None = None
        self.log_handle: Any = None

    def __enter__(self) -> "CoreSession":
        try:
            self.directory = Path(tempfile.mkdtemp(prefix="core-", dir=self.workspace))
            os.chmod(self.directory, 0o700)
            config_path = self.directory / "config.json"
            log_path = self.directory / "core.log"
            if self.label == KARING_LABEL:
                config = karing_client_config(self.raw_link, self.port)
                check_argv = (str(self.karing), "check", "-c", str(config_path))
                run_argv = (str(self.karing), "run", "-c", str(config_path))
            else:
                config = client_config(self.port, self.outbound)
                check_argv = (str(self.xray), "run", "-test", "-c", str(config_path))
                run_argv = (str(self.xray), "run", "-c", str(config_path))
            config_path.write_text(json.dumps(config, separators=(",", ":")), encoding="utf-8")
            os.chmod(config_path, 0o600)
            self.log_handle = log_path.open("wb")
            os.chmod(log_path, 0o600)
            returncode, _ = run_registered_process(
                check_argv,
                self.registry,
                self.cancel,
                safe_child_env(self.workspace),
                20,
                self.monitor,
                stdout=self.log_handle,
                stderr=subprocess.STDOUT,
                text=False,
            )
            if returncode != 0:
                raise RunnerError("core_config_failed")
            try:
                self.process = subprocess.Popen(
                    run_argv,
                    stdin=subprocess.DEVNULL,
                    stdout=self.log_handle,
                    stderr=subprocess.STDOUT,
                    start_new_session=True,
                    env=safe_child_env(self.workspace),
                )
            except OSError as exc:
                raise RunnerError("core_start_failed") from exc
            self.registry.add(self.process)
            if not wait_for_listener(self.port, self.process):
                raise RunnerError("core_listener_failed")
            self.monitor.check()
            return self
        except BaseException:
            self.close()
            raise

    def close(self) -> None:
        if self.process is not None:
            self.registry.terminate(self.process)
            self.registry.discard(self.process)
            self.process = None
        if self.log_handle is not None:
            self.log_handle.close()
            self.log_handle = None
        if self.directory is not None:
            shutil.rmtree(self.directory, ignore_errors=False)
            self.directory = None

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.close()


def probe_tunnel(
    session: CoreSession,
    providers: Sequence[Provider],
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    monitor: ExternalMonitor,
) -> None:
    selected: list[Provider] = []
    keys: set[str] = set()
    for provider in providers:
        if provider.independence_key not in keys:
            selected.append(provider)
            keys.add(provider.independence_key)
        if len(selected) == MIN_QUALIFIED_PROVIDERS:
            break
    if len(selected) < MIN_QUALIFIED_PROVIDERS:
        raise RunnerError("probe_provider_count_failed")
    for provider in selected:
        curl_request(
            provider,
            0,
            None,
            session.port,
            None,
            curl,
            registry,
            cancel,
            env,
            monitor,
            25,
        )


def aggregate_workers(kind: str, workers: Sequence[WorkerResult]) -> StageResult:
    if not workers:
        raise RunnerError("stage_worker_failed")
    expected_workers = STREAM_WORKERS if kind == "stream" else TRANSFER_WORKERS
    expected_bytes = (
        STREAM_WORKERS * STREAM_BYTES_PER_WORKER
        if kind == "stream"
        else TRANSFER_WORKERS * TRANSFER_CHUNKS_PER_WORKER * TRANSFER_CHUNK_BYTES
    )
    if len(workers) != expected_workers:
        raise RunnerError("stage_worker_failed")
    if any(item.last_payload - item.first_payload < MIN_PAYLOAD_SECONDS for item in workers):
        raise RunnerError("stage_duration_failed")
    successful = sum(item.successful_bytes for item in workers)
    attempted = sum(item.attempted_bytes for item in workers)
    provider_keys = set().union(*(item.provider_keys for item in workers))
    if successful != expected_bytes or attempted < successful:
        raise RunnerError("stage_byte_count_failed")
    if len(provider_keys) < MIN_QUALIFIED_PROVIDERS:
        raise RunnerError("stage_provider_count_failed")
    result = StageResult(
        kind=kind,
        first_payload=min(item.first_payload for item in workers),
        last_payload=max(item.last_payload for item in workers),
        successful_bytes=successful,
        attempted_bytes=attempted,
        provider_count=len(provider_keys),
        worker_count=len(workers),
    )
    if result.duration < MIN_PAYLOAD_SECONDS:
        raise RunnerError("stage_duration_failed")
    return result


def validate_clean_stage(result: StageResult) -> None:
    # Receiver rotation/direct checks make the attempt useful diagnostically,
    # but not a clean acceptance sample.  Classify it before applying the
    # duration ceiling so endpoint delay cannot be blamed on the VPN.
    if result.attempted_bytes > result.successful_bytes:
        raise RunnerError("stage_provider_instability")
    if result.kind == "stream":
        if result.rate_mbps < 8.388608 * 0.8:
            raise RunnerError("stage_rate_failed")
    elif result.duration > MAX_CHUNKED_PAYLOAD_SECONDS:
        # Chunked stages deliberately rotate 72 independent HTTPS requests.
        # Their fixed ceiling preserves a meaningful 20.1 Mbps useful-data
        # floor without pretending that curl's per-request cap is a promise.
        raise RunnerError("stage_rate_failed")


def run_workers(functions: Iterable[Callable[[], WorkerResult]], cancel: threading.Event) -> list[WorkerResult]:
    callables = list(functions)
    results: list[WorkerResult] = []
    with ThreadPoolExecutor(max_workers=len(callables), thread_name_prefix="vpn-stage") as executor:
        futures = [executor.submit(function) for function in callables]
        try:
            for future in as_completed(futures):
                results.append(future.result())
        except BaseException:
            cancel.set()
            for future in futures:
                future.cancel()
            raise
    return results


def run_stream_stage(
    session: CoreSession,
    providers: Sequence[Provider],
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    monitor: ExternalMonitor,
) -> StageResult:
    selected = list(providers[:STREAM_WORKERS])
    if len({item.independence_key for item in selected}) != STREAM_WORKERS:
        raise RunnerError("stage_provider_count_failed")

    def make_worker(worker_index: int, provider: Provider) -> Callable[[], WorkerResult]:
        def worker() -> WorkerResult:
            request = resilient_curl_request(
                providers,
                worker_index,
                STREAM_BYTES_PER_WORKER,
                "512k",
                session.port,
                None,
                curl,
                registry,
                cancel,
                env,
                monitor,
                120,
            )
            return WorkerResult(
                request.first_payload,
                request.last_payload,
                request.successful_bytes,
                request.attempted_bytes,
                frozenset([request.provider_key]),
            )

        return worker

    return aggregate_workers(
        "stream",
        run_workers(
            (make_worker(index, item) for index, item in enumerate(selected)), cancel
        ),
    )


def run_chunked_stage(
    kind: str,
    session: CoreSession,
    providers: Sequence[Provider],
    payload_path: Path | None,
    curl: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    env: dict[str, str],
    monitor: ExternalMonitor,
) -> StageResult:
    if len({item.independence_key for item in providers}) < MIN_QUALIFIED_PROVIDERS:
        raise RunnerError("stage_provider_count_failed")

    def make_worker(worker_index: int) -> Callable[[], WorkerResult]:
        def worker() -> WorkerResult:
            requests: list[RequestResult] = []
            for chunk_index in range(TRANSFER_CHUNKS_PER_WORKER):
                if cancel.is_set():
                    raise RunnerError("cancelled")
                requests.append(
                    resilient_curl_request(
                        providers,
                        worker_index + chunk_index,
                        TRANSFER_CHUNK_BYTES,
                        "1m",
                        session.port,
                        payload_path,
                        curl,
                        registry,
                        cancel,
                        env,
                        monitor,
                        35,
                    )
                )
            return WorkerResult(
                first_payload=min(item.first_payload for item in requests),
                last_payload=max(item.last_payload for item in requests),
                successful_bytes=sum(item.successful_bytes for item in requests),
                attempted_bytes=sum(item.attempted_bytes for item in requests),
                provider_keys=frozenset(item.provider_key for item in requests),
            )

        return worker

    workers = run_workers((make_worker(index) for index in range(TRANSFER_WORKERS)), cancel)
    return aggregate_workers(kind, workers)


def verify_core_versions(
    xray: Path,
    karing: Path,
    registry: ProcessRegistry,
    cancel: threading.Event,
    workspace: Path,
) -> None:
    for path in (xray, karing):
        try:
            info = path.stat()
        except OSError as exc:
            raise RunnerError("core_binary_invalid") from exc
        if not stat.S_ISREG(info.st_mode) or not os.access(path, os.X_OK):
            raise RunnerError("core_binary_invalid")
    env = safe_child_env(workspace)
    xray_code, xray_output = run_registered_process(
        (str(xray), "version"), registry, cancel, env, 15, None
    )
    karing_code, karing_output = run_registered_process(
        (str(karing), "version"), registry, cancel, env, 15, None
    )
    if xray_code != 0 or not re.search(rf"\bXray\s+{re.escape(XRAY_VERSION)}\b", xray_output):
        raise RunnerError("xray_version_mismatch")
    if (
        karing_code != 0
        or f"sing-box version {KARING_VERSION}" not in karing_output
        or f"Revision: {KARING_REVISION}" not in karing_output
    ):
        raise RunnerError("karing_version_mismatch")


def parse_profiles(links: Sequence[str]) -> dict[str, tuple[str, dict[str, Any]]]:
    profiles: dict[str, tuple[str, dict[str, Any]]] = {}
    try:
        for raw_link in links:
            label, _, outbound = parse_link(raw_link)
            if label in profiles:
                raise RunnerError("profile_set_invalid")
            profiles[label] = (raw_link, outbound)
    except MobilityTestError as exc:
        raise RunnerError("profile_set_invalid") from exc
    if set(profiles) != set(EXPECTED_LABELS):
        raise RunnerError("profile_set_invalid")
    return profiles


def synthetic_manifest() -> dict[str, object]:
    def entries(kind: str) -> list[dict[str, object]]:
        result: list[dict[str, object]] = []
        for index in range(3):
            host = f"{kind}{index}.example.test"
            item: dict[str, object] = {
                "name": f"{kind}-{index}",
                "independence_key": f"{kind}-site-{index}",
                "expected_status": [200],
            }
            if kind == "download":
                item["url_template"] = f"https://{host}/data?bytes={{bytes}}"
            else:
                item["url"] = f"https://{host}/probe"
            if kind == "upload":
                item["acknowledges_full_body"] = True
            result.append(item)
        return result

    return {"version": 1, "probe": entries("probe"), "download": entries("download"), "upload": entries("upload")}


def self_test() -> None:
    pools = parse_provider_manifest_value(synthetic_manifest())
    if not all(len(value) == 3 for value in (pools.probe, pools.download, pools.upload)):
        raise RunnerError("synthetic_validation_failed")
    metrics = parse_curl_metrics(
        CURL_METRIC_PREFIX + "\t200\t65536\t0\t0.1\t0.2\t1.1\t60000\t0"
    )
    if metrics.status != 200 or metrics.downloaded != 65536:
        raise RunnerError("synthetic_validation_failed")
    if ExternalMonitor.parse_line("READY")[0] != "ready" or ExternalMonitor.parse_line("SAMPLE")[0] != "sample":
        raise RunnerError("synthetic_validation_failed")
    now = time.monotonic()
    stream_workers = [
        WorkerResult(now, now + 72, STREAM_BYTES_PER_WORKER, STREAM_BYTES_PER_WORKER, frozenset([f"s{index}"]))
        for index in range(STREAM_WORKERS)
    ]
    transfer_worker_bytes = TRANSFER_CHUNKS_PER_WORKER * TRANSFER_CHUNK_BYTES
    transfer_workers = [
        WorkerResult(now, now + 72, transfer_worker_bytes, transfer_worker_bytes, frozenset(["a", "b"]))
        for _ in range(TRANSFER_WORKERS)
    ]
    stream_result = aggregate_workers("stream", stream_workers)
    if stream_result.successful_bytes != 72 * MIB:
        raise RunnerError("synthetic_validation_failed")
    download_result = aggregate_workers("download", transfer_workers)
    if download_result.successful_bytes != 288 * MIB:
        raise RunnerError("synthetic_validation_failed")
    upload_result = aggregate_workers("upload", transfer_workers)
    if upload_result.duration < MIN_PAYLOAD_SECONDS:
        raise RunnerError("synthetic_validation_failed")
    for result in (stream_result, download_result, upload_result):
        validate_clean_stage(result)
    slower_workers = [
        WorkerResult(
            now,
            now + MAX_CHUNKED_PAYLOAD_SECONDS,
            transfer_worker_bytes,
            transfer_worker_bytes,
            frozenset(["a", "b"]),
        )
        for _ in range(TRANSFER_WORKERS)
    ]
    slower_result = aggregate_workers("download", slower_workers)
    validate_clean_stage(slower_result)
    if slower_result.duration != MAX_CHUNKED_PAYLOAD_SECONDS:
        raise RunnerError("synthetic_validation_failed")
    too_slow_workers = [
        replace(item, last_payload=item.last_payload + 0.1) for item in slower_workers
    ]
    try:
        validate_clean_stage(aggregate_workers("download", too_slow_workers))
    except RunnerError as error:
        if error.code != "stage_rate_failed":
            raise RunnerError("synthetic_validation_failed") from error
    else:
        raise RunnerError("synthetic_validation_failed")
    for dirty_result in (
        replace(
            stream_result,
            attempted_bytes=stream_result.successful_bytes + MIB,
            last_payload=stream_result.last_payload + 120,
        ),
        replace(
            slower_result,
            attempted_bytes=slower_result.successful_bytes + MIB,
            last_payload=slower_result.last_payload + 1,
        ),
    ):
        try:
            validate_clean_stage(dirty_result)
        except RunnerError as error:
            if error.code != "stage_provider_instability":
                raise RunnerError("synthetic_validation_failed") from error
        else:
            raise RunnerError("synthetic_validation_failed")
    print(
        f"synthetic_validation=passed profiles={PROFILE_COUNT} stages={STAGE_COUNT}",
        flush=True,
    )


def run(args: argparse.Namespace) -> None:
    links = load_private_links(args.links_file)
    profiles = parse_profiles(links)
    manifest = load_provider_manifest(args.provider_manifest)
    monitor_argv = parse_monitor_command(args.monitor_command_file)
    curl = Path(args.curl)
    if not curl.is_file() or not os.access(curl, os.X_OK):
        raise RunnerError("curl_binary_invalid")

    cancel = threading.Event()
    registry = ProcessRegistry(cancel)
    workspace_owner = PrivateWorkspace()
    cleanup_ok = False
    previous_handlers: dict[int, Any] = {}

    def handle_signal(signum: int, frame: object) -> None:
        del signum, frame
        cancel.set()

    for signum in (signal.SIGINT, signal.SIGTERM):
        previous_handlers[signum] = signal.getsignal(signum)
        signal.signal(signum, handle_signal)

    try:
        workspace = workspace_owner.__enter__()
        child_env = safe_child_env(workspace)
        preflight_payload = workspace / "preflight-payload.bin"
        upload_payload = workspace / "upload-payload.bin"
        create_payload(preflight_payload, PREFLIGHT_BYTES)
        create_payload(upload_payload, TRANSFER_CHUNK_BYTES)
        verify_core_versions(args.xray, args.karing_core, registry, cancel, workspace)
        print("cores=verified xray=26.7.11 karing=1.12.22.2502", flush=True)
        qualified = qualify_providers(
            manifest, preflight_payload, curl, registry, cancel, child_env
        )
        print(
            f"providers=qualified probe={len(qualified.probe)} "
            f"download={len(qualified.download)} upload={len(qualified.upload)}",
            flush=True,
        )
        monitor = ExternalMonitor(monitor_argv, registry, cancel, workspace)
        try:
            monitor.start()
            print("monitor=ready cadence=1hz", flush=True)
            stage_number = 0
            for label in EXPECTED_LABELS:
                raw_link, outbound = profiles[label]
                for kind in STAGE_KINDS:
                    stage_number += 1
                    monitor.check()
                    stage_attempt = 0
                    total_successful_bytes = 0
                    total_attempted_bytes = 0
                    total_monitor_samples = 0
                    while True:
                        stage_attempt += 1
                        try:
                            with CoreSession(
                                label,
                                raw_link,
                                outbound,
                                args.xray,
                                args.karing_core,
                                workspace,
                                registry,
                                cancel,
                                monitor,
                            ) as session:
                                probe_tunnel(
                                    session,
                                    qualified.probe,
                                    curl,
                                    registry,
                                    cancel,
                                    child_env,
                                    monitor,
                                )
                                monitor_started = monitor.mark()
                                if kind == "stream":
                                    result = run_stream_stage(
                                        session,
                                        qualified.download,
                                        curl,
                                        registry,
                                        cancel,
                                        child_env,
                                        monitor,
                                    )
                                elif kind == "download":
                                    result = run_chunked_stage(
                                        kind,
                                        session,
                                        qualified.download,
                                        None,
                                        curl,
                                        registry,
                                        cancel,
                                        child_env,
                                        monitor,
                                    )
                                else:
                                    result = run_chunked_stage(
                                        kind,
                                        session,
                                        qualified.upload,
                                        upload_payload,
                                        curl,
                                        registry,
                                        cancel,
                                        child_env,
                                        monitor,
                                    )
                                monitor_ended = time.monotonic()
                                samples = monitor.validate_window(
                                    monitor_started, monitor_ended
                                )
                                total_successful_bytes += result.successful_bytes
                                total_attempted_bytes += result.attempted_bytes
                                total_monitor_samples += samples
                                validate_clean_stage(result)
                        except RunnerError as error:
                            if error.code == "stage_provider_instability" and stage_attempt == 1:
                                print(
                                    f"stage_retry={stage_number:02d}/{STAGE_COUNT} profile={label} "
                                    f"kind={kind} reason=receiver_instability "
                                    f"successful={total_successful_bytes} "
                                    f"attempted={total_attempted_bytes} "
                                    f"monitor_samples={total_monitor_samples}",
                                    flush=True,
                                )
                                continue
                            raise
                        break
                    # A completely new core process proves the required fresh
                    # reconnect after every sustained stage.
                    with CoreSession(
                        label,
                        raw_link,
                        outbound,
                        args.xray,
                        args.karing_core,
                        workspace,
                        registry,
                        cancel,
                        monitor,
                    ) as recovery:
                        probe_tunnel(
                            recovery, qualified.probe, curl, registry, cancel, child_env, monitor
                        )
                    print(
                        f"stage={stage_number:02d}/{STAGE_COUNT} profile={label} kind={kind} "
                        f"status=passed duration={result.duration:.1f}s "
                        f"bytes={result.successful_bytes} "
                        f"successful_total={total_successful_bytes} "
                        f"attempted={total_attempted_bytes} attempts={stage_attempt} "
                        f"rate={result.rate_mbps:.1f}mbps providers={result.provider_count} "
                        f"workers={result.worker_count} reconnect=passed "
                        f"monitor_samples={total_monitor_samples}",
                        flush=True,
                    )
            monitor.check()
            print(
                f"sustained_suite=passed profiles={PROFILE_COUNT} stages={STAGE_COUNT}",
                flush=True,
            )
        finally:
            monitor.close()
    finally:
        registry.terminate_all()
        for signum, handler in previous_handlers.items():
            signal.signal(signum, handler)
        try:
            cleanup_ok = workspace_owner.cleanup()
        except OSError:
            cleanup_ok = False
        print("cleanup=" + ("passed" if cleanup_ok and registry.empty() else "failed"), flush=True)
        if not cleanup_ok or not registry.empty():
            raise RunnerError("cleanup_failed")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true", help="run synthetic validation only")
    parser.add_argument("--xray", type=Path, help="Xray 26.7.11 executable")
    parser.add_argument("--karing-core", type=Path, help="exact Karing sing-box executable")
    parser.add_argument("--links-file", help="private JSON link array, or - for stdin")
    parser.add_argument("--provider-manifest", type=Path, help="non-secret HTTPS provider manifest")
    parser.add_argument("--monitor-command-file", type=Path, help="non-secret JSON monitor argv")
    parser.add_argument("--curl", default=shutil.which("curl") or "/usr/bin/curl")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    if args.self_test:
        self_test()
        return 0
    if any(
        value is None
        for value in (
            args.xray,
            args.karing_core,
            args.links_file,
            args.provider_manifest,
            args.monitor_command_file,
        )
    ):
        raise RunnerError("required_argument_missing")
    run(args)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RunnerError as exc:
        print(f"sustained_suite=failed code={exc.code}", flush=True)
        raise SystemExit(1)
    except (MobilityTestError, OSError, ValueError, json.JSONDecodeError):
        print("sustained_suite=failed code=private_or_runtime_input_invalid", flush=True)
        raise SystemExit(1)
