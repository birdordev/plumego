#!/usr/bin/env python3
"""Exercise a fenced PostgreSQL promotion with live scheduler processes."""
import argparse
import contextlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

from smoke import stop


def require(condition, description):
    if not condition:
        raise RuntimeError(description)


def wait_for(predicate, description, timeout=25):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError("timed out waiting for " + description)


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, timeout=60, **kwargs)


def reserve_ports(count):
    with contextlib.ExitStack() as stack:
        sockets = [stack.enter_context(socket.socket()) for _ in range(count)]
        for sock in sockets:
            sock.bind(("127.0.0.1", 0))
        return [sock.getsockname()[1] for sock in sockets]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--backlog", type=int, default=0)
    args = parser.parse_args()
    if not 0 <= args.backlog <= 100:
        parser.error("backlog must be between 0 and 100")
    directory = Path(__file__).resolve().parent
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("PG", "SCHEDULER_"))}
    env["PGGSSENCMODE"] = "disable"
    primary_port, standby_port, api_port = reserve_ports(3)
    database = "user=postgres dbname=postgres sslmode=disable connect_timeout=2"

    def dsn(port):
        return f"{database} host=127.0.0.1 port={port}"

    def sql(port, query, **variables):
        parameters = [arg for key, val in variables.items() for arg in ("-v", f"{key}={val}")]
        return run("psql", dsn(port), "-XqAt", "-v", "ON_ERROR_STOP=1", *parameters,
                   input=query, capture_output=True, env=env).stdout.strip()

    with tempfile.TemporaryDirectory(prefix="plumego-failover-") as temp, contextlib.ExitStack() as stack:
        root = Path(temp)
        primary, standby = root / "primary", root / "standby"

        def ctl(node, *args):
            return run("pg_ctl", "-D", node, "-t", "15", *args, env=env, stdout=subprocess.DEVNULL)

        def stop_database(node):
            result = subprocess.run(["pg_ctl", "-D", node, "status"], env=env,
                                    capture_output=True, timeout=5)
            if result.returncode == 0:
                ctl(node, "-m", "immediate", "-w", "stop")
            elif result.returncode not in (3, 4):
                result.check_returncode()

        def start_database(node, port):
            stack.callback(stop_database, node)
            ctl(node, "-l", root / (node.name + ".log"), "-o",
                f"-p {port} -h 127.0.0.1 -c unix_socket_directories=''", "-w", "start")

        def start_standby(node, source_port, port):
            run("pg_basebackup", "-d", dsn(source_port) + " application_name=scheduler_drill",
                "-D", node, "-R", "-X", "stream", "-c", "fast", env=env)
            start_database(node, port)

        def enable_sync(writer):
            sql(writer, "ALTER SYSTEM SET synchronous_standby_names = 'FIRST 1 (scheduler_drill)';")
            sql(writer, "ALTER SYSTEM SET synchronous_commit = 'remote_apply';")
            sql(writer, "SELECT pg_reload_conf();")
            wait_for(lambda: sql(writer, """
                SELECT current_setting('synchronous_commit') = 'remote_apply'
                    AND EXISTS (SELECT 1 FROM pg_stat_replication
                        WHERE application_name = 'scheduler_drill' AND sync_state = 'sync'
                            AND state = 'streaming' AND replay_lsn IS NOT NULL);
            """) == "t", "synchronous standby")

        def assert_fenced(node):
            result = subprocess.run(["pg_ctl", "-D", node, "status"], env=env,
                                    capture_output=True, timeout=5)
            require(result.returncode == 3, "old writer is not confirmed stopped")

        run("initdb", "-D", primary, "-U", "postgres", "-A", "trust", "--no-locale",
            env=env, stdout=subprocess.DEVNULL)
        start_database(primary, primary_port)
        start_standby(standby, primary_port, standby_port)
        enable_sync(primary_port)

        binary = root / "scheduler"
        run("go", "build", "-o", binary, ".", cwd=directory, env=env)
        probe = root / "scheduler-lease-probe"
        run("go", "test", "-c", "-o", probe, ".", cwd=directory, env=env)
        env.update(
            SCHEDULER_DATABASE_URL=f"{database} host=127.0.0.1,127.0.0.1 "
                                   f"port={primary_port},{standby_port} target_session_attrs=read-write",
            SCHEDULER_API_TOKEN="local-drill-token",
            SCHEDULER_TENANT="failover-drill",
            SCHEDULER_SYNC_STANDBY="scheduler_drill",
            SCHEDULER_OUTBOX_TOKEN="local-receiver-token",
        )
        run(binary, "-role", "migrate", cwd=directory, env=env)

        def start_role(role, **overrides):
            process = subprocess.Popen([binary, "-role", role, "-addr", f"127.0.0.1:{api_port}"],
                                       cwd=directory, env={**env, **overrides})
            stack.callback(lambda: stop(process) if process.poll() is None else None)
            return process

        def request(path, data=None, key=""):
            req = urllib.request.Request(
                f"http://127.0.0.1:{api_port}" + path,
                None if data is None else json.dumps(data).encode(),
                {"Authorization": "Bearer " + env["SCHEDULER_API_TOKEN"],
                 "Content-Type": "application/json", "Idempotency-Key": key},
            )
            try:
                response = urllib.request.urlopen(req, timeout=4)
            except urllib.error.HTTPError as exc:
                response = exc
            except (urllib.error.URLError, TimeoutError):
                return 0, {}
            with response:
                return response.status, json.load(response)

        def submit(key, topic="demo.record", payload=None):
            status, body = request("/runs", {"topic": topic, "payload": payload or {}}, key)
            require(status == 202, f"submission {key} returned {status}")
            return body["data"]["id"]

        def state(run_id):
            status, body = request("/runs/" + run_id)
            return body["data"] if status == 200 else {}

        api = start_role("api")
        scheduler = start_role("scheduler")
        finalizer = start_role("finalizer")
        short_worker = start_role("worker", SCHEDULER_WORKER_POOL="short")
        wait_for(lambda: request("/readyz")[0] == 200, "API readiness")
        sql(primary_port, "INSERT INTO scheduler_inventory VALUES ('failover-drill','drill-sku',20);")
        stock = {"operation_id": "drill-order", "action": "reserve", "sku": "drill-sku", "quantity": 6}
        event = submit("outbox", "demo.inventory", stock)
        wait_for(lambda: state(event).get("queue_status") == "done", "committed result")
        require(request("/schedules", {
            "id": "drill", "expression": "@every 2s", "time_zone": "UTC",
            "topic": "demo.record", "payload": {},
        })[0] == 201, "plan creation")
        wait_for(lambda: request("/schedules/drill")[1].get("data", {}).get("last_fired_at"),
                 "first planned instance")
        old_worker = start_role("worker", SCHEDULER_WORKER_POOL="io",
                                SCHEDULER_WORKER_LEASE_DURATION="5s")
        leased = submit("leased", "demo.io", {"delay_ms": 4000})
        wait_for(lambda: state(leased).get("execution_attempts") == 1, "live execution")
        old_worker.kill()
        old_worker.wait(timeout=5)
        former_lease = sql(primary_port, "SELECT lease_owner FROM mq_tasks WHERE id=:'id';", id=leased)
        require(bool(former_lease), "the former worker had no recorded lease")

        receipts = root / "receiver.sqlite"
        with contextlib.closing(sqlite3.connect(receipts)) as db, db:
            db.executescript("""
                CREATE TABLE deliveries (event_id TEXT NOT NULL);
                CREATE TABLE effects (event_id TEXT PRIMARY KEY);
                CREATE TABLE inventory (sku TEXT PRIMARY KEY, reserved INTEGER NOT NULL);
                INSERT INTO inventory VALUES ('drill-sku', 0);
            """)
        received = threading.Event()
        disconnect = threading.Event()
        stack.callback(disconnect.set)

        class Receiver(BaseHTTPRequestHandler):
            def do_POST(self):
                delivery = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                event_id = delivery["id"]
                if (self.headers.get("Idempotency-Key") != event_id or
                        self.headers.get("Authorization") != "Bearer " + env["SCHEDULER_OUTBOX_TOKEN"]):
                    self.send_error(400)
                    return
                if event_id == event and delivery.get("inventory") != {"tenant": "failover-drill", **stock}:
                    self.send_error(409)
                    return
                with contextlib.closing(sqlite3.connect(receipts)) as db, db:
                    db.execute("INSERT INTO deliveries VALUES (?)", (event_id,))
                    effect = db.execute("INSERT OR IGNORE INTO effects VALUES (?)", (event_id,))
                    first = effect.rowcount == 1
                    if event_id == event and first:
                        db.execute("UPDATE inventory SET reserved = reserved + ? WHERE sku = ?",
                                   (stock["quantity"], stock["sku"]))
                if event_id == event and first:
                    received.set()
                    disconnect.wait(15)
                    return
                self.send_response(204)
                self.end_headers()

            def log_message(self, *args):
                pass

        server = stack.enter_context(ThreadingHTTPServer(("127.0.0.1", 0), Receiver))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        stack.callback(server.shutdown)
        backlog_lsn = sql(primary_port, "SELECT pg_current_wal_insert_lsn();")
        if args.backlog:
            short_worker.send_signal(signal.SIGSTOP)
            stack.callback(lambda: short_worker.send_signal(signal.SIGCONT) if short_worker.poll() is None else None)
            payload = {"runs": [
                {"idempotency_key": f"failover-load-{i}", "topic": "demo.record", "payload": {}}
                for i in range(args.backlog)
            ]}
            status, response = request("/runs/batch", payload)
            require(status == 202 and len(response["data"]["ids"]) == args.backlog,
                    "failover backlog was not accepted")
        outbox = start_role("outbox", SCHEDULER_OUTBOX_URL=f"http://127.0.0.1:{server.server_port}/events")
        require(received.wait(15), "receiver did not commit the first effect")
        horizon = sql(primary_port, "SELECT pg_current_wal_flush_lsn();")
        wait_for(lambda: sql(standby_port,
            "SELECT pg_last_wal_replay_lsn() >= :'horizon'::pg_lsn;", horizon=horizon) == "t",
            "candidate's protected commit horizon")
        cut = time.monotonic()
        stop_database(primary)
        assert_fenced(primary)
        disconnect.set()
        require(request("/readyz")[0] == 503, "standby was accepted by the writable connection")
        require(request("/runs/" + event)[0] == 503, "read during the cut did not return unavailable")
        require(request("/runs", {"topic": "demo.record", "payload": {}}, "during-cut")[0] == 503,
                "write during the cut did not return unavailable")

        require(sql(standby_port, "SELECT pg_is_in_recovery();") == "t", "replica was not in recovery")
        pending_query = """
            SELECT count(*) FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
            WHERE r.request_key LIKE 'failover-load-%' AND q.status = 'queued';
        """
        pending_at_cut = int(sql(standby_port, pending_query))
        require(pending_at_cut == args.backlog, "acknowledged failover backlog changed or was lost")
        plan_count_query = "SELECT count(*) FROM scheduled_runs WHERE schedule_id = 'drill';"
        plan_count_at_cut = int(sql(standby_port, plan_count_query))
        ctl(standby, "-w", "promote")
        require(sql(standby_port, "SELECT NOT pg_is_in_recovery();") == "t", "new database writer unavailable")
        writer_ready_seconds = time.monotonic() - cut
        require(request("/readyz")[0] == 503, "API advertised readiness before restoring replication")
        require(request("/runs", {"topic": "demo.record", "payload": {}}, "during-cut")[0] == 503,
                "API accepted a command before restoring synchronous protection")
        rebuilt = root / "rebuilt"
        start_standby(rebuilt, standby_port, primary_port)
        require(sql(primary_port, "SELECT pg_is_in_recovery();") == "t", "returned old endpoint is writable")
        write = subprocess.run(["psql", dsn(primary_port), "-XqAt", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=verbose",
                                "-c", "UPDATE scheduler_schema SET version = version"],
                               text=True, capture_output=True, env=env, timeout=5)
        require(write.returncode != 0 and "25006" in write.stderr,
                "returned old endpoint did not reject a write during recovery")
        enable_sync(standby_port)
        protected_seconds = time.monotonic() - cut
        wait_for(lambda: request("/readyz")[0] == 200, "API readiness on the new primary")
        ready_seconds = time.monotonic() - cut
        if args.backlog:
            short_worker.send_signal(signal.SIGCONT)
        require(sql(standby_port, """
            SELECT count(*) FROM scheduled_runs WHERE id IN (:'event', :'leased');
        """, event=event, leased=leased) == "2", "acknowledged instance lost")
        require(sql(standby_port, """
            SELECT status, attempts, execution_attempts, lease_owner IS NOT NULL
            FROM mq_tasks WHERE id = :'id';
        """, id=leased) == "leased|1|1|t", "original claim did not survive promotion")
        require(sql(standby_port, """
            SELECT o.delivered_at IS NULL, o.lease_token IS NOT NULL, r.run_id IS NOT NULL
            FROM scheduled_outbox o JOIN scheduled_results r USING (run_id) WHERE run_id = :'id';
        """, id=event) == "t|t|t", "outbox lease or result did not survive promotion")
        require(submit("outbox", "demo.inventory", stock) == event, "idempotency entry changed after promotion")
        require(request("/runs/" + event + "/result")[0] == 200, "old result was not readable")
        during = submit("during-cut")
        first_confirmed_seconds = time.monotonic() - cut
        require(submit("during-cut") == during, "outage retry created duplicate instances")
        replacement = start_role("worker", SCHEDULER_WORKER_POOL="io",
                                 SCHEDULER_WORKER_LEASE_DURATION="5s")
        wait_for(lambda: (current := state(leased)).get("queue_status") == "leased"
                 and current.get("execution_attempts") == 2, "live replacement claim")
        run(probe, "-test.run=^TestPostgresFailoverRejectsFormerLease$", "-test.timeout=6s",
            env={**env, "PLUMEGO_TEST_FAILOVER_DATABASE_URL": dsn(standby_port),
                 "PLUMEGO_TEST_FAILOVER_RUN_ID": leased, "PLUMEGO_TEST_FAILOVER_LEASE_OWNER": former_lease},
            stdout=subprocess.DEVNULL)
        recovered = wait_for(lambda: (result if (result := state(leased)).get("queue_status") == "done"
                                     else None), "recovered leased task")
        lease_recovered_seconds = time.monotonic() - cut
        require(recovered["claim_attempts"] == 2 and recovered["execution_attempts"] == 2,
                "recovery did not use exactly one replacement claim")
        wait_for(lambda: sql(standby_port, """
            SELECT delivered_at IS NOT NULL AND lease_token IS NULL FROM scheduled_outbox WHERE run_id = :'id';
        """, id=event) == "t", "durable re-delivery acknowledgement")
        outbox_confirmed_seconds = time.monotonic() - cut
        wait_for(lambda: int(sql(standby_port, plan_count_query)) > plan_count_at_cut,
                 "schedule advancing after promotion")
        wait_for(lambda: state(during).get("queue_status") == "done", "post-promotion write and execution")
        with contextlib.closing(sqlite3.connect(receipts)) as db:
            deliveries = db.execute("SELECT count(*) FROM deliveries WHERE event_id = ?", (event,)).fetchone()[0]
            effects = db.execute("SELECT count(*) FROM effects WHERE event_id = ?", (event,)).fetchone()[0]
            reserved = db.execute("SELECT reserved FROM inventory WHERE sku = ?", (stock["sku"],)).fetchone()[0]
        require(deliveries >= 2 and effects == 1 and reserved == stock["quantity"],
                "delivery and the committed inventory effect did not reconcile")
        require(all(process.poll() is None for process in
                    (api, scheduler, finalizer, short_worker, replacement, outbox)), "a role exited")
        report = {
            "mode": "local",
            "acknowledged_runs_lost": 0, "acknowledged_runs_checked": 2 + args.backlog,
            "api_ready_seconds": round(ready_seconds, 3),
            "writer_ready_seconds": round(writer_ready_seconds, 3),
            "synchronous_protection_restored_seconds": round(protected_seconds, 3),
            "first_new_ack_seconds": round(first_confirmed_seconds, 3),
            "lease_recovered_seconds": round(lease_recovered_seconds, 3),
            "outbox_confirmed_seconds": round(outbox_confirmed_seconds, 3),
            "old_endpoint_rejected_write": True,
            "old_worker_rejected_write": True, "stale_writes_accepted": 0,
            "commit_horizon_verified": True, "admission_paused_without_sync": True,
            "fully_reconciled_seconds": round(time.monotonic() - cut, 3),
            "outbox_deliveries": deliveries, "duplicate_deliveries": deliveries - 1,
            "receiver_effects": effects, "duplicate_effects": effects - 1,
            "receiver_reserved_units": reserved,
            "recovered_task_claims": recovered["claim_attempts"],
        }
        if args.backlog:
            finished = """
                SELECT count(*) FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
                WHERE r.request_key LIKE 'failover-load-%' AND q.status = 'done';
            """
            wait_for(lambda: int(sql(standby_port, finished)) == args.backlog, "failover backlog drains")
            report["backlog_drained_seconds"] = round(time.monotonic() - cut, 3)
            backlog_p99, backlog_seconds, wal_bytes = sql(standby_port, """
                SELECT round((1000 * percentile_cont(.99)
                    WITHIN GROUP (ORDER BY extract(epoch FROM q.updated_at-r.scheduled_at)))::numeric, 3),
                    extract(epoch FROM max(q.updated_at)-min(r.scheduled_at)),
                    pg_wal_lsn_diff(pg_current_wal_insert_lsn(), :'lsn'::pg_lsn)::bigint
                FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
                WHERE r.request_key LIKE 'failover-load-%';
            """, lsn=backlog_lsn).split("|")
            report.update(peak_confirmed_backlog=pending_at_cut, backlog_p99_ms=float(backlog_p99),
                          backlog_per_second=round(args.backlog / float(backlog_seconds), 2),
                          cluster_wal_bytes=int(wal_bytes), fully_reconciled_seconds=round(time.monotonic() - cut, 3))
        second_horizon = sql(standby_port, "SELECT pg_current_wal_flush_lsn();")
        wait_for(lambda: sql(primary_port, "SELECT pg_last_wal_replay_lsn() >= :'horizon'::pg_lsn;",
                             horizon=second_horizon) == "t", "second protected commit horizon")
        second_cut = time.monotonic()
        stop_database(standby)
        assert_fenced(standby)
        ctl(rebuilt, "-w", "promote")
        require(sql(primary_port, "SELECT NOT pg_is_in_recovery();") == "t", "second protected promotion failed")
        require(sql(primary_port, """
            SELECT count(*) FROM scheduled_runs r JOIN scheduled_results s ON s.run_id=r.id
            WHERE r.id IN (:'old_event',:'old_lease',:'restored');
        """, old_event=event, old_lease=leased, restored=during) == "3", "second cut lost an acknowledged result")
        require(int(sql(primary_port, pending_query)) == 0,
                "second cut reintroduced terminal backlog")
        require(sql(primary_port, """
            SELECT available FROM scheduler_inventory WHERE tenant_id='failover-drill' AND sku='drill-sku';
        """) == "14", "second cut did not preserve the committed inventory effect")
        if args.backlog:
            require(int(sql(primary_port, finished)) == args.backlog,
                    "second cut lost an acknowledged backlog result")
        require(request("/readyz")[0] == 503, "second cut bypassed the replication admission gate")
        report["second_writer_ready_seconds"] = round(time.monotonic() - second_cut, 3)
        report["second_cut_results_checked"] = 3 + args.backlog
        print(json.dumps(report, sort_keys=True))


if __name__ == "__main__":
    main()
