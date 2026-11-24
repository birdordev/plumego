#!/usr/bin/env python3
"""Run repeatable multi-process workloads on an isolated PostgreSQL cluster."""
import argparse
from collections import Counter
import concurrent.futures
import contextlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

from failover_drill import require, reserve_ports, run, wait_for
from smoke import stop

DIRECTORY = Path(__file__).resolve().parent


def cpu_seconds(value):
    days, separator, clock = value.partition("-")
    seconds = int(days) * 86400 if separator else 0
    for index, part in enumerate(reversed((clock if separator else days).split(":"))):
        seconds += float(part) * 60 ** index
    return seconds


class LoadCluster:
    def __init__(self, directory, stack):
        self.directory = directory
        self.stack = stack
        self.env = {key: value for key, value in os.environ.items() if not key.startswith(("PG", "SCHEDULER_"))}
        port, hot, cold = reserve_ports(3)
        self.ports = {"hot": hot, "cold": cold}
        data = directory / "pg"
        self.dsn = f"user=postgres dbname=postgres host=127.0.0.1 port={port} connect_timeout=2 sslmode=disable"
        run("initdb", "-D", data, "-U", "postgres", "-A", "trust", "--no-locale",
            stdout=subprocess.DEVNULL, env=self.env)
        run("pg_ctl", "-D", data, "-l", directory / "postgres.log",
            "-o", f"-p {port} -h 127.0.0.1 -c unix_socket_directories=''", "-w", "start",
            stdout=subprocess.DEVNULL, env=self.env)
        stack.callback(run, "pg_ctl", "-D", data, "-m", "immediate", "-w", "stop",
                       stdout=subprocess.DEVNULL, env=self.env)
        self.postmaster = int((data / "postmaster.pid").read_text().splitlines()[0])
        self.env.update(SCHEDULER_DATABASE_URL=self.dsn, SCHEDULER_API_TOKEN="local-load-token")
        self.binary = directory / "scheduler"
        run("go", "build", "-o", self.binary, ".", cwd=DIRECTORY, env=self.env)
        run(self.binary, "-role", "migrate", cwd=DIRECTORY, env=self.env)
        # 触发真实 Worker 重试，并使每个夹具实例的第一次业务提交失败。
        self.sql("""
            CREATE FUNCTION baseline_retry_once() RETURNS trigger LANGUAGE plpgsql AS $$
            BEGIN
                IF EXISTS (SELECT 1 FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
                    WHERE r.id = NEW.run_id AND r.request_key LIKE 'retry-%' AND q.execution_attempts = 1) THEN
                    RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'baseline transient failure';
                END IF;
                RETURN NEW;
            END $$;
            CREATE TRIGGER baseline_retry_once BEFORE INSERT ON scheduled_results
                FOR EACH ROW EXECUTE FUNCTION baseline_retry_once();
        """)
        self.processes = []
        for tenant, api_port in self.ports.items():
            self.start("api", api_port, SCHEDULER_TENANT=tenant)
        for _ in range(2):
            self.start("scheduler")
            self.start("worker", SCHEDULER_WORKER_POOL="short", SCHEDULER_WORKER_CONCURRENCY="4")
            self.start("worker", SCHEDULER_WORKER_POOL="io", SCHEDULER_WORKER_CONCURRENCY="2")
        self.start("finalizer")
        for tenant in self.ports:
            wait_for(lambda: self.request(tenant, "/readyz", expected=None)[0] == 200, tenant + " API")

    def sql(self, query, **variables):
        args = [arg for key, value in variables.items() for arg in ("-v", f"{key}={value}")]
        try:
            return run("psql", self.dsn, "-XqAt", "-v", "ON_ERROR_STOP=1", *args, env=self.env,
                       input=query, capture_output=True).stdout.strip()
        except subprocess.CalledProcessError as exc:
            raise RuntimeError(exc.stderr.strip()) from exc

    def start(self, role, port=0, **settings):
        log = self.stack.enter_context((self.directory / f"service-{len(self.processes)}.log").open("w"))
        cmd = subprocess.Popen([self.binary, "-role", role, "-addr", f"127.0.0.1:{port}"],
                               cwd=DIRECTORY, env={**self.env, **settings}, stdout=log, stderr=log)
        self.stack.callback(stop, cmd)
        self.processes.append(cmd)

    def request(self, tenant, path, body=None, expected=202):
        req = urllib.request.Request(f"http://127.0.0.1:{self.ports[tenant]}" + path,
                                     None if body is None else json.dumps(body).encode(),
                                     {"Authorization": "Bearer local-load-token", "Content-Type": "application/json"})
        try:
            response = urllib.request.urlopen(req, timeout=5)
        except urllib.error.HTTPError as exc:
            response = exc
        except urllib.error.URLError:
            if expected is None:
                return 0, {}
            raise
        with response:
            result = json.load(response)
            if expected is not None:
                require(response.status == expected, f"{tenant} {path}: {response.status}, {result}")
            return response.status, result

    def submit(self, scenario, tenant, count, topic, payload, rate=0):
        started = time.monotonic()
        for base in range(0, count, 16):
            if rate:
                time.sleep(max(0, started + base / rate - time.monotonic()))
            batch = [{"idempotency_key": f"{scenario}-{i}", "topic": topic, "payload": payload}
                     for i in range(base, min(base + 16, count))]
            _, result = self.request(tenant, "/runs/batch", {"runs": batch})
            require(len(result["data"]["ids"]) == len(batch), "batch was not fully accepted")

    def processes_sample(self):
        output = run("ps", "-axo", "pid=,ppid=,rss=,time=", capture_output=True).stdout
        service = {p.pid for p in self.processes}
        rows = {}
        for line in output.splitlines():
            pid, parent, rss, elapsed = line.split()
            pid, parent = int(pid), int(parent)
            if pid in service or pid == self.postmaster or parent == self.postmaster:
                rows[pid] = (int(rss), cpu_seconds(elapsed), pid in service)
        return rows


