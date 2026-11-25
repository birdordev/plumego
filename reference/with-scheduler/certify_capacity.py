#!/usr/bin/env python3
"""Certify bounded mixed scheduler workloads against an isolated deployment."""

import argparse
from collections import Counter, defaultdict
import concurrent.futures
import contextlib
import copy
from datetime import datetime, timedelta, timezone
import http.client
import json
import math
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from load_baseline import LoadCluster
from smoke import stop

LANES = {
    "hot": ("hot", "demo.record"),
    "cold": ("cold", "demo.record"),
    "cpu": ("hot", "demo.compute"),
    "io": ("hot", "demo.io"),
    "retry": ("hot", "demo.record"),
}
PHASES = ("control", "sustained", "burst", "soak", "degraded")
PHASE_LIMITS = {"min_completed_per_second", "max_backlog_growth", "max_drain_seconds", "min_archived_rows"}
LANE_LIMITS = {"max_api_p99_ms", "max_offer_p99_ms", "max_queue_p99_ms",
               "max_terminal_p99_ms", "max_oldest_runnable_ms", "max_rejected_fraction"}
TIMER_LIMITS = {"max_dispatch_p99_ms", "max_queue_p99_ms", "max_terminal_p99_ms",
                "max_oldest_runnable_ms"}
LOCAL_LIMITS = {
    "phase": {"min_completed_per_second": 1, "max_backlog_growth": 300,
              "max_drain_seconds": 20, "min_archived_rows": 0},
    "lanes": {lane: {key: (0 if key == "max_rejected_fraction" else 15000) for key in LANE_LIMITS}
              for lane in LANES},
    "timer": {key: 15000 for key in TIMER_LIMITS},
}
LOCAL_LIMITS["lanes"]["cold"].update(max_queue_p99_ms=500, max_terminal_p99_ms=750, max_oldest_runnable_ms=750)

TOPICS_SQL = """
WITH selected AS MATERIALIZED (
    SELECT r.id, r.created_at, r.scheduled_at, r.tenant_id, r.schedule_id, q.status,
        q.updated_at, q.available_at, q.execution_attempts, q.lease_until, q.expires_at,
        CASE WHEN r.request_key IS NULL THEN 'timer'
            ELSE split_part(r.request_key, '-', 3) END AS lane
    FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
    WHERE (r.tenant_id = :'hot' OR r.tenant_id = :'cold')
        AND ((r.request_key >= :'prefix' AND r.request_key < :'prefix' || '~')
          OR (r.request_key IS NULL AND r.schedule_id >= :'prefix' AND r.schedule_id < :'prefix' || '~'
              AND r.scheduled_at = NULLIF(:'fire', '')::timestamptz))
)
"""

SNAPSHOT = TOPICS_SQL + """
SELECT COALESCE(json_object_agg(lane, row_to_json(t)), '{}'::json)
FROM (
    SELECT lane, count(*) AS persisted,
        count(*) FILTER (WHERE status = 'done') AS done,
        count(*) FILTER (WHERE status NOT IN ('queued', 'leased', 'done')) AS failed,
        count(*) FILTER (WHERE status IN ('queued', 'leased')) AS pending,
        MAX(1000 * EXTRACT(epoch FROM statement_timestamp() - effective_ready_at))
            FILTER (WHERE effective_ready_at <= statement_timestamp()
                    AND (expires_at IS NULL OR expires_at > statement_timestamp())
                    AND NOT EXISTS (SELECT 1 FROM scheduler_blocked_claims b WHERE b.task_id = ready.id))
            AS oldest_runnable_ms
    FROM (SELECT *, CASE WHEN status = 'queued' THEN available_at
        WHEN status = 'leased' AND lease_until <= statement_timestamp()
            THEN greatest(available_at, lease_until) END AS effective_ready_at FROM selected) ready
    GROUP BY lane
) t;
"""

