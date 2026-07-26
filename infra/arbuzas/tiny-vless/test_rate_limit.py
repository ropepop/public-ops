#!/usr/bin/env python3
"""Exercise the host limiter script with command-level fakes."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


SCRIPT = Path(__file__).parent / "host/tiny-vless-rate-limit"


FAKE_DOCKER = """#!/bin/sh
case "$1" in
  inspect) printf '4321\\n' ;;
  exec) printf '42\\n' ;;
  *) exit 1 ;;
esac
"""

FAKE_IP = """#!/bin/sh
case "$1 $2" in
  "link show"|"link add"|"link set") exit 0 ;;
  *) exit 1 ;;
esac
"""

FAKE_TC = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

state_path = Path(os.environ["TC_STATE"])
log_path = Path(os.environ["TC_LOG"])
state = json.loads(state_path.read_text())
args = sys.argv[1:]

def save():
    state_path.write_text(json.dumps(state, sort_keys=True))

def log():
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(args) + "\n")

def expected_filter():
    return [
        {"protocol":"all","pref":1,"kind":"matchall","chain":0},
        {
            "protocol":"all","pref":1,"kind":"matchall","chain":0,
            "options":{"handle":1,"actions":[{
                "order":1,"kind":"mirred","mirred_action":"redirect",
                "direction":"egress","to_dev":"ifb-tinyvless",
                "control_action":{"type":"stolen"}
            }]}
        }
    ]

if args[:3] == ["qdisc", "show", "dev"]:
    device = args[3]
    geometry = state["geometry"].get(device, "missing")
    if geometry == "healthy":
        print("qdisc tbf 10: root refcnt 2 rate 100Mbit burst 2Mb lat 50ms")
    elif geometry == "old":
        print("qdisc tbf 10: root refcnt 2 rate 100Mbit burst 512Kb lat 50ms")
    if device == "veth-test" and state.get("ingress"):
        print("qdisc ingress ffff: parent ffff:fff1")
    raise SystemExit(0)

if args[:4] == ["-j", "filter", "show", "dev"]:
    mode = state.get("filter", "missing")
    payload = expected_filter() if mode in {"healthy", "extra"} else []
    if mode == "extra":
        payload += [
            {"protocol":"all","pref":0,"kind":"matchall","chain":0},
            {
                "protocol":"all","pref":0,"kind":"matchall","chain":0,
                "options":{"handle":2,"actions":[{
                    "order":1,"kind":"mirred","mirred_action":"redirect",
                    "direction":"egress","to_dev":"wrong-ifb",
                    "control_action":{"type":"stolen"}
                }]}
            }
        ]
    print(json.dumps(payload, separators=(",", ":")))
    raise SystemExit(0)

if args[:3] == ["qdisc", "change", "dev"]:
    log()
    if state.get("change_fails"):
        raise SystemExit(1)
    if not state.get("ignore_repairs"):
        state["geometry"][args[3]] = "healthy"
        save()
    raise SystemExit(0)

if args[:3] == ["qdisc", "del", "dev"]:
    log()
    if not state.get("ignore_repairs"):
        device = args[3]
        if "root" in args:
            state["geometry"][device] = "missing"
        if "ingress" in args:
            state["ingress"] = False
            state["filter"] = "missing"
        save()
    raise SystemExit(0)

if args[:3] == ["qdisc", "add", "dev"]:
    log()
    if not state.get("ignore_repairs"):
        device = args[3]
        if "root" in args:
            state["geometry"][device] = "healthy"
        if "ingress" in args:
            state["ingress"] = True
        save()
    raise SystemExit(0)

if args[:3] == ["filter", "add", "dev"]:
    log()
    if not state.get("ignore_repairs"):
        state["filter"] = "healthy"
        save()
    raise SystemExit(0)

raise SystemExit(1)
'''


def executable(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o700)


def run_case(initial: dict[str, object], **overrides: str) -> tuple[int, list[list[str]]]:
    with tempfile.TemporaryDirectory(prefix="tiny-vless-rate-limit-test-") as raw_root:
        root = Path(raw_root)
        fake_bin = root / "bin"
        fake_bin.mkdir(mode=0o700)
        executable(fake_bin / "docker", FAKE_DOCKER)
        executable(fake_bin / "ip", FAKE_IP)
        executable(fake_bin / "modprobe", "#!/bin/sh\nexit 0\n")
        executable(fake_bin / "tc", FAKE_TC)

        sys_net = root / "sys-class-net"
        veth = sys_net / "veth-test"
        veth.mkdir(parents=True, mode=0o700)
        (veth / "ifindex").write_text("42\n", encoding="ascii")

        state = root / "state.json"
        state.write_text(json.dumps(initial), encoding="utf-8")
        log = root / "tc.log"
        log.write_text("", encoding="utf-8")
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{fake_bin}:{Path(sys.executable).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
                "SYS_CLASS_NET_ROOT": str(sys_net),
                "TC_STATE": str(state),
                "TC_LOG": str(log),
            }
        )
        env.update(overrides)
        process = subprocess.run(
            ["/bin/sh", str(SCRIPT)],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
            timeout=10,
        )
        mutations = [json.loads(line) for line in log.read_text().splitlines()]
        return process.returncode, mutations


def base_state() -> dict[str, object]:
    return {
        "geometry": {"veth-test": "healthy", "ifb-tinyvless": "healthy"},
        "ingress": True,
        "filter": "healthy",
    }


def require(condition: bool, code: str) -> None:
    if not condition:
        raise SystemExit(code)


def main() -> int:
    code, mutations = run_case(base_state())
    require(code == 0 and not mutations, "healthy_state_was_mutated")

    old = base_state()
    old["geometry"] = {"veth-test": "old", "ifb-tinyvless": "old"}
    code, mutations = run_case(old)
    require(code == 0, "in_place_change_failed")
    require(
        len(mutations) == 2
        and all(item[:3] == ["qdisc", "change", "dev"] for item in mutations),
        "in_place_change_not_minimal",
    )

    missing = base_state()
    missing["geometry"] = {"veth-test": "missing", "ifb-tinyvless": "missing"}
    missing["ingress"] = False
    missing["filter"] = "missing"
    code, mutations = run_case(missing)
    require(code == 0, "missing_state_repair_failed")
    require(
        any(item[:3] == ["qdisc", "add", "dev"] for item in mutations)
        and any(item[:3] == ["filter", "add", "dev"] for item in mutations),
        "missing_state_was_not_rebuilt",
    )

    extra = base_state()
    extra["filter"] = "extra"
    code, mutations = run_case(extra)
    require(code == 0, "stale_filter_repair_failed")
    require(
        any(item[:3] == ["qdisc", "del", "dev"] and "ingress" in item for item in mutations)
        and not any(item[:3] == ["qdisc", "change", "dev"] for item in mutations),
        "stale_filter_was_not_replaced_safely",
    )

    failed = base_state()
    failed["geometry"] = {"veth-test": "old", "ifb-tinyvless": "old"}
    failed["change_fails"] = True
    failed["ignore_repairs"] = True
    code, _ = run_case(failed)
    require(code != 0, "failed_final_verification_was_accepted")

    code, mutations = run_case(base_state(), RATE="100.mbit")
    require(code != 0 and not mutations, "invalid_unit_was_accepted")

    print("tiny_vless_rate_limit_test=passed cases=6")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
