#!/usr/bin/env python3
from __future__ import annotations

import argparse
import glob
import logging
import os
import signal
import sys
import time
from typing import Optional


LOG = logging.getLogger("arbuzas_thinkpad_fan")
DEFAULT_FAN_PROC = "/proc/acpi/ibm/fan"
DEFAULT_FAN_CONTROL_PARAM = "/sys/module/thinkpad_acpi/parameters/fan_control"
DEFAULT_TEMP_GLOB = "/sys/devices/platform/thinkpad_hwmon/hwmon/hwmon*/temp1_input"


def env_int(name: str, default: int) -> int:
    value = os.environ.get(name, "").strip()
    return int(value) if value else default


def env_float(name: str, default: float) -> float:
    value = os.environ.get(name, "").strip()
    return float(value) if value else default


def env_str(name: str, default: str) -> str:
    value = os.environ.get(name, "").strip()
    return value or default


def discover_single_path(pattern: str) -> str:
    matches = sorted(glob.glob(pattern))
    if not matches:
        raise FileNotFoundError(f"no paths matched {pattern!r}")
    return matches[0]


def read_file(path: str) -> str:
    with open(path, "r", encoding="utf-8") as handle:
        return handle.read().strip()


def read_temp_celsius(path: str) -> float:
    return int(read_file(path)) / 1000.0


def write_fan_command(fan_proc: str, command: str) -> None:
    with open(fan_proc, "w", encoding="utf-8") as handle:
        handle.write(command + "\n")


def write_fan_commands(fan_proc: str, *commands: str) -> None:
    for command in commands:
        write_fan_command(fan_proc, command)


class FanController:
    def __init__(
        self,
        fan_proc: str,
        temp_path: str,
        fan_control_param: str,
        manual_level: int,
        enter_auto_c: float,
        exit_auto_c: float,
        watchdog_seconds: int,
    ) -> None:
        self.fan_proc = fan_proc
        self.temp_path = temp_path
        self.fan_control_param = fan_control_param
        self.manual_level = manual_level
        self.enter_auto_c = enter_auto_c
        self.exit_auto_c = exit_auto_c
        self.watchdog_seconds = watchdog_seconds
        self.auto_mode = False

    def ensure_manual_control_available(self) -> None:
        enabled = read_file(self.fan_control_param)
        if enabled != "Y":
            raise RuntimeError(
                f"manual fan control is unavailable because {self.fan_control_param}={enabled!r}"
            )

    def desired_auto_mode(self, temp_c: float) -> bool:
        if self.auto_mode:
            return temp_c > self.exit_auto_c
        return temp_c >= self.enter_auto_c

    def refresh(self) -> tuple[float, str]:
        temp_c = read_temp_celsius(self.temp_path)
        desired_auto = self.desired_auto_mode(temp_c)
        self.auto_mode = desired_auto
        level_command = "level auto" if desired_auto else f"level {self.manual_level}"
        write_fan_commands(
            self.fan_proc,
            f"watchdog {self.watchdog_seconds}",
            level_command,
        )
        return temp_c, level_command

    def restore_auto(self) -> None:
        try:
            write_fan_commands(self.fan_proc, "watchdog 0", "level auto")
        except Exception as exc:  # pragma: no cover - best effort cleanup
            LOG.warning("failed to restore ThinkPad auto mode: %s", exc)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Keep the Arbuzas ThinkPad fan on the lowest manual level during "
            "normal temperatures and return control to the embedded controller "
            "at high temperatures."
        )
    )
    parser.add_argument("--once", action="store_true", help="apply the policy once and exit")
    return parser.parse_args()


def main() -> int:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    args = parse_args()

    manual_level = env_int("ARBUZAS_FAN_MANUAL_LEVEL", 1)
    enter_auto_c = env_float("ARBUZAS_FAN_ENTER_AUTO_C", 89.0)
    exit_auto_c = env_float("ARBUZAS_FAN_EXIT_AUTO_C", 89.0)
    poll_interval = env_float("ARBUZAS_FAN_POLL_INTERVAL_SECONDS", 2.0)
    watchdog_seconds = env_int("ARBUZAS_FAN_WATCHDOG_SECONDS", 10)
    fan_proc = env_str("ARBUZAS_FAN_PROC", DEFAULT_FAN_PROC)
    fan_control_param = env_str(
        "ARBUZAS_FAN_CONTROL_PARAM", DEFAULT_FAN_CONTROL_PARAM
    )
    temp_glob = env_str("ARBUZAS_FAN_TEMP_GLOB", DEFAULT_TEMP_GLOB)
    temp_path = discover_single_path(temp_glob)

    controller = FanController(
        fan_proc=fan_proc,
        temp_path=temp_path,
        fan_control_param=fan_control_param,
        manual_level=manual_level,
        enter_auto_c=enter_auto_c,
        exit_auto_c=exit_auto_c,
        watchdog_seconds=watchdog_seconds,
    )
    controller.ensure_manual_control_available()

    initial_temp = read_temp_celsius(temp_path)
    controller.auto_mode = initial_temp >= enter_auto_c
    LOG.info(
        "starting fan controller: temp_path=%s manual_level=%s enter_auto_c=%.1f exit_auto_c=%.1f watchdog_seconds=%s",
        temp_path,
        manual_level,
        enter_auto_c,
        exit_auto_c,
        watchdog_seconds,
    )

    stop_requested = False

    def request_stop(signum: int, _frame: Optional[object]) -> None:
        nonlocal stop_requested
        LOG.info("received signal %s, restoring ThinkPad auto mode", signum)
        stop_requested = True

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)

    last_level_command: Optional[str] = None
    try:
        while not stop_requested:
            temp_c, level_command = controller.refresh()
            if level_command != last_level_command:
                LOG.info("temp=%.1fC applying %s", temp_c, level_command)
                last_level_command = level_command
            if args.once:
                break
            time.sleep(poll_interval)
    finally:
        controller.restore_auto()

    return 0


if __name__ == "__main__":
    sys.exit(main())
