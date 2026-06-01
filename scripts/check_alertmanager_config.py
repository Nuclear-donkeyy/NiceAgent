#!/usr/bin/env python3
"""Lightweight structural checks for the Alertmanager example config.

This deliberately avoids PyYAML/amtool so repository checks can run on a plain
Python install. It validates only the small subset we own here: receiver names,
route receiver references, webhook placeholders, and basic grouping fields.
Production deployments should still run `amtool check-config`.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path


RECEIVER_RE = re.compile(r"^\s*-\s+name:\s+([A-Za-z0-9_-]+)\s*$")
ROUTE_RECEIVER_RE = re.compile(r"^\s*receiver:\s+([A-Za-z0-9_-]+)\s*$")
WEBHOOK_URL_RE = re.compile(r"^\s*-\s+url:\s+(\S+)\s*$")
GROUP_FIELD_RE = re.compile(r"^\s*-\s+(alertname|service|component|severity)\s*$")


def check_file(path: Path) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    errors: list[str] = []
    stripped = [line.strip() for line in lines]

    for required in ("global:", "route:", "receivers:"):
        if required not in stripped:
            errors.append(f"missing top-level {required[:-1]}")

    receivers = {match.group(1) for line in lines if (match := RECEIVER_RE.match(line))}
    if not receivers:
        errors.append("missing receiver definitions")

    route_receivers = [match.group(1) for line in lines if (match := ROUTE_RECEIVER_RE.match(line))]
    if not route_receivers:
        errors.append("missing route receiver references")

    for receiver in route_receivers:
        if receiver not in receivers:
            errors.append(f"route references undefined receiver {receiver}")

    if "niceagent-oncall" not in receivers:
        errors.append("missing niceagent-oncall receiver")

    group_fields = {match.group(1) for line in lines if (match := GROUP_FIELD_RE.match(line))}
    for expected in ("alertname", "service", "component", "severity"):
        if expected not in group_fields:
            errors.append(f"missing group_by field {expected}")

    webhook_urls = [match.group(1) for line in lines if (match := WEBHOOK_URL_RE.match(line))]
    if not webhook_urls:
        errors.append("missing webhook_configs url")
    for url in webhook_urls:
        if "example.invalid" not in url:
            errors.append(f"webhook url must be a placeholder, got {url}")

    if not any('severity="critical"' in line for line in lines):
        errors.append("missing critical severity route")

    if not any('component="model-provider"' in line for line in lines):
        errors.append("missing model-provider route")

    return errors


def main(argv: list[str]) -> int:
    paths = [Path(arg) for arg in argv[1:]]
    if not paths:
        paths = [Path("deployments/monitoring/alertmanager.example.yml")]

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

    print("Alertmanager example config structure looks valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
