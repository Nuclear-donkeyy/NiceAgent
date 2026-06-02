#!/usr/bin/env python3
"""Lightweight checks for repository-owned Grafana dashboard JSON files.

This is not a replacement for importing dashboards into Grafana. It catches the
mistakes that are cheap to verify in CI: invalid JSON, missing dashboard
identity, duplicate panel IDs, missing Prometheus datasource targets and missing
NiceAgent metric references.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any


REQUIRED_METRICS = {
    "niceagent_skill_rate_limit_denials_total",
    "niceagent_skill_policy_denials_total",
    "niceagent_quota_denials_total",
}


def iter_targets(panel: dict[str, Any]) -> list[dict[str, Any]]:
    targets = panel.get("targets", [])
    if not isinstance(targets, list):
        return []
    return [target for target in targets if isinstance(target, dict)]


def check_dashboard(path: Path) -> list[str]:
    errors: list[str] = []
    try:
        dashboard = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        return [f"invalid JSON: {exc}"]

    if not isinstance(dashboard, dict):
        return ["dashboard root must be an object"]

    for field in ("title", "uid", "panels"):
        if field not in dashboard:
            errors.append(f"missing {field}")

    panels = dashboard.get("panels")
    if not isinstance(panels, list) or not panels:
        errors.append("panels must be a non-empty list")
        return errors

    panel_ids: set[int] = set()
    expressions: list[str] = []
    for index, panel in enumerate(panels):
        if not isinstance(panel, dict):
            errors.append(f"panel {index}: must be an object")
            continue

        panel_id = panel.get("id")
        if not isinstance(panel_id, int):
            errors.append(f"panel {index}: missing integer id")
        elif panel_id in panel_ids:
            errors.append(f"panel {index}: duplicate id {panel_id}")
        else:
            panel_ids.add(panel_id)

        if not panel.get("title"):
            errors.append(f"panel {panel_id or index}: missing title")

        targets = iter_targets(panel)
        if not targets:
            errors.append(f"panel {panel_id or index}: missing targets")
            continue
        for target in targets:
            expr = target.get("expr")
            if not isinstance(expr, str) or not expr.strip():
                errors.append(f"panel {panel_id or index}: target missing expr")
                continue
            expressions.append(expr)

            datasource = target.get("datasource")
            if not isinstance(datasource, dict) or datasource.get("type") != "prometheus":
                errors.append(f"panel {panel_id or index}: target datasource must be prometheus")

    all_expr = "\n".join(expressions)
    for metric in sorted(REQUIRED_METRICS):
        if metric not in all_expr:
            errors.append(f"missing required metric reference {metric}")

    return errors


def main(argv: list[str]) -> int:
    if len(argv) > 1:
        paths = [Path(arg) for arg in argv[1:]]
    else:
        root = Path("deployments/monitoring/grafana/dashboards")
        paths = sorted(root.glob("*.json"))

    if not paths:
        print("No Grafana dashboard JSON files found.", file=sys.stderr)
        return 1

    all_errors: list[str] = []
    for path in paths:
        if not path.exists():
            all_errors.append(f"{path}: file not found")
            continue
        for error in check_dashboard(path):
            all_errors.append(f"{path}: {error}")

    if all_errors:
        for error in all_errors:
            print(error, file=sys.stderr)
        return 1

    print("Grafana dashboard JSON structure looks valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
