#!/usr/bin/env python3
"""Exercise real reducers in a disposable loopback database; never connects to production.

Authentication is replaced only in the temporary copy, so synthetic service and
member calls can use the local CLI identity. Product admission, result handling,
transactions, and deduplication are unchanged. No phone or external service runs.
"""
import os
import hashlib
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

WORKLOAD = Path(__file__).resolve().parents[1]
SOURCE = WORKLOAD / "spacetimedb"


def replace_function(source, name, body):
    start = source.index(f"fn {name}(")
    opening = source.index("{", start)
    depth, end = 1, opening + 1
    while depth:
        depth += (source[end] == "{") - (source[end] == "}")
        end += 1
    return source[:opening + 1] + body + source[end - 1:]


def run(arguments, *, cwd, expected_error=None):
    result = subprocess.run(arguments, cwd=cwd, capture_output=True, text=True)
    output = result.stdout + result.stderr
    if expected_error:
        assert result.returncode != 0 and expected_error in output, output
    elif result.returncode:
        raise RuntimeError(output)
    return output


def main():
    with tempfile.TemporaryDirectory(prefix="ticket-statistics-test-") as temporary:
        directory = Path(temporary)
        module = directory / "module"
        shutil.copytree(SOURCE / "src", module / "src")
        for filename in ("Cargo.toml", "Cargo.lock"):
            shutil.copy2(SOURCE / filename, module / filename)
        source = (module / "src/lib.rs").read_text()
        source = replace_function(source, "require_service", '\n    let _ = ctx; Ok(())\n')
        source = replace_function(source, "client_email_from_auth", '\n    let _ = (ctx, ticket_id); Ok("fixture@example.test".into())\n')
        source = replace_function(source, "identity_connected", '\n    let _ = ctx; Ok(())\n')
        source += "\n" + (SOURCE / "test-fixtures/action_statistics.rs").read_text()
        (module / "src/lib.rs").write_text(source)
        # Reuse only compiler caches, never production module output paths.
        env = {**os.environ, "CARGO_TARGET_DIR": str(SOURCE / "target/statistics-fixture")}
        wasm = SOURCE / "target/statistics-fixture/wasm32-unknown-unknown/release/ticket_remote_spacetimedb.wasm"
        def build_to(destination):
            build = subprocess.run(["spacetime", "build", "--module-path", str(module)], cwd=directory, env=env, capture_output=True, text=True)
            if build.returncode: raise RuntimeError(build.stdout + build.stderr)
            shutil.copy2(wasm, destination)
        current_wasm = directory / "statistics-current.wasm"
        baseline_wasm = directory / "statistics-before.wasm"
        build_to(current_wasm)
        (module / "src/lib.rs").write_text((SOURCE / "test-fixtures/action_statistics_before.rs").read_text())
        build_to(baseline_wasm)
        with socket.socket() as port_probe:
            port_probe.bind(("127.0.0.1", 0))
            port = port_probe.getsockname()[1]
        server_url = f"http://127.0.0.1:{port}"
        with (directory / "server.log").open("w") as log:
            server = subprocess.Popen(["spacetime", "start", "--listen-addr", f"127.0.0.1:{port}", "--data-dir", str(directory / "data"), "--in-memory", "--non-interactive"], stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        urllib.request.urlopen(server_url + "/v1/ping", timeout=1).close()
                        break
                    except OSError:
                        if server.poll() is not None: raise RuntimeError((directory / "server.log").read_text())
                        time.sleep(.1)
                args = ["--server", server_url, "--no-config", "--yes"]
                run(["spacetime", "publish", *args, "--bin-path", str(baseline_wasm), "--delete-data=never", "statistics-fixture"], cwd=directory)
                run(["spacetime", "call", *args, "statistics-fixture", "fixture_migration_seed"], cwd=directory)
                run(["spacetime", "publish", *args, "--bin-path", str(current_wasm), "--delete-data=never", "statistics-fixture"], cwd=directory)
                for case in ["migration", "registration", "queued", "queued-rejected", "rejected", "menu", "code", "queued-code", "original-hour", "expired", "rollback", "assert-rollback"]:
                    run(["spacetime", "call", *args, "statistics-fixture", "fixture_statistics_case", case], cwd=directory,
                        expected_error="fixture_expected_rollback" if case == "rollback" else None)
                    print(f"PASS {case}", flush=True)
                # The projection's actual service-identity gate is unchanged
                # in the fixture. An anonymous reader must see no usage rows.
                public = run(["spacetime", "sql", *args, "--anonymous", "statistics-fixture", "SELECT * FROM ticketremote_service_member_daily_actions"], cwd=directory)
                scope = hashlib.sha256(b"fixture@example.test").hexdigest()
                assert scope not in public, public
                private = subprocess.run(["spacetime", "sql", *args, "--anonymous", "statistics-fixture", "SELECT * FROM ticketremote_member_daily_actions"], cwd=directory, capture_output=True, text=True)
                assert private.returncode != 0, "anonymous reader could query the private action table"
                print("PASS private storage and service-only projection", flush=True)
            except Exception:
                logs = subprocess.run(["spacetime", "logs", "--server", server_url, "--no-config", "--num-lines", "12", "statistics-fixture"], cwd=directory, capture_output=True, text=True)
                print(logs.stdout, flush=True)
                raise
            finally:
                server.terminate()
                try: server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == "__main__":
    main()
