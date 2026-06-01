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


def main(argv: list[str]) -> int:
    path = Path(argv[1]) if len(argv) > 1 else Path("deployments/k8s/sandbox-hardening.yaml")
    if not path.exists():
        print(f"{path}: file not found", file=sys.stderr)
        return 1

    errors = check_file(path)
    if errors:
        for error in errors:
            print(f"{path}: {error}", file=sys.stderr)
        return 1

    print("K8s sandbox hardening manifest structure looks valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
