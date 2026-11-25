import copy
import unittest

from certify_capacity import LANES, connection_budget, local_profile
from release_gate import CAPACITY_CASES, RECOVERY_TIMES, ZERO_TOLERANCE, evaluate


class ReleaseGateTest(unittest.TestCase):
    def setUp(self):
        self.profile = local_profile()
        self.policy = {
            "deployment": "staging", "revision": "candidate",
            "degraded_topologies": {"worker_loss": "one short Worker removed",
                                    "database_failover": "replacement writer protected"},
            "recovery_limits_seconds": dict.fromkeys(RECOVERY_TIMES | {"restore_seconds"}, 30),
            "canary_min_seconds": 900, "features": [],
        }
        identity = {key: self.policy[key] for key in ("deployment", "revision")}
        self.evidence = {}

        def record(case, data):
            self.evidence[case] = {**identity, "report": {"mode": "target", **data}}

        for case, phase in CAPACITY_CASES.items():
            c = self.profile["phases"][phase]
            counts = {lane: rate * c["seconds"] for lane, rate in c["rates"].items() if rate}
            if c["timers"]:
                counts["timer"] = c["timers"]
            row = {"topology": self.policy["degraded_topologies"].get(case, c["topology"]),
                   "phase": phase, "offered_rates": c["rates"], "offered_seconds": c["seconds"],
                   "completed_per_second": round(sum(counts.values()) / c["seconds"], 2),
                   "backlog_growth": 0, "drain_seconds": 1,
                   "archived_rows": c["limits"]["phase"]["min_archived_rows"], "failures": [], "lanes": {},
                   "workload": {key: self.profile[key] for key in ("io_delay_ms", "payload_bytes", "batch", "in_flight")},
                   "database": {"connection_budget": connection_budget(self.profile["connections"]),
                                "max_connections": self.profile["connections"]["database_max"],
                                "reserved_connections": self.profile["connections"]["reserved"],
                                "peak_client_connections": 30}}
            row["workload"].update(timers=c["timers"], jitter_ms=c["jitter_ms"])
            for lane, count in counts.items():
                group, topic = LANES.get(lane, ("hot", "demo.record"))
                row["lanes"][lane] = {"offered": count, "persisted": count, "acknowledged": count, "completed": count,
                                      "terminal_samples": count, "completed_during_offer": count,
                                      "responses": {"accepted": count},
                                      "failed": 0, "pending": 0, "acknowledged_ids_missing": 0,
                                      "rejected_fraction": 0, "retried_runs": count if lane == "retry" else 0,
                                      "queue_p99_ms": 5, "terminal_p99_ms": 9, "oldest_runnable_ms": 9,
                                      "api_p99_ms": 3, "offer_p99_ms": 3, "dispatch_p99_ms": 5,
                                      "tenant_group": group, "task_type": topic}
            record(case, {"mode": "target", "passed": True, "phases": [row]})
        valid = dict.fromkeys(ZERO_TOLERANCE | {"unresolved_differences"}, 0)
        record("business", {**valid, "acknowledged_runs_checked": 8, "effects_checked": 4, "ledger_reviewed": True,
                            "submit_windows_exercised": True, "result_windows_exercised": True,
                            "receiver_commit_disconnect_exercised": True})
        record("failover", {**valid, **dict.fromkeys(RECOVERY_TIMES, 2), "acknowledged_runs_checked": 8,
                            "second_cut_results_checked": 9, "old_endpoint_rejected_write": True,
                            "old_worker_rejected_write": True, "commit_horizon_verified": True,
                            "admission_paused_without_sync": True})
        record("restore", {**valid, "claims_checked": 2, "schedule_cursors_checked": 1, "post_target_effects_detected": 1,
                           "sender_paused_during_reconciliation": True, "original_keys_preserved": True,
                           "restore_seconds": 4})
        record("rollback", {"in_flight_runs_checked": 1, "run_ids_preserved": True, "task_versions_preserved": True,
                            "idempotency_preserved": True, "compatible_workers_drained": True})
        record("canary", {**valid, "observed_seconds": 900, "acknowledged_runs_checked": 8, "effects_checked": 4,
                          "latency_slo_met": True, "backlog_slo_met": True, "reconciliation_cycle_completed": True})

    def reject(self, scope, check, stage="preflight"):
        decision = evaluate(self.policy, self.profile, self.evidence, stage)
        self.assertFalse(decision["passed"])
        self.assertTrue(any(row["scope"] == scope and row["check"] == check for row in decision["failures"]), decision)

    def test_evaluates_complete_staged_evidence(self):
        self.assertTrue(evaluate(self.policy, self.profile, self.evidence, "preflight")["passed"])
        self.assertTrue(evaluate(self.policy, self.profile, self.evidence, "promote")["passed"])

    def test_requires_both_distinct_degraded_topologies(self):
        self.evidence["database_failover"]["report"] = copy.deepcopy(self.evidence["worker_loss"]["report"])
        self.reject("database_failover", "topology_mismatch")
        del self.evidence["database_failover"]
        self.reject("database_failover", "evidence_missing")

    def test_rechecks_original_capacity_metrics_and_sampling(self):
        row = self.evidence["soak"]["report"]["phases"][0]
        cold = row["lanes"]["cold"]
        cold["queue_p99_ms"] = float("nan")
        self.reject("soak", "max_queue_p99_ms_cold")
        cold["queue_p99_ms"] = 121_000
        self.reject("soak", "queue_p99_ms")
        cold["queue_p99_ms"] = 5
        cold["offered"] -= 1
        self.reject("soak", "lane_accounting_cold")
        cold["offered"] += 1
        row["completed_per_second"] *= 2
        self.reject("soak", "throughput_accounting")

    def test_rejects_mixed_revision_and_local_capacity(self):
        self.evidence["restore"]["revision"] = "previous"
        self.reject("restore", "deployment_or_revision_mismatch")
        self.evidence["sustained"]["report"]["mode"] = "local"
        self.reject("sustained", "target_capacity_failed")

    def test_rejects_local_recovery_even_with_matching_deployment_and_revision(self):
        self.evidence["restore"]["report"]["mode"] = "local"
        self.reject("restore", "target_report_required")
        self.evidence["rollback"]["report"].pop("mode")
        self.reject("rollback", "target_report_required")

    def test_enforces_recovery_threshold_and_zero_tolerance(self):
        self.evidence["failover"]["report"]["fully_reconciled_seconds"] = 31
        self.reject("failover", "fully_reconciled_seconds")
        self.evidence["business"]["report"]["duplicate_effects"] = 1
        self.reject("business", "duplicate_effects")
        self.evidence["canary"]["report"]["stale_writes_accepted"] = 1
        self.reject("canary", "stale_writes_accepted", "promote")

    def test_keeps_promotion_and_optional_features_gated(self):
        self.evidence["canary"]["report"]["observed_seconds"] = 899
        self.reject("canary", "observation_window", "promote")
        self.policy["features"].append("multi_region")
        self.reject("multi_region", "evidence_missing")
