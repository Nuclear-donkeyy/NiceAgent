"""Unit tests for Redis capacity smoke report helpers."""

from __future__ import annotations

import unittest

import smoke_redis_capacity as smoke


class RedisCapacityReportTests(unittest.TestCase):
    def test_success_report_contains_rates_and_counts(self) -> None:
        report = smoke.build_success_report(
            messages=100,
            batch_size=25,
            stream="niceagent:capacity:runs:test",
            group="group",
            consumer="consumer",
            duration_seconds=1.5,
            enqueue_seconds=0.5,
            consume_seconds=1.0,
            consumed=100,
            acked=100,
            pending_count=0,
            lag=0,
        )

        self.assertEqual(report["kind"], "redis_capacity_smoke")
        self.assertEqual(report["result"], "passed")
        self.assertEqual(report["enqueue_rate_per_second"], 200)
        self.assertEqual(report["consume_rate_per_second"], 100)
        self.assertEqual(report["pending_count"], 0)

    def test_skip_report_is_machine_readable(self) -> None:
        report = smoke.build_skip_report("missing redis", 200, 50)

        self.assertEqual(report["result"], "skipped")
        self.assertEqual(report["reason"], "missing redis")
        self.assertEqual(report["messages"], 200)

    def test_parse_xread_entries(self) -> None:
        entries = smoke.parse_xread_entries(
            [
                [
                    "stream",
                    [
                        ["1-0", ["run_id", "run-1"]],
                        ["2-0", ["run_id", "run-2"]],
                    ],
                ]
            ]
        )

        self.assertEqual(entries, ["1-0", "2-0"])

    def test_parse_group_info(self) -> None:
        info = smoke.parse_group_info(
            [
                ["name", "other", "consumers", 0, "lag", 10],
                ["name", "target", "consumers", 1, "lag", 2],
            ],
            "target",
        )

        self.assertEqual(info["lag"], 2)


if __name__ == "__main__":
    unittest.main()