DETAIL = TOPICS_SQL + """
SELECT COALESCE(json_object_agg(lane, row_to_json(t)), '{}'::json)
FROM (
    SELECT lane,
        count(*) FILTER (WHERE status = 'done' AND updated_at >= :'window_start'::timestamptz
            AND updated_at <= :'window_end'::timestamptz) AS completed_during_offer,
        count(*) FILTER (WHERE status = 'done' AND started_at IS NOT NULL AND completed_at IS NOT NULL)
            AS terminal_samples,
        COALESCE(sum(greatest(execution_attempts - 1, 0)), 0) AS retries,
        count(*) FILTER (WHERE execution_attempts > 1) AS retried_runs,
        round(1000 * (percentile_cont(.99) WITHIN GROUP (
            ORDER BY extract(epoch FROM greatest(started_at - created_at, interval '0'))) FILTER (
                WHERE started_at IS NOT NULL))::numeric, 3) AS queue_p99_ms,
        round(1000 * (percentile_cont(.99) WITHIN GROUP (
            ORDER BY extract(epoch FROM greatest(created_at - scheduled_at -
                scheduler_jitter_milliseconds(tenant_id, schedule_id, :'jitter'::int) * interval '1 millisecond',
                interval '0')))
            FILTER (WHERE lane = 'timer'))::numeric, 3) AS dispatch_p99_ms,
        round(1000 * (percentile_cont(.99) WITHIN GROUP (
            ORDER BY extract(epoch FROM greatest(updated_at - scheduled_at, interval '0')))
            FILTER (WHERE status = 'done'))::numeric, 3) AS terminal_p99_ms
    FROM (
        SELECT s.*, r.completed_at, (SELECT min(a.execution_started_at) FROM scheduler_attempt_history a
            WHERE a.task_id = s.id AND a.execution_started_at IS NOT NULL) AS started_at
        FROM selected s LEFT JOIN scheduled_results r ON r.run_id = s.id
    ) measured GROUP BY lane
) t;
"""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(endpoint, token, path, body=None, method=None):
    req = urllib.request.Request(endpoint + path,
                                 None if body is None else json.dumps(body).encode(),
                                 {"Authorization": "Bearer " + token, "Content-Type": "application/json"},
                                 method=method)
    try:
        response = urllib.request.build_opener(NoRedirect).open(req, timeout=3)
    except urllib.error.HTTPError as exc:
        response = exc
    except urllib.error.URLError as exc:
        return ("timeout" if isinstance(exc.reason, TimeoutError) else "transport_error"), None
    except TimeoutError:
        return "timeout", None
    except (OSError, http.client.HTTPException):
        return "transport_error", None
    with response:
        try:
            return response.status, json.load(response)
        except (ValueError, OSError, http.client.HTTPException):
            return response.status, None


class Target:
    def __init__(self):
        self.tenants = {group: os.environ["SCHEDULER_CERT_" + group.upper() + "_TENANT"]
                        for group in ("hot", "cold")}
        if len(set(self.tenants.values())) != 2 or not all(self.tenants.values()):
            raise ValueError("certification requires two distinct test tenants")
        self.api = {}
        for group in self.tenants:
            prefix = "SCHEDULER_CERT_" + group.upper()
            endpoint = os.environ[prefix + "_URL"].rstrip("/")
            parsed = urllib.parse.urlsplit(endpoint)
            loopback = parsed.hostname in ("localhost", "127.0.0.1", "::1")
            if not (parsed.hostname and (parsed.scheme == "https" or (parsed.scheme == "http" and loopback))
                    and not (parsed.username or parsed.password or parsed.query or parsed.fragment)):
                raise ValueError("test API URL must use HTTPS or loopback HTTP without inline credentials")
            self.api[group] = endpoint, os.environ[prefix + "_TOKEN"]
        if not all(token for _, token in self.api.values()):
            raise ValueError("test API tokens must be nonempty")
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith(("PG", "SCHEDULER_CERT_"))}
        self.env.update(postgres_environment(os.environ["SCHEDULER_CERT_READ_DSN"]))

    def request(self, group, path, body=None, method=None):
        return request(*self.api[group], path, body, method)

    def sql(self, query, **variables):
        args = [arg for key, value in variables.items() for arg in ("-v", f"{key}={value}")]
        result = subprocess.run(["psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=verbose", *args],
                                input=query, text=True, capture_output=True, env=self.env, timeout=10)
        if result.returncode:
            state = re.search(r"\b(?:ERROR|FATAL):\s+([A-Z0-9]{5}):", result.stderr)
            suffix = " (SQLSTATE " + state.group(1) + ")" if state else ""
            raise RuntimeError("read-only certification query failed" + suffix)
        return result.stdout.strip()


