#!/usr/bin/env python3
"""Evaluate scheduler release evidence for one revision and target deployment."""

import argparse
import json
import math
from pathlib import Path

from certify_capacity import LANES, check_limits, connection_budget, fields, validate_profile

CAPACITY_CASES = {
    "control": "control",
    "sustained": "sustained",
    "burst": "burst",
    "soak": "soak",
    "worker_loss": "degraded",
    "database_failover": "degraded",
}
RECOVERY_TIMES = {
    "writer_ready_seconds", "synchronous_protection_restored_seconds", "first_new_ack_seconds",
    "lease_recovered_seconds", "backlog_drained_seconds", "outbox_confirmed_seconds",
    "fully_reconciled_seconds", "second_writer_ready_seconds",
}
FEATURES = {
    "business_calendar": {"timezone", "daylight_saving", "exceptions", "versioning", "misfire"},
    "multi_region": {"partition", "single_writer", "cross_region_deduplication", "failback"},
    "untrusted_execution": {"resource_limits", "cancellation", "cleanup", "credential_isolation"},
}
ZERO_TOLERANCE = {"acknowledged_runs_lost", "duplicate_effects", "stale_writes_accepted"}


def number(value):
    return finite(value) and value >= 0


def finite(value):
    return type(value) is int or (type(value) is float and math.isfinite(value))


def validate_policy(policy, profile):
    fields(policy, {"deployment", "revision", "degraded_topologies", "recovery_limits_seconds",
                    "canary_min_seconds", "features"}, "release policy")
    validate_profile(profile)
    for key in ("deployment", "revision"):
        if not isinstance(policy[key], str) or not policy[key].strip():
            raise ValueError("release policy requires " + key)
    fields(policy["degraded_topologies"], {"worker_loss", "database_failover"}, "degraded topologies")
    names = tuple(policy["degraded_topologies"].values())
    if not all(isinstance(v, str) and v.strip() for v in names) or names[0] == names[1]:
        raise ValueError("provide distinct descriptions for the two degraded topologies")
    fields(policy["recovery_limits_seconds"], RECOVERY_TIMES | {"restore_seconds"}, "recovery limits")
    if any(not number(v) or v == 0 for v in policy["recovery_limits_seconds"].values()):
        raise ValueError("each recovery limit requires a positive approved threshold")
    if not number(policy["canary_min_seconds"]) or policy["canary_min_seconds"] == 0:
        raise ValueError("canary requires a positive minimum observation period")
    if not isinstance(policy["features"], list) or any(
            not isinstance(feature, str) or feature not in FEATURES for feature in policy["features"]):
        raise ValueError("unknown release feature")


