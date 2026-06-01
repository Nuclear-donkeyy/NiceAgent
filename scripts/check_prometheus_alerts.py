#!/usr/bin/env python3
"""Lightweight structural checks for Prometheus alert rule files.

This intentionally avoids PyYAML/promtool so local CI can validate the
repository-owned rule file without extra runtime dependencies. It is not a full
YAML or PromQL parser; production deployments should still run promtool.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path


ALERT_RE = re.compile(r"^\s*-\s+alert:\s+([A-Za-z_:][A-Za-z0-9_:]*)\s*$")
EXPR_RE = re.compile(r"^\s*expr:\s*(?:\||>|\S.*)?$")
FOR_RE = re.compile(r"^\s*for:\s+\S+")
SEVERITY_RE = re.compile(r"^\s*severity:\s+(info|warning|critical)\s*$")
SUMMARY_RE = re.compile(r"^\s*summary:\s+")
DESCRIPTION_RE = re.compile(r"^\s*description:\s+")


def check_file(path: Path) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    errors: list[str] = []
    if not any(line.strip() == "groups:" for line in lines):
        errors.append("missing top-level groups")

    alert_lines = [(idx, match.group(1)) for idx, line in enumerate(lines) if (match := ALERT_RE.match(line))]
    if not alert_lines:
        errors.append("missing alert rules")
        return errors

    seen: set[str] = set()
    for index, (line_no, alert_name) in enumerate(alert_lines):
        if alert_name in seen:
            errors.append(f"duplicate alert {alert_name}")
        seen.add(alert_name)

        next_line = alert_lines[index + 1][0] if index + 1 < len(alert_lines) else len(lines)
        block = lines[line_no:next_line]
        required = {
            "expr": any(EXPR_RE.match(line) for line in block),
            "for": any(FOR_RE.match(line) for line in block),
            "severity": any(SEVERITY_RE.match(line) for line in block),
            "summary": any(SUMMARY_RE.match(line) for line in block),
            "description": any(DESCRIPTION_RE.match(line) for line in block),
        }
        for field, ok in required.items():
            if not ok:
                errors.append(f"{alert_name}: missing {field}")

    return errors


def main(argv: list[str]) -> int:
    paths = [Path(arg) for arg in argv[1:]]
    if not paths:
        paths = [Path("deployments/monitoring/prometheus-alerts.yml")]
    all_errors: list[str] = []
    for path in paths:
        if not path.exists():
            all_errors.append(f"{path}: file not found")
            continue
        for error in check_file(path):
            all_errors.append(f"{path}: {error}")
    if all_errors:
        for error in all_errors:
            print(error, file=sys.stderr)
        return 1
    print("Prometheus alert rule structure looks valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