def postgres_environment(dsn):
    # libpq 通过环境变量读取连接参数。凭据不会出现在子进程命令行。
    allowed = {"host", "hostaddr", "port", "dbname", "user", "password", "passfile",
               "sslmode", "sslrootcert", "sslcert", "sslkey", "gssencmode", "connect_timeout",
               "target_session_attrs", "application_name"}
    try:
        if dsn.startswith(("postgres://", "postgresql://")):
            url = urllib.parse.urlsplit(dsn)
            parts = [
                ("host", url.hostname), ("port", str(url.port) if url.port else None),
                ("user", url.username), ("password", url.password), ("dbname", url.path[1:] or None),
            ]
            parts = [(key, urllib.parse.unquote(value)) for key, value in parts if value is not None]
            parts.extend(urllib.parse.parse_qsl(url.query, strict_parsing=True))
        else:
            parts = [tuple(term.split("=", 1)) for term in shlex.split(dsn)]
        params = dict(parts)
        if not params.get("host") or not params.get("dbname") or not params.get("user") or len(parts) != len(params):
            raise ValueError
        if params.keys() - allowed:
            raise ValueError
    except ValueError:
        raise ValueError("invalid read-only PostgreSQL connection configuration") from None
    # libpq 的数据库参数名与环境变量名不同，其余支持项以大写映射。
    params["database"] = params.pop("dbname")
    params.setdefault("connect_timeout", "4")
    return {**{"PG" + key.upper(): value for key, value in params.items()},
            "PGOPTIONS": "-c default_transaction_read_only=on -c statement_timeout=5000"}


def local_target(cluster):
    target = Target.__new__(Target)
    target.tenants = {"hot": "hot", "cold": "cold"}
    target.api = {group: (f"http://127.0.0.1:{port}", "local-load-token")
                  for group, port in cluster.ports.items()}
    target.sql = cluster.sql
    return target


def fields(obj, expected, label):
    if not isinstance(obj, dict) or set(obj) != set(expected):
        raise ValueError(f"{label} must contain {', '.join(sorted(expected))}")


def whole(value, lo, hi, label):
    if type(value) is not int or not lo <= value <= hi:
        raise ValueError(f"{label} must be an integer from {lo} to {hi}")


def validate_limits(obj, expected, label):
    fields(obj, expected, label)
    for key, value in obj.items():
        if type(value) not in (float, int) or not math.isfinite(value) or value < 0:
            raise ValueError(f"invalid threshold {label}.{key}")
        if key == "max_rejected_fraction" and value > 1:
            raise ValueError(f"{label}.{key} exceeds 1")


def validate_profile(profile):
    fields(profile, {"phases", "connections", "io_delay_ms", "payload_bytes", "drain_seconds", "in_flight", "batch"}, "profile")
    fields(profile["phases"], PHASES, "phases")
    whole(profile["io_delay_ms"], 1, 30000, "io_delay_ms")
    whole(profile["payload_bytes"], 0, 64_000, "payload_bytes")
    whole(profile["drain_seconds"], 2, 3600, "drain_seconds")
    whole(profile["in_flight"], 1, 64, "in_flight")
    whole(profile["batch"], 1, 128, "batch")
    if profile["batch"] * (profile["payload_bytes"] + 256) >= 128 * 1024:
        raise ValueError("configured batch may exceed the API request limit")
    connection_budget(profile["connections"])
    for phase, cfg in profile["phases"].items():
        fields(cfg, {"seconds", "rates", "timers", "jitter_ms", "topology", "limits"}, phase)
        limits = cfg["limits"]
        fields(limits, {"phase", "lanes", "timer"}, phase + ".limits")
        validate_limits(limits["phase"], PHASE_LIMITS, phase + ".limits.phase")
        fields(limits["lanes"], LANES, phase + ".limits.lanes")
        for lane, values in limits["lanes"].items():
            validate_limits(values, LANE_LIMITS, phase + ".limits.lanes." + lane)
        validate_limits(limits["timer"], TIMER_LIMITS, phase + ".limits.timer")
        whole(cfg["seconds"], 1, 86400, phase + ".seconds")
        whole(cfg["timers"], 0 if phase == "control" else 1, 10000, phase + ".timers")
        whole(cfg["jitter_ms"], 0, 60000, phase + ".jitter_ms")
        if cfg["jitter_ms"] >= 1000 * cfg["seconds"]:
            raise ValueError(f"{phase}.jitter_ms must fit within the offered load window")
        if type(cfg["topology"]) is not str or not cfg["topology"].strip():
            raise ValueError(f"{phase}.topology is required")
        fields(cfg["rates"], LANES, phase + ".rates")
        for lane, rate in cfg["rates"].items():
            low = int(phase != "control" or lane == "cold")
            high = 0 if phase == "control" and lane != "cold" else 100000
            whole(rate, low, high, f"{phase}.rates.{lane}")
        if sum(cfg["rates"].values()) * cfg["seconds"] > 5_000_000:
            raise ValueError(f"{phase} exceeds the measured cohort size limit")
    if profile["phases"]["control"]["timers"]:
        raise ValueError("control must contain only low-volume short work")


