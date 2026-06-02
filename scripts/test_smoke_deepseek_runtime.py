#!/usr/bin/env python3
"""Unit tests for DeepSeek smoke report helpers."""

from __future__ import annotations

import unittest

import smoke_deepseek_runtime as smoke


class DeepSeekSmokeReportTests(unittest.TestCase):
    def test_build_probe_report_marks_passed_and_redacts_sensitive_values(self) -> None:
        report = smoke.build_probe_report(
            {
                "provider": "deepseek",
                "model": "deepseek-test",
                "status": "healthy",
                "probe_status": "healthy",
                "probe_count": 1,
                "probe_success": 1,
                "probe_error": 0,
                "last_error_class": "",
                "last_latency_ms": 123,
                "authorization": "Bearer sk-real-secret",
                "debug": "provider echoed sk-real-secret in a diagnostic",
            },
            "fallback-model",
            ["sk-real-secret"],
        )

        self.assertEqual(report["result"], "passed")
        self.assertEqual(report["provider"], "deepseek")
        self.assertEqual(report["model"], "deepseek-test")
        self.assertEqual(report["checks"]["provider_is_deepseek"], True)
        self.assertEqual(report["checks"]["api_key_redacted"], True)
        rendered = str(report)
        self.assertNotIn("sk-real-secret", rendered)

    def test_build_probe_report_marks_failed_when_probe_is_not_healthy(self) -> None:
        report = smoke.build_probe_report(
            {
                "provider": "deepseek",
                "model": "deepseek-test",
                "status": "degraded",
                "probe_status": "degraded",
                "probe_count": 1,
                "probe_success": 0,
                "last_error_class": "auth_error",
            },
            "fallback-model",
            [],
        )

        self.assertEqual(report["result"], "failed")
        self.assertEqual(report["checks"]["status_is_healthy"], False)
        self.assertEqual(report["checks"]["probe_succeeded"], False)

    def test_build_failure_report_redacts_log_tail(self) -> None:
        report = smoke.build_failure_report(
            RuntimeError("runtime printed sk-real-secret"),
            "line 1\nAuthorization: Bearer sk-real-secret\nline 3",
            ["sk-real-secret"],
        )

        self.assertEqual(report["result"], "failed")
        rendered = str(report)
        self.assertNotIn("sk-real-secret", rendered)
        self.assertIn("[REDACTED]", rendered)

    def test_build_skip_report_is_machine_readable(self) -> None:
        report = smoke.build_skip_report("missing key", "deepseek-test")

        self.assertEqual(report["result"], "skipped")
        self.assertEqual(report["checks"]["api_key_configured"], False)
        self.assertEqual(report["checks"]["model_name_configured"], True)


if __name__ == "__main__":
    unittest.main()
