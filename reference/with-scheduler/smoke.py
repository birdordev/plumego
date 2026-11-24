#!/usr/bin/env python3
"""Exercise independent scheduler processes against a disposable database."""
import contextlib
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def stop(process):
    if process.poll() is None:
        process.send_signal(signal.SIGTERM)
    try:
        code = process.wait(timeout=15)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise
    if code != 0:
        raise RuntimeError(f"{process.args} exited with {code}")


def wait_for(predicate):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        found = predicate()
        if found:
            return found
        time.sleep(0.05)
    raise RuntimeError("scheduler smoke check timed out")


def main():
    dsn = os.environ.get("PLUMEGO_TEST_POSTGRES_DSN")
    if not dsn:
        raise RuntimeError("set PLUMEGO_TEST_POSTGRES_DSN to a disposable PostgreSQL database")
    env = {
        **os.environ,
        "SCHEDULER_DATABASE_URL": dsn,
        "SCHEDULER_API_TOKEN": "local-smoke-token",
        "SCHEDULER_TENANT": "smoke-tenant",
    }
    directory = Path(__file__).resolve().parent
    with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as stack:
        binary = str(Path(tmp) / "scheduler")
        subprocess.run(["go", "build", "-o", binary, "."], cwd=directory, env=env, check=True)
        for _ in range(2):
            subprocess.run([binary, "-role", "migrate"], cwd=directory, env=env, check=True)
        def allocate_address():
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", 0))
                return f"127.0.0.1:{probe.getsockname()[1]}"

        address = allocate_address()

        def start(role, *, listen=None, worker_versions=None):
            child_env = dict(env)
            if worker_versions is not None:
                child_env["SCHEDULER_WORKER_POOL"] = "short"
                child_env["SCHEDULER_WORKER_TASK_VERSIONS"] = worker_versions
            process = subprocess.Popen(
                [binary, "-role", role, "-addr", listen or address], cwd=directory, env=child_env,
            )
            stack.callback(stop, process)
            return process

        def request(path, data=None, key=None, authenticated=True, method=None, *, listen=None):
            headers = {"Content-Type": "application/json"}
            if authenticated:
                headers["Authorization"] = "Bearer " + env["SCHEDULER_API_TOKEN"]
            if key:
                headers["Idempotency-Key"] = key
            body = None if data is None else json.dumps(data).encode()
            req = urllib.request.Request("http://" + (listen or address) + path, body, headers, method=method)
            try:
                response = urllib.request.urlopen(req, timeout=1)
            except urllib.error.HTTPError as exc:
                response = exc
            except urllib.error.URLError:
                return 0, {}, {}
            with response:
                return response.status, response.headers, {} if response.status == 204 else json.load(response)

        original_api = start("api")
        wait_for(lambda: request("/readyz")[0] == 200)
        assert request("/runs", {}, authenticated=False)[0] == 401
        payload = {"topic": "demo.record", "payload": {"message": "smoke"}}
        code, headers, _ = request("/runs", payload, "smoke-request")
        assert code == 202, code
        uri = headers["Location"]
        assert request("/runs", payload, "smoke-request")[1]["Location"] == uri
        assert request("/runs", {**payload, "payload": {}}, "smoke-request")[0] == 409
        assert request("/runs/unknown")[0] == 404
        cancel_payload = {**payload, "run_at": "2099-01-01T00:00:00Z"}
        code, headers, _ = request("/runs", cancel_payload, "smoke-cancel")
        assert code == 202
        canceled_uri = headers["Location"]
        assert request(canceled_uri + "/cancel", {})[0] == 204
        assert request(canceled_uri)[2]["data"]["queue_status"] == "canceled"

        schedule = {**payload, "id": "smoke-cron", "expression": "@every 1s", "time_zone": "UTC"}
        assert request("/schedules", schedule)[0] == 201
        route = "/schedules/smoke-cron"
        code, trigger_headers, _ = request(route + "/trigger", {}, "manual-once")
        assert code == 202
        manual_uri = trigger_headers["Location"]
        replacement = {**schedule, "payload": {"message": "updated"}, "version": 1}
        assert request(route, replacement, method="PUT")[0] == 200
        assert request(route, replacement, method="PUT")[0] == 409
        assert request(route + "/trigger", {}, "manual-once")[1]["Location"] == manual_uri
        manual = request(manual_uri)[2]["data"]
        assert manual["manual_trigger"] and manual["schedule_version"] == 1
        first = start("scheduler")
        start("finalizer")
        old_worker = start("worker", worker_versions="1")
        old_replica = start("worker", worker_versions="1")
        start("outbox")

        def done():
            code, _, data = request(uri)
            return code == 200 and data["data"]["queue_status"] == "done" and data["data"]["result_ready"]

        wait_for(done)
        assert request(uri + "/cancel", {})[0] == 409
        code, _, data = request("/runs?limit=1")
        assert code == 200 and len(data["data"]["items"]) == 1
        after = data["data"]["next_cursor"]
        code, _, data = request("/runs?limit=1&cursor=" + after)
        assert code == 200 and len(data["data"]["items"]) == 1
        assert request("/runs?limit=101")[0] == 400

        def last_fire():
            code, _, data = request("/schedules/smoke-cron")
            return code == 200 and data["data"].get("last_fired_at")

        wait_for(last_fire)
        stop(first)
        route = "/schedules/smoke-cron"
        assert request(route, {"paused": True, "version": 2}, method="PATCH")[0] == 200
        assert request(route, {"paused": False, "version": 1}, method="PATCH")[0] == 409
        last_before_restart = last_fire()
        assert last_before_restart
        scheduler_replica = start("scheduler")
        replacement_scheduler = start("scheduler")
        time.sleep(1.2)
        assert last_fire() == last_before_restart
        assert request(route, {"paused": False, "version": 3}, method="PATCH")[0] == 200
        wait_for(lambda: last_fire() not in (False, None, last_before_restart))
        stop(scheduler_replica)
        before_takeover = last_fire()
        wait_for(lambda: last_fire() not in (False, None, before_takeover))
        assert done()

        new_address = allocate_address()
        start("api", listen=new_address)
        wait_for(lambda: request("/readyz", listen=new_address)[0] == 200)
        assert request("/runs", payload, "smoke-request", listen=new_address)[1]["Location"] == uri
        stop(original_api)
        address = new_address
        assert done()

        def submit(version, key):
            code, headers, _ = request("/runs", {**payload, "task_version": version}, key)
            assert code == 202, code
            return headers["Location"]

        def run_completed(run_uri):
            code, _, data = request(run_uri)
            return code == 200 and data["data"]["queue_status"] == "done" and data["data"]["result_ready"]

        new_run = submit(2, "worker-rollout-new-version")
        submit_probe = submit(1, "worker-rollout-legacy-before-replacement")
        wait_for(lambda: run_completed(submit_probe))
        assert request(new_run)[2]["data"]["queue_status"] == "queued"
        start("worker", worker_versions="1,2")
        wait_for(lambda: run_completed(new_run))
        stop(old_worker)
        stop(old_replica)
        legacy_run = submit(1, "worker-rollout-legacy-after-replacement")
        current_run = submit(2, "worker-rollout-current-after-replacement")
        wait_for(lambda: run_completed(legacy_run) and run_completed(current_run))
        assert replacement_scheduler.poll() is None
        assert request("/readyz")[0] == 200
        print("migration, API replica handoff, scheduler takeover, and versioned Worker rollout passed")


if __name__ == "__main__":
    main()