def connection_budget(budget):
    fields(budget, {"database_max", "reserved", "roles"}, "connections")
    whole(budget["database_max"], 1, 262143, "connections.database_max")
    whole(budget["reserved"], 1, budget["database_max"], "connections.reserved")
    if not isinstance(budget["roles"], dict) or not budget["roles"]:
        raise ValueError("connections.roles must declare every deployed role")
    planned = 0
    for role, settings in budget["roles"].items():
        fields(settings, {"replicas", "max_open", "auxiliary"}, role)
        whole(settings["replicas"], 0, 10000, role + ".replicas")
        whole(settings["max_open"], 0, 262143, role + ".max_open")
        whole(settings["auxiliary"], 0, 262143, role + ".auxiliary")
        planned += settings["replicas"] * (settings["max_open"] + settings["auxiliary"])
    if planned + budget["reserved"] > budget["database_max"]:
        raise ValueError("maximum replicas and reserved sessions exceed the database connection budget")
    return planned


def local_profile():
    warm = {"hot": 24, "cold": 12, "cpu": 12, "io": 8, "retry": 4}
    profile = {
        "io_delay_ms": 120, "payload_bytes": 512, "in_flight": 4, "batch": 4, "drain_seconds": 20,
        "connections": {"database_max": 200, "reserved": 24,
                        "roles": {role: {"replicas": spec[0], "max_open": spec[1], "auxiliary": spec[2]}
                                  for role, spec in {"api": (2, 16, 0), "scheduler": (2, 16, 0),
                                                     "short": (2, 16, 1), "io": (2, 8, 1), "cpu": (1, 8, 1),
                                                     "finalizer": (1, 16, 0), "archive": (1, 16, 0)}.items()}},
        "phases": {
            "control": {"seconds": 2, "rates": {lane: 12 if lane == "cold" else 0 for lane in LANES},
                        "timers": 0, "jitter_ms": 0, "topology": "local, cold tenant only"},
            "sustained": {"seconds": 4, "rates": warm.copy(), "timers": 12, "jitter_ms": 0,
                          "topology": "local, all pools"},
            "burst": {"seconds": 2, "rates": {lane: rate * 3 for lane, rate in warm.items()},
                      "timers": 32, "jitter_ms": 0, "topology": "local, all pools"},
            "soak": {"seconds": 12, "rates": warm.copy(), "timers": 36, "jitter_ms": 1000,
                     "topology": "local, all pools and jitter"},
            "degraded": {"seconds": 4, "rates": warm.copy(), "timers": 12, "jitter_ms": 0,
                         "topology": "local, one short Worker removed"},
        },
    }
    for phase in profile["phases"].values():
        phase["limits"] = copy.deepcopy(LOCAL_LIMITS)
    profile["phases"]["soak"]["limits"]["phase"]["min_archived_rows"] = 512
    return profile


def hotspot_profile():
    profile = local_profile()
    profile.update(batch=16, in_flight=16, drain_seconds=45)
    burst = profile["phases"]["burst"]
    burst.update(seconds=6, rates={"hot": 580, "cold": 25, "cpu": 15, "io": 32, "retry": 15})
    burst["limits"]["phase"].update(max_backlog_growth=1500, max_drain_seconds=35)
    for lane, bounds in burst["limits"]["lanes"].items():
        if lane != "cold":
            bounds["max_rejected_fraction"] = .98
    return profile


def percentile(values):
    if not values:
        return None
    return round(sorted(values)[math.ceil(len(values) * .99) - 1], 3)