SAMPLE = """
    SELECT count(*) FILTER (WHERE q.status = 'done'),
        count(*) FILTER (WHERE q.status IN ('dead','expired','canceled')),
        count(*) FILTER (WHERE (q.status = 'queued' OR (q.status = 'leased' AND q.lease_until <= statement_timestamp()))
            AND q.available_at <= statement_timestamp()
            AND (q.expires_at IS NULL OR q.expires_at > statement_timestamp())
            AND NOT EXISTS (SELECT 1 FROM scheduler_blocked_claims b WHERE b.task_id = q.id)),
        count(*) FILTER (WHERE q.status IN ('queued','leased')),
        (SELECT COALESCE(jsonb_object_agg(kind, n), '{}'::jsonb) FROM (
            SELECT wait_event_type || ':' || wait_event AS kind, count(*) AS n
            FROM pg_stat_activity
            WHERE datname = current_database() AND pid <> pg_backend_pid()
                AND state = 'active' AND wait_event_type IS NOT NULL
            GROUP BY kind
        ) waits)
    FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
    WHERE left(r.request_key, length(:'prefix')) = :'prefix'
       OR (left(r.schedule_id, length(:'prefix')) = :'prefix' AND r.scheduled_at = :'fire'::timestamptz);
"""

LATENCY = """
WITH measured AS (
    SELECT r.tenant_id, q.execution_attempts, r.created_at, r.scheduled_at,
        q.updated_at, s.completed_at,
        (SELECT min(execution_started_at) FROM mq_task_attempts WHERE task_id = r.id) AS opened
    FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id JOIN scheduled_results s ON s.run_id = r.id
    WHERE left(r.request_key, length(:'prefix')) = :'prefix'
       OR (left(r.schedule_id, length(:'prefix')) = :'prefix' AND r.scheduled_at = :'fire'::timestamptz)
), grouped AS (
    SELECT COALESCE(tenant_id, 'all') AS label, count(*) AS completed,
        sum(execution_attempts - 1) AS retries,
        round((1000 * percentile_cont(.99) WITHIN GROUP (ORDER BY extract(epoch FROM (updated_at - scheduled_at))))::numeric, 3) AS due_to_confirm_p99_ms,
        round((1000 * percentile_cont(.99) WITHIN GROUP (ORDER BY extract(epoch FROM (opened - created_at))))::numeric, 3) AS created_to_start_p99_ms,
        round((1000 * percentile_cont(.99) WITHIN GROUP (ORDER BY extract(epoch FROM (completed_at - opened))))::numeric, 3) AS start_to_receipt_p99_ms,
        round((1000 * percentile_cont(.99) WITHIN GROUP (ORDER BY extract(epoch FROM (updated_at - completed_at))))::numeric, 3) AS receipt_to_confirm_p99_ms,
        round((1000 * percentile_cont(.99) WITHIN GROUP (ORDER BY extract(epoch FROM (created_at - scheduled_at))))::numeric, 3) AS creation_lag_p99_ms
    FROM measured GROUP BY GROUPING SETS ((tenant_id), ())
)
SELECT json_object_agg(label, to_jsonb(grouped) - 'label') FROM grouped;
"""


