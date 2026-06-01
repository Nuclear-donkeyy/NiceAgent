#!/usr/bin/env python3
"""Smoke test the Agent Runtime DeepSeek profile with a real API key.

The script is intentionally opt-in. Without MODEL_API_KEY/DEEPSEEK_API_KEY and
MODEL_NAME/DEEPSEEK_MODEL it exits successfully with SKIP, so CI can keep the
target wired without accidentally calling a paid model provider.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run a DeepSeek Agent Runtime health-probe smoke test.")
    parser.add_argument("--timeout", type=float, default=45.0, help="seconds to wait for runtime and probe result")
    parser.add_argument("--report", default="", help="optional JSON report path; defaults to .local/deepseek-smoke/")
    parser.add_argument(
        "--require-key",
        action="store_true",
        help="fail instead of SKIP when the API key or model name is missing",
    )
    return parser.parse_args()


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def redacted(text: str, secrets: list[str]) -> str:
    value = text
    for secret in secrets:
        if secret:
            value = value.replace(secret, "[REDACTED]")
    return value


def get_json(url: str) -> dict[str, Any]:
    with urllib.request.urlopen(url, timeout=2) as response:  # noqa: S310 - localhost smoke endpoint.
        return json.loads(response.read().decode("utf-8"))


def report_path(raw: str) -> Path:
    if raw:
        return Path(raw)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return ROOT / ".local" / "deepseek-smoke" / f"{timestamp}.json"


def write_report(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def wait_for_health(url: str, timeout: float) -> dict[str, Any]:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        try:
            payload = get_json(url)
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
            last_error = str(exc)
            time.sleep(0.5)
            continue
        health = payload.get("model_provider") or {}
        if health.get("probe_count", 0) > 0:
            return payload
        time.sleep(0.5)
    raise TimeoutError(f"timed out waiting for model health probe; last_error={last_error}")


def main() -> int:
    args = parse_args()
    api_key = os.getenv("DEEPSEEK_API_KEY") or os.getenv("MODEL_API_KEY")
    model_name = os.getenv("DEEPSEEK_MODEL") or os.getenv("MODEL_NAME")
    if not api_key or not model_name:
        message = "SKIP deepseek smoke: set DEEPSEEK_API_KEY and DEEPSEEK_MODEL to run a real provider probe"
        print(message)
        return 2 if args.require_key else 0

    port = free_port()
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-deepseek-smoke-"))
    env = os.environ.copy()
    env.update(
        {
            "AGENT_RUNTIME_ADDR": f"127.0.0.1:{port}",
            "AGENT_RUNTIME_ID": "deepseek-smoke-runtime",
            "NICEAGENT_ENV": "local",
            "INTERNAL_API_TOKEN_REQUIRED": "false",
            "RUNTIME_QUEUE_MODE": "disabled",
            "MODEL_PROVIDER": "openai-compatible",
            "MODEL_PROVIDER_PROFILE": "deepseek",
            "MODEL_API_KEY": api_key,
            "MODEL_NAME": model_name,
            "MODEL_TIMEOUT_SECONDS": os.getenv("MODEL_TIMEOUT_SECONDS", "30"),
            "MODEL_HEALTH_PROBE_ENABLED": "true",
            "MODEL_HEALTH_PROBE_INITIAL_DELAY_SECONDS": "0",
            "MODEL_HEALTH_PROBE_INTERVAL_SECONDS": "3600",
            "MODEL_HEALTH_PROBE_TIMEOUT_SECONDS": os.getenv("MODEL_HEALTH_PROBE_TIMEOUT_SECONDS", "20"),
            "SANDBOX_WORKSPACE_ROOT": str(work_root / "workspaces"),
        }
    )
    if os.getenv("MODEL_BASE_URL"):
        env["MODEL_BASE_URL"] = os.getenv("MODEL_BASE_URL", "")

    process = subprocess.Popen(
        ["go", "run", "./cmd"],
        cwd=ROOT / "services" / "agent-runtime",
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    exit_code = 1
    failure: Exception | None = None
    try:
        health = wait_for_health(f"http://127.0.0.1:{port}/healthz", args.timeout)
        model_health = health.get("model_provider") or {}
        summary = {
            "checked_at": datetime.now(timezone.utc).isoformat(),
            "provider": model_health.get("provider"),
            "model": model_health.get("model") or model_name,
            "status": model_health.get("status"),
            "probe_status": model_health.get("probe_status"),
            "probe_count": model_health.get("probe_count"),
            "probe_success": model_health.get("probe_success"),
            "probe_error": model_health.get("probe_error"),
            "last_error_class": model_health.get("last_error_class"),
            "last_latency_ms": model_health.get("last_latency_ms"),
        }
        write_report(report_path(args.report), summary)
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        if summary["status"] != "healthy" or summary["probe_status"] != "healthy":
            exit_code = 1
        else:
            exit_code = 0
    except Exception as exc:  # noqa: BLE001 - smoke prints concrete failure and redacted runtime logs.
        failure = exc
        print(f"deepseek smoke failed: {exc}", file=sys.stderr)
    finally:
        if process.poll() is None:
            process.terminate()
        try:
            stdout, _ = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            stdout, _ = process.communicate(timeout=5)
        if failure and stdout:
            print(redacted("\n".join(stdout.splitlines()[-80:]), [api_key]), file=sys.stderr)
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