def send_batch(target, lane, base, size, prefix, planned, profile):
    group, topic = LANES[lane]
    payload = {"delay_ms": profile["io_delay_ms"]} if lane == "io" else {"value": "x" * profile["payload_bytes"]}
    # 标记重试流量，便于独立测试环境注入一次事务级瞬时故障。
    batch = [{"idempotency_key": f"{prefix}-{lane}-{i}", "topic": topic, "payload": payload}
             for i in range(base, base + size)]
    start = time.monotonic()
    status, body = target.request(group, "/runs/batch", {"runs": batch})
    accepted_ids = (body.get("data", {}).get("ids", [])
                    if status == 202 and isinstance(body, dict) and isinstance(body.get("data"), dict) else [])
    accepted_ids = accepted_ids if isinstance(accepted_ids, list) and len(accepted_ids) == size and all(
        isinstance(key, str) and re.fullmatch(r"[A-Za-z0-9_-]{1,128}", key) for key in accepted_ids) else []
    valid = bool(accepted_ids) and len(set(accepted_ids)) == size
    return ("accepted" if valid else str(status) if status != 202 else "malformed_202"), size, (
        1000 * (start - planned)), (1000 * (time.monotonic() - start)), accepted_ids if valid else []


def offer(target, lane, rate, prefix, started, seconds, profile):
    counts = Counter()
    lateness, latency, accepted_ids = [], [], []
    limit, batch = profile["in_flight"], min(profile["batch"], max(1, rate // 10))
    with concurrent.futures.ThreadPoolExecutor(max_workers=limit) as executor:
        pending = set()

        def collect(futures):
            for future in futures:
                status, size, late_ms, api_ms, ids = future.result()
                counts[status] += size
                lateness.extend([late_ms] * size)
                latency.extend([api_ms] * size)
                accepted_ids.extend(ids)

        total = rate * seconds
        for base in range(0, total, batch):
            planned = started + base / rate
            time.sleep(max(0, planned - time.monotonic()))
            finished = {future for future in pending if future.done()}
            collect(finished)
            pending.difference_update(finished)
            size = min(batch, total - base)
            if len(pending) >= limit:
                counts["generator_overrun"] += size
            else:
                pending.add(executor.submit(send_batch, target, lane, base, size, prefix, planned, profile))
        collect(pending)
    return counts, percentile(lateness), percentile(latency), accepted_ids


def stage_timers(target, prefix, cfg, local, names):
    if not cfg["timers"]:
        return "", time.monotonic() + .1
    fire = (datetime.now(timezone.utc) + timedelta(seconds=max(5, cfg["timers"] / 20))).replace(microsecond=0)
    for index in range(cfg["timers"]):
        name = f"{prefix}-timer-{index}"
        status, body = target.request("hot", "/schedules", {
            "id": name, "expression": f"{fire.second} {fire.minute} {fire.hour} * * *", "time_zone": "UTC",
            "topic": "demo.record", "payload": {}, "jitter_window_ms": cfg["jitter_ms"],
        })
        if status != 201 or not isinstance(body, dict):
            raise RuntimeError(f"schedule {index}: creation HTTP {status}")
        names.append(name)
    if local:
        fire = datetime.now(timezone.utc) + timedelta(seconds=1)
        target.sql("""UPDATE schedules SET next_fire_at = :'fire'::timestamptz
            WHERE tenant_id=:'hot' AND id >= :'prefix' AND id < :'prefix' || '~';""",
                   prefix=prefix, hot=target.tenants["hot"], fire=fire.isoformat())
    until_fire = (fire - datetime.now(timezone.utc)).total_seconds()
    if until_fire < 0.2:
        raise RuntimeError("schedule setup overran the common firing time")
    return fire.isoformat(), time.monotonic() + until_fire


def pause_timers(target, names):
    failed = 0
    for name in names:
        path = "/schedules/" + name
        status, body = target.request("hot", path)
        definition = body.get("data", {}) if isinstance(body, dict) else {}
        if status != 200 or not isinstance(definition, dict) or type(definition.get("version")) is not int:
            failed += 1
            continue
        status, _ = target.request("hot", path, {"version": definition["version"], "paused": True}, "PATCH")
        if status != 200:
            failed += 1
    return failed


def current_database_signals(target):
    return json.loads(target.sql("""
        SELECT json_build_object('wal_bytes', pg_wal_lsn_diff(pg_current_wal_insert_lsn(), '0/0')::bigint,
            'database_bytes', pg_database_size(current_database()),
            'client_connections', (SELECT count(*) FROM pg_stat_activity
                WHERE backend_type='client backend'),
            'archived_rows', (SELECT coalesce(sum(total),0) FROM scheduler_archive_totals),
            'max_connections', current_setting('max_connections')::int,
            'dead_rows', coalesce((SELECT sum(n_dead_tup) FROM pg_stat_user_tables
                WHERE relname IN ('mq_tasks','mq_task_attempts','scheduled_runs','scheduled_outbox')),0),
            'active_waits', coalesce((SELECT json_object_agg(event, n) FROM (
                SELECT wait_event_type || ':' || wait_event AS event, count(*) AS n
                FROM pg_stat_activity WHERE state='active' AND wait_event IS NOT NULL
                AND datname=current_database() AND pid<>pg_backend_pid() GROUP BY 1) waits), '{}'::json),
            'replication_replay_bytes', (SELECT max(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn))
                FROM pg_stat_replication));
    """))


def inspect_phase(target, phase, cfg, profile, prefix, fire, started, local_cluster):
    enabled = {lane: rate for lane, rate in cfg["rates"].items() if rate}
    variables = {"prefix": prefix, "fire": fire, **target.tenants}
    expected_timers = cfg["timers"]
    oldest, waits = defaultdict(float), Counter()
    before = current_database_signals(target)
    plan = profile["connections"]
    if before["max_connections"] != plan["database_max"]:
        raise RuntimeError("database max_connections differs from the certified profile")
    peak_connections = before["client_connections"]
    peak_backlog = 0
    window_start = datetime.now(timezone.utc) + timedelta(seconds=started - time.monotonic())
    initial_window, last_window = {}, {}
    deadline = started + cfg["seconds"] + profile["drain_seconds"]
    finished_at = None
    maintenance = None
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(enabled)) as producers:
        work = {lane: producers.submit(offer, target, lane, rate, prefix, started, cfg["seconds"], profile)
                for lane, rate in enabled.items()}
        time.sleep(max(0, started - time.monotonic()))
        if local_cluster and phase == "soak":
            maintenance = local_cluster.start("archive")
        while True:
            now = time.monotonic()
            results = json.loads(target.sql(SNAPSHOT, **variables))
            peak_backlog = max(peak_backlog, sum(row["pending"] for row in results.values()))
            for lane, row in results.items():
                oldest[lane] = max(oldest[lane], row["oldest_runnable_ms"] or 0)
            if now <= started + cfg["seconds"]:
                last_window = results
                if now < started + cfg["seconds"] / 2:
                    initial_window = results
            if all(future.done() for future in work.values()) and now >= started + cfg["seconds"]:
                accepted = {lane: future.result()[0]["accepted"] for lane, future in work.items()}
                if expected_timers:
                    accepted["timer"] = expected_timers
                if all(results.get(lane, {}).get("persisted", 0) >= count for lane, count in accepted.items()
                       ) and all(not row["pending"] for row in results.values()):
                    finished_at = time.monotonic()
                    break
            if now >= deadline:
                break
            signals = current_database_signals(target)
            waits.update(signals["active_waits"])
            peak_connections = max(peak_connections, signals["client_connections"])
            time.sleep(.3)
    details = json.loads(target.sql(DETAIL, **variables, window_start=window_start.isoformat(),
                                    window_end=(window_start + timedelta(seconds=cfg["seconds"])).isoformat(),
                                    jitter=cfg["jitter_ms"]))
    after = current_database_signals(target)
    if maintenance:
        stop(maintenance)
    output = {"topology": cfg["topology"], "offered_seconds": cfg["seconds"], "offered_rates": cfg["rates"], "lanes": {},
              "drain_seconds": None if finished_at is None else round(finished_at - started - cfg["seconds"], 3),
              "completed_per_second": round(sum(row["completed_during_offer"] for row in details.values())
                                            / cfg["seconds"], 2),
              "backlog_growth": sum(row["pending"] for row in last_window.values())
              - sum(row["pending"] for row in initial_window.values()),
              "peak_backlog": peak_backlog,
              "archived_rows": after["archived_rows"] - before["archived_rows"],
              "database": {"wal_bytes": after["wal_bytes"] - before["wal_bytes"],
                           "database_growth_bytes": after["database_bytes"] - before["database_bytes"],
                           "dead_rows": after["dead_rows"], "sampled_waits": dict(waits),
                           "replication_replay_bytes": after["replication_replay_bytes"],
                           "connection_budget": connection_budget(plan), "max_connections": plan["database_max"],
                           "reserved_connections": plan["reserved"],
                           "peak_client_connections": max(peak_connections, after["client_connections"])}}
    done = sum(row["done"] for row in results.values())
    output["database"]["wal_bytes_per_completion"] = round(output["database"]["wal_bytes"] / done, 2) if done else None
    for lane in [*enabled, *(("timer",) if expected_timers else ())]:
        counts, offer_p99, api_p99, ids = work[lane].result() if lane in work else (
            Counter(accepted=expected_timers), None, None, [])
        row = results.get(lane, {})
        report = dict(details.get(lane, {}))
        group, topic = LANES.get(lane, ("hot", "demo.record"))
        report.update(offered=sum(counts.values()), acknowledged=counts["accepted"], responses=dict(counts),
                      tenant_group=group, task_type=topic,
                      api_p99_ms=api_p99, offer_p99_ms=offer_p99, oldest_runnable_ms=round(oldest[lane], 3),
                      persisted=row.get("persisted", 0), completed=row.get("done", 0),
                      failed=row.get("failed", 0), pending=row.get("pending", 0),
                      rejected_fraction=counts["429"] / sum(counts.values()))
        if ids:
            report["acknowledged_ids_missing"] = count_missing(target, prefix, target.tenants[LANES[lane][0]], lane, ids)
        output["lanes"][lane] = report
    output["phase"] = phase
    return output


def count_missing(target, prefix, tenant, lane, ids):
    missing = 0
    for index in range(0, len(ids), 1000):
        missing += int(target.sql("""
            SELECT count(*) FROM unnest(string_to_array(:'ids', ',')) AS expected(id)
            WHERE NOT EXISTS (SELECT 1 FROM scheduled_runs r WHERE r.id = expected.id AND r.tenant_id = :'tenant'
                AND r.request_key >= :'prefix' AND r.request_key < :'prefix' || '~');
        """, tenant=tenant, prefix=f"{prefix}-{lane}-", ids=",".join(ids[index:index + 1000])))
    return missing


def check_limits(report, limits):
    failures = []

    def check(scope, row, bounds):
        for bound, limit in sorted(bounds.items()):
            direction, metric = bound.split("_", 1)
            value = row.get(metric)
            if value is None or (value > limit if direction == "max" else value < limit):
                failures.append({"scope": scope, "metric": metric, "value": value, "threshold": bound, "limit": limit})

    check("phase", report, limits["phase"])
    db = report.get("database")
    if db and db["peak_client_connections"] > db["max_connections"] - db["reserved_connections"]:
        failures.append({"scope": "database", "metric": "connection_reserve", "value": db["peak_client_connections"]})
    for lane, row in report["lanes"].items():
        check(lane, row, limits["timer"] if lane == "timer" else limits["lanes"][lane])
        if (row["persisted"] != row["acknowledged"] or row["completed"] != row["acknowledged"]
                or row.get("terminal_samples") != row["completed"] or row.get("acknowledged_ids_missing")
                or set(row["responses"]) - {"accepted", "429"}):
            failures.append({"scope": lane, "metric": "accounting", "value": row})
        if lane == "retry" and row.get("retried_runs") != row["acknowledged"]:
            failures.append({"scope": lane, "metric": "retried_runs", "value": row.get("retried_runs"),
                             "expected": row["acknowledged"]})
    return failures


def certify(target, profile, phases, local_cluster=None):
    results = []
    for phase in phases:
        local = local_cluster is not None
        if local and phase == "degraded":
            stop(local_cluster.short_workers[-1])
        cfg = profile["phases"][phase]
        prefix = "cert-" + uuid.uuid4().hex[:16]
        if local and phase == "soak":
            target.sql("""
                INSERT INTO mq_tasks (id,topic,tenant_id,priority,status,attempts,max_attempts,
                    available_at,created_at,updated_at)
                SELECT 'archive-' || i,'demo.record','historical',0,'done',1,5,
                    statement_timestamp()-interval '100 days', statement_timestamp()-interval '100 days',
                    statement_timestamp()-interval '100 days'
                FROM generate_series(1,512) AS i;
            """)
        names = []
        try:
            fire, started = stage_timers(target, prefix, cfg, local, names)
            row = inspect_phase(target, phase, cfg, profile, prefix, fire, started, local_cluster)
            row["prefix"] = prefix
            row["workload"] = {key: profile[key] for key in ("io_delay_ms", "payload_bytes", "batch", "in_flight")}
            row["workload"].update(timers=cfg["timers"], jitter_ms=cfg["jitter_ms"])
            row["failures"] = check_limits(row, cfg["limits"])
        finally:
            if names and (missed := pause_timers(target, names)):
                raise RuntimeError(f"{prefix}: could not pause {missed} test plans")
        results.append(row)
    return results


def summarize_reports(reports):
    if len({r["mode"] for r in reports}) != 1:
        raise ValueError("local and target reports must be summarized independently")
    measured = [row for report in reports for row in report["phases"]]
    by_phase = {phase: [r for r in measured if r["phase"] == phase and not r["failures"]] for phase in PHASES}
    controls = by_phase["control"]
    cold_control = max((r["lanes"]["cold"]["terminal_p99_ms"] for r in controls), default=None)
    passed = all(by_phase.values())
    sustained = max((r["completed_per_second"] for r in by_phase["sustained"] + by_phase["soak"]), default=None)
    return {"passed": passed, "mode": reports[0]["mode"],
            "highest_passing_sustained_completion_per_second": sustained,
            "missing_passing_phases": [phase for phase, rows in by_phase.items() if not rows],
            "passing_bursts": [{"offered_per_second": sum(row["offered_rates"].values()),
                                "seconds": row["offered_seconds"], "peak_backlog": row["peak_backlog"],
                                "drain_seconds": row["drain_seconds"]} for row in by_phase["burst"]],
            "cold_control_terminal_p99_ms": cold_control,
            "cold_terminal_p99_ms_by_phase": {
                phase: [row["lanes"]["cold"]["terminal_p99_ms"] for row in rows]
                for phase, rows in by_phase.items()},
            "degraded_topologies": [{"topology": row["topology"], "completed_per_second": row["completed_per_second"],
                                    "drain_seconds": row["drain_seconds"]} for row in by_phase["degraded"]]}


def compare_hotspot():
    profile = hotspot_profile()
    validate_profile(profile)
    report = {}
    for name, protected in (("shared", ""), ("protected", "cold")):
        with tempfile.TemporaryDirectory(prefix="plumego-hotspot-") as directory, contextlib.ExitStack() as stack:
            cluster = LoadCluster(Path(directory), stack, mixed=True, protected_tenants=protected)
            cluster.sql("""
                INSERT INTO scheduler_tenant_ingress (tenant_id,regular_per_second) VALUES ('hot',220);
                INSERT INTO scheduler_execution_budgets (scope,budget_key,max_concurrent) VALUES ('tenant','hot',5);
            """)
            profile["phases"]["burst"]["topology"] = "local, tenant admission and " + name + " claims"
            report[name] = certify(local_target(cluster), profile, ("control", "burst"), cluster)
    return {"passed": all(not row["failures"] for row in report["protected"]), "mode": "local",
            "phases": report["protected"], "shared_claims_control": report["shared"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--local", action="store_true")
    mode.add_argument("--profile", type=Path)
    mode.add_argument("--template", action="store_true")
    mode.add_argument("--summary", type=Path, nargs="+")
    mode.add_argument("--hotspot", action="store_true")
    parser.add_argument("--phase", choices=PHASES)
    args = parser.parse_args()
    if args.template:
        print(json.dumps(local_profile(), indent=2))
        return 0
    if args.profile and not args.phase:
        parser.error("target mode requires --phase so each topology can be prepared and verified independently")
    try:
        if args.hotspot:
            report = compare_hotspot()
            print(json.dumps(report, indent=2))
            return int(not report["passed"])
        if args.summary:
            report = summarize_reports([json.loads(path.read_text()) for path in args.summary])
            print(json.dumps(report, indent=2))
            return int(not report["passed"])
        profile = local_profile() if args.local else json.loads(args.profile.read_text())
        validate_profile(profile)
        with contextlib.ExitStack() as stack:
            cluster = LoadCluster(Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="plumego-cert-"))),
                                  stack, mixed=True) if args.local else None
            target = local_target(cluster) if cluster else Target()
            report = certify(target, profile, (args.phase,) if args.phase else PHASES, cluster)
    except (KeyError, ValueError, OSError, subprocess.SubprocessError, RuntimeError) as exc:
        print(json.dumps({"passed": False, "error": str(exc)}, ensure_ascii=False))
        return 1
    passed = all(not row["failures"] for row in report)
    print(json.dumps({"passed": passed, "mode": "local" if args.local else "target", "phases": report}, indent=2))
    return int(not passed)


if __name__ == "__main__":
    raise SystemExit(main())