def exercise(cluster, name, count, produce, variables=None):
    variables = {"prefix": name + "-", "fire": "2000-01-01 UTC", **(variables or {})}
    wal_before, bytes_before = cluster.sql("SELECT pg_current_wal_insert_lsn(), pg_database_size(current_database());").split("|")
    first_processes = cluster.processes_sample()
    last_cpu = {pid: (elapsed, service) for pid, (_, elapsed, service) in first_processes.items()}
    peak_rss = [0, 0]
    peak_ready = peak_pending = 0
    completed_in_window = 0
    observed_waits = Counter()
    stop_at = None
    start = time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        producer = pool.submit(produce)
        while True:
            stamp = time.monotonic()
            counts = cluster.sql(SAMPLE, **variables).split("|", 4)
            done, failures, ready, pending = map(int, counts[:4])
            observed_waits.update(json.loads(counts[4]))
            require(failures == 0, f"{name}: {failures} terminal failures")
            peak_ready, peak_pending = max(peak_ready, ready), max(peak_pending, pending)
            values = cluster.processes_sample()
            for p, (_, elapsed, service) in values.items():
                last_cpu[p] = elapsed, service
            for i, app in enumerate((True, False)):
                peak_rss[i] = max(peak_rss[i], sum(rss for rss, _, belongs in values.values() if belongs == app))
            if producer.done() and stop_at is None:
                producer.result()
                stop_at = time.monotonic()
                completed_in_window = done
            if stop_at is not None and done == count:
                break
            require(stamp - start < 150, f"{name}: only {done}/{count} completed")
            time.sleep(0.05)
    end = time.monotonic()
    summary = json.loads(cluster.sql(LATENCY, **variables))
    wal, size = map(int, cluster.sql("""
        SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(), :'lsn'::pg_lsn)::bigint,
            pg_database_size(current_database());
    """, lsn=wal_before).split("|"))
    cpu = [sum(value - first_processes.get(pid, (0, 0, app))[1]
               for pid, (value, belongs) in last_cpu.items() if belongs == app) for app in (True, False)]
    return {"tasks": count, "elapsed_s": round(end - start, 3),
            "completed_per_second": round(count / (end - start), 2),
            "completed_during_admission_per_second": round(completed_in_window / (stop_at - start), 2),
            "drain_after_admission_s": round(max(0, end - stop_at), 3),
            "peak_sampled_runnable": peak_ready, "peak_sampled_pending": peak_pending,
            "app_cpu_s": round(cpu[0], 3), "pg_cpu_s": round(cpu[1], 3),
            "app_peak_rss_kib": peak_rss[0], "pg_peak_rss_kib": peak_rss[1],
            "wal_bytes": wal, "db_growth_bytes": size - int(bytes_before),
            "pg_active_wait_observations": dict(observed_waits), "latency": summary}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--short-rate", type=int, default=240)
    parser.add_argument("--short-seconds", type=int, default=5)
    parser.add_argument("--long-count", type=int, default=64)
    parser.add_argument("--storm-count", type=int, default=128)
    args = parser.parse_args()
    if not (1 <= args.short_rate <= 1500 and 1 <= args.short_seconds <= 30
            and 1 <= args.short_rate * args.short_seconds <= 10000
            and 4 <= args.long_count <= 1000 and 4 <= args.storm_count <= 500):
        parser.error("profile exceeds bounded local fixture sizes")

    with tempfile.TemporaryDirectory(prefix="plumego-load-") as temporary, contextlib.ExitStack() as stack:
        cluster = LoadCluster(Path(temporary), stack)
        report = {"host": platform.platform(), "cpu_count": os.cpu_count(),
                  "postgres": cluster.sql("SHOW server_version;"),
                  "go": run("go", "version", capture_output=True).stdout.strip(),
                  "workers": {"short": {"replicas": 2, "slots": 8}, "io": {"replicas": 2, "slots": 4}},
                  "scenarios": {}}
        rows = report["scenarios"]
        short_count = args.short_rate * args.short_seconds
        rows["short"] = exercise(cluster, "short", short_count,
                                 lambda: cluster.submit("short", "hot", short_count, "demo.record", {}, args.short_rate))
        rows["long_io"] = exercise(cluster, "long", args.long_count,
                                   lambda: cluster.submit("long", "hot", args.long_count, "demo.io", {"delay_ms": 250}))
        for i in range(args.storm_count):
            cluster.request("hot", "/schedules", {"id": f"storm-{i}", "expression": "@hourly",
                            "time_zone": "UTC", "topic": "demo.record", "payload": {}}, expected=201)
        fire = cluster.sql("SELECT (statement_timestamp() + interval '1 second')::text;")
        rows["timer_storm"] = exercise(cluster, "storm", args.storm_count, lambda: cluster.sql("""
            UPDATE schedules SET next_fire_at = :'fire'::timestamptz
            WHERE left(id, 6) = 'storm-';
        """, fire=fire), {"fire": fire})

        def hotspot():
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                hot = pool.submit(cluster.submit, "hotspot", "hot", 320, "demo.record", {})
                cold = pool.submit(cluster.submit, "hotspot", "cold", 32, "demo.record", {}, 40)
                hot.result()
                cold.result()

        rows["hot_tenant"] = exercise(cluster, "hotspot", 352, hotspot)
        rows["retry_storm"] = exercise(cluster, "retry", args.storm_count,
                                       lambda: cluster.submit("retry", "hot", args.storm_count, "demo.record", {}))
        require(rows["retry_storm"]["latency"]["all"]["retries"] == args.storm_count, "retry was not one per instance")
        require(all(p.poll() is None for p in cluster.processes), "a workload service exited")
    try:
        failover = run(sys.executable, DIRECTORY / "failover_drill.py", "--backlog", "64",
                       capture_output=True, env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
    except subprocess.CalledProcessError as exc:
        raise RuntimeError("failover fixture failed\n" + exc.stderr) from exc
    report["failover"] = json.loads(failover.stdout.splitlines()[-1])
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
