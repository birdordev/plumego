import copy
import time
import unittest

import certify_capacity as capacity


class SlowAPI:
    def request(self, tenant, path, body):
        time.sleep(.15)
        return 202, {"data": {"ids": [item["idempotency_key"] for item in body["runs"]]}}


class CapacityCertificationTest(unittest.TestCase):
    def test_request_pressure_does_not_reduce_offered_count(self):
        profile = {**capacity.local_profile(), "batch": 1, "in_flight": 1}
        counts, offer, api, ids = capacity.offer(SlowAPI(), "cold", 20, "cert-slow", time.monotonic(), 1, profile)
        self.assertEqual(20, counts.total() if hasattr(counts, "total") else sum(counts.values()))
        self.assertGreater(counts["generator_overrun"], 0)
        self.assertEqual(counts["accepted"], len(ids))
        self.assertGreater(api, offer)

    def test_gates_fail_for_thresholds_and_acknowledged_ids_missing(self):
        profile = capacity.local_profile()
        limits = profile["phases"]["sustained"]["limits"]
        report = {"completed_per_second": 2, "backlog_growth": 0, "drain_seconds": 1, "archived_rows": 0, "lanes": {"cold": {
            "responses": {"accepted": 8}, "acknowledged": 8, "persisted": 8, "completed": 8, "terminal_samples": 8,
            "api_p99_ms": 10, "offer_p99_ms": 0, "oldest_runnable_ms": 100, "queue_p99_ms": 12,
            "terminal_p99_ms": 22, "rejected_fraction": 0, "acknowledged_ids_missing": 0,
        }}}
        self.assertEqual([], capacity.check_limits(report, limits))
        report["lanes"]["cold"]["queue_p99_ms"] = None
        report["lanes"]["cold"]["acknowledged_ids_missing"] = 1
        self.assertEqual({"queue_p99_ms", "accounting"}, {v["metric"] for v in capacity.check_limits(report, limits)})

    def test_profile_rejects_unmeasured_or_oversized_load(self):
        profile = capacity.local_profile()
        capacity.validate_profile(profile)
        invalid = copy.deepcopy(profile)
        invalid["phases"]["soak"]["rates"]["retry"] = 0
        with self.assertRaisesRegex(ValueError, "soak.rates.retry"):
            capacity.validate_profile(invalid)
        invalid = copy.deepcopy(profile)
        invalid["batch"] = 128
        invalid["payload_bytes"] = 64000
        with self.assertRaisesRegex(ValueError, "request limit"):
            capacity.validate_profile(invalid)

    def test_database_environment_enforces_read_only_and_credentials_are_separate(self):
        env = capacity.postgres_environment("postgresql://reader:fixture%20secret@localhost:5432/tasks?sslmode=verify-full")
        self.assertEqual("tasks", env["PGDATABASE"])
        self.assertEqual("fixture secret", env["PGPASSWORD"])
        self.assertEqual("verify-full", env["PGSSLMODE"])
        self.assertIn("default_transaction_read_only=on", env["PGOPTIONS"])
        with self.assertRaisesRegex(ValueError, "^invalid read-only PostgreSQL connection configuration$"):
            capacity.postgres_environment("host=localhost dbname=tasks user=reader options='-c default_transaction_read_only=off'")


if __name__ == "__main__":
    unittest.main()
