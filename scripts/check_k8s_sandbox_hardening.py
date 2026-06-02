#!/usr/bin/env python3
"""Lightweight checks for the repository-owned sandbox hardening manifest.

This is intentionally structural rather than a full Kubernetes schema
validator. CI can run it without kubectl, kubeconform, or PyYAML.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path


REQUIRED_SNIPPETS = (
    "kind: LimitRange",
    "kind: ResourceQuota",
    "name: niceagent-sandbox-executor-ingress",
    "name: niceagent-sandbox-executor-egress",
    "policyTypes:",
    "- Ingress",
    "- Egress",
    "kubernetes.io/metadata.name: kube-system",
    "port: 53",
    "port: 4317",
    "port: 4318",
    "cidr: 0.0.0.0/0",
    "10.0.0.0/8",
    "100.64.0.0/10",
    "127.0.0.0/8",
    "169.254.0.0/16",
    "172.16.0.0/12",
    "192.168.0.0/16",
)


def document_named_block(text: str, name: str) -> str:
    docs = re.split(r"^---\s*$", text, flags=re.MULTILINE)
    for doc in docs:
        if f"name: {name}" in doc:
            return doc
    return ""


def check_file(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    errors: list[str] = []
    for snippet in REQUIRED_SNIPPETS:
        if snippet not in text:
            errors.append(f"missing {snippet}")

    ingress = document_named_block(text, "niceagent-sandbox-executor-ingress")
    if not ingress:
        errors.append("missing sandbox ingress NetworkPolicy")
    elif "app: niceagent-agent-runtime" not in ingress or "port: 8082" not in ingress:
        errors.append("sandbox ingress policy must only expose port 8082 from agent runtime pods")

    egress = document_named_block(text, "niceagent-sandbox-executor-egress")
    if not egress:
        errors.append("missing sandbox egress NetworkPolicy")
    else:
        if "podSelector: {}" not in egress:
            errors.append("sandbox egress policy should explicitly scope same-namespace pod egress")
        if "ipBlock:" not in egress or "except:" not in egress:
            errors.append("sandbox egress policy should use ipBlock except ranges")

    return errors


def check_runtimeclass_file(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    errors: list[str] = []
    required = (
        "kind: RuntimeClass",
        "name: niceagent-sandbox",
        "handler: runsc",
        "scheduling:",
        "niceagent.io/node-pool: sandbox",
        "kind: Deployment",
        "name: niceagent-sandbox-executor",
        "runtimeClassName: niceagent-sandbox",
        "key: niceagent.io/sandbox",
        "effect: NoSchedule",
    )
    for snippet in required:
        if snippet not in text:
            errors.append(f"missing runtimeclass profile snippet {snippet}")

    if "optional" not in text.lower() or "do not apply" not in text.lower():
        errors.append("runtimeclass profile must clearly say it is optional and not part of the default apply path")

    return errors


def main(argv: list[str]) -> int:
    paths = [Path(arg) for arg in argv[1:]]
    if not paths:
        paths = [Path("deployments/k8s/sandbox-hardening.yaml")]

    errors: list[str] = []
    for path in paths:
        if not path.exists():
            errors.append(f"{path}: file not found")
            continue
        checker = check_runtimeclass_file if "runtimeclass" in path.name else check_file
        for error in checker(path):
            errors.append(f"{path}: {error}")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print("K8s sandbox hardening manifests look valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