def evaluate(policy, profile, evidence, stage):
    validate_policy(policy, profile)
    if not isinstance(evidence, dict):
        raise ValueError("release evidence must be an object")
    failures = []

    def require(condition, scope, check):
        if not condition:
            failures.append({"scope": scope, "check": check})
        return condition

    def report(name):
        item = evidence.get(name)
        if not require(isinstance(item, dict), name, "evidence_missing"):
            return None
        if not require(all(item.get(k) == policy[k] for k in ("deployment", "revision")),
                       name, "deployment_or_revision_mismatch"):
            return None
        data = item.get("report")
        if not require(isinstance(data, dict), name, "report_missing"):
            return None
        if name not in CAPACITY_CASES and not require(data.get("mode") == "target", name, "target_report_required"):
            return None
        return data

    def check_zero(data, name, metrics):
        for metric in sorted(metrics):
            require(number(data.get(metric)) and data[metric] == 0, name, metric)

    def check_true(data, name, metrics):
        for metric in sorted(metrics):
            require(data.get(metric) is True, name, metric)

    def check_positive(data, name, metrics):
        for metric in sorted(metrics):
            require(number(data.get(metric)) and data[metric] > 0, name, metric)

    def check_times(data, name, metrics):
        for metric in sorted(metrics):
            seen = data.get(metric)
            if not number(seen) or seen > policy["recovery_limits_seconds"][metric]:
                failures.append({"scope": name, "check": metric, "observed": seen if number(seen) else None,
                                 "limit": policy["recovery_limits_seconds"][metric]})

    for case, phase in CAPACITY_CASES.items():
        data = report(case)
        if data is None:
            continue
        if not require(data.get("mode") == "target" and data.get("passed") is True, case, "target_capacity_failed"):
            continue
        rows = data.get("phases")
        if not require(isinstance(rows, list) and len(rows) == 1 and isinstance(rows[0], dict) and
                       rows[0].get("phase") == phase and rows[0].get("failures") == [],
                       case, "capacity_phase_missing_or_failed"):
            continue
        row = rows[0]
        config = profile["phases"][phase]
        topology = policy["degraded_topologies"].get(case, config["topology"])
        require(row.get("topology") == topology, case, "topology_mismatch")
        require(row.get("offered_rates") == config["rates"] and row.get("offered_seconds") == config["seconds"],
                case, "offered_load_mismatch")
        workload = {key: profile[key] for key in ("io_delay_ms", "payload_bytes", "batch", "in_flight")}
        workload.update(timers=config["timers"], jitter_ms=config["jitter_ms"])
        require(row.get("workload") == workload, case, "workload_mismatch")
        lanes = {lane for lane, rate in config["rates"].items() if rate}
        if config["timers"]:
            lanes.add("timer")
        db = row.get("database")
        if not require(isinstance(row.get("lanes"), dict) and row["lanes"].keys() == lanes and isinstance(db, dict),
                       case, "incomplete_capacity_report"):
            continue
        budget = profile["connections"]
        require(db.get("connection_budget") == connection_budget(budget) and
                db.get("max_connections") == budget["database_max"] and
                db.get("reserved_connections") == budget["reserved"], case, "connection_budget_mismatch")
        require(number(db.get("peak_client_connections")), case, "connection_peak_missing")
        for metric in config["limits"]["phase"]:
            key = metric.split("_", 1)[1]
            require(finite(row.get(key)), case, key)
        completed_in_window = 0
        for lane, measurements in row["lanes"].items():
            if not require(isinstance(measurements, dict), case, "invalid_lane_" + lane):
                continue
            bounds = config["limits"]["timer"] if lane == "timer" else config["limits"]["lanes"][lane]
            topic = LANES.get(lane, ("hot", "demo.record"))
            offered = config["timers"] if lane == "timer" else config["rates"][lane] * config["seconds"]
            responses = measurements.get("responses")
            require(isinstance(responses, dict) and set(responses) <= {"accepted", "429"} and
                    all(type(n) is int and n >= 0 for n in responses.values()) and sum(responses.values()) == offered and
                    responses.get("accepted") == measurements.get("acknowledged") and responses.get("accepted", 0) > 0 and
                    measurements.get("offered") == offered and (measurements.get("tenant_group"), measurements.get("task_type")) == topic
                    and measurements.get("failed") == 0 and measurements.get("pending") == 0 and
                    measurements.get("rejected_fraction") == responses.get("429", 0) / offered and
                    (lane == "timer" or measurements.get("acknowledged_ids_missing") == 0),
                    case, "lane_accounting_" + lane)
            completed = measurements.get("completed_during_offer")
            terminal = measurements.get("completed")
            if require(type(completed) is int and type(terminal) is int and 0 <= completed <= terminal,
                       case, "window_accounting_" + lane):
                completed_in_window += completed
            for metric in bounds:
                require(number(measurements.get(metric.split("_", 1)[1])), case, metric + "_" + lane)
        require(row.get("completed_per_second") == round(completed_in_window / config["seconds"], 2),
                case, "throughput_accounting")
        try:
            for issue in check_limits(row, config["limits"]):
                failures.append({"scope": case, "check": issue["metric"], "lane": issue["scope"]})
        except (KeyError, TypeError, ValueError):
            require(False, case, "invalid_capacity_metrics")

    data = report("business")
    if data is not None:
        check_positive(data, "business", {"acknowledged_runs_checked", "effects_checked"})
        check_zero(data, "business", ZERO_TOLERANCE | {"unresolved_differences"})
        check_true(data, "business", {"ledger_reviewed", "submit_windows_exercised", "result_windows_exercised",
                                      "receiver_commit_disconnect_exercised"})

    data = report("failover")
    if data is not None:
        check_positive(data, "failover", {"acknowledged_runs_checked", "second_cut_results_checked"})
        check_zero(data, "failover", ZERO_TOLERANCE)
        check_true(data, "failover", {"old_endpoint_rejected_write", "old_worker_rejected_write",
                                      "commit_horizon_verified", "admission_paused_without_sync"})
        check_times(data, "failover", RECOVERY_TIMES)

    data = report("restore")
    if data is not None:
        check_positive(data, "restore", {"claims_checked", "schedule_cursors_checked", "post_target_effects_detected"})
        check_zero(data, "restore", {"duplicate_effects", "unresolved_differences"})
        check_true(data, "restore", {"sender_paused_during_reconciliation", "original_keys_preserved"})
        check_times(data, "restore", {"restore_seconds"})

    data = report("rollback")
    if data is not None:
        check_positive(data, "rollback", {"in_flight_runs_checked"})
        check_true(data, "rollback", {"run_ids_preserved", "task_versions_preserved",
                                      "idempotency_preserved", "compatible_workers_drained"})

    for feature in policy["features"]:
        data = report(feature)
        if data is not None:
            check_true(data, feature, FEATURES[feature])

    if stage == "promote":
        data = report("canary")
        if data is not None:
            seconds = data.get("observed_seconds")
            require(number(seconds) and seconds >= policy["canary_min_seconds"], "canary", "observation_window")
            check_positive(data, "canary", {"acknowledged_runs_checked", "effects_checked"})
            check_zero(data, "canary", ZERO_TOLERANCE | {"unresolved_differences"})
            check_true(data, "canary", {"latency_slo_met", "backlog_slo_met", "reconciliation_cycle_completed"})
    return {"passed": not failures, "stage": stage, "failures": failures}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--policy", type=Path, required=True)
    parser.add_argument("--capacity-profile", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--stage", choices=("preflight", "promote"), required=True)
    args = parser.parse_args()
    try:
        policy, profile, evidence = (json.loads(p.read_text())
                                     for p in (args.policy, args.capacity_profile, args.evidence))
        decision = evaluate(policy, profile, evidence, args.stage)
    except (OSError, TypeError, ValueError, KeyError) as exc:
        decision = {"passed": False, "stage": args.stage, "error": str(exc)}
    print(json.dumps(decision, indent=2, ensure_ascii=False))
    return int(not decision["passed"])


if __name__ == "__main__":
    raise SystemExit(main())
