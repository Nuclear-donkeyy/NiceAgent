#!/usr/bin/env python3
"""Smoke test the Sandbox Executor container mode.

This smoke is intentionally opt-in friendly: if Docker CLI/daemon is not
available it exits successfully with SKIP, so regular CI and local machines
without Docker do not fail. When Docker is available, the test disables local
fallback and proves that the sandbox service can execute a simple command
through the container executor.
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    image = os.environ.get("SANDBOX_CONTAINER_IMAGE", "alpine:3.20")
    if not docker_available():
        print("SKIP sandbox container smoke: Docker CLI or daemon is not available")
        return 0

    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"
    port = free_port()
    token = "niceagent-sandbox-container-smoke-token"
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-sandbox-container-"))
    log_path = work_root / "sandbox.log"
    env = os.environ.copy()
    env.update(
        {
            "PATH": "/usr/local/go/bin:" + env.get("PATH", ""),
            "GOCACHE": env.get("GOCACHE") or str(Path(tempfile.gettempdir()) / "niceagent-go-cache"),
            "NICEAGENT_ENV": "local",
            "INTERNAL_API_TOKEN": token,
            "INTERNAL_API_TOKEN_REQUIRED": "true",
            "SANDBOX_EXECUTOR_ADDR": f"127.0.0.1:{port}",
            "SANDBOX_WORKSPACE_ROOT": str(work_root / "workspaces"),
            "EXECUTOR_MODE": "container",
            "SANDBOX_CONTAINER_IMAGE": image,
            "SANDBOX_CONTAINER_ALLOWED_IMAGES": image,
            "SANDBOX_CONTAINER_LOCAL_FALLBACK": "false",
        }
    )
    process = subprocess.Popen(
        [go_bin, "run", "./cmd"],
        cwd=ROOT / "services" / "sandbox-executor",
        env=env,
        stdout=log_path.open("wb"),
        stderr=subprocess.STDOUT,
    )
    try:
        health = wait_json(f"http://127.0.0.1:{port}/healthz", timeout=30)
        if health.get("executor_mode") != "container":
            raise RuntimeError(f"health executor_mode = {health.get('executor_mode')!r}, want container")
        if health.get("container_image") != image:
            raise RuntimeError(f"health container_image = {health.get('container_image')!r}, want {image!r}")
        result = post_json(
            f"http://127.0.0.1:{port}/internal/sandbox/exec",
            token,
            {
                "run_id": "run-sandbox-container-smoke",
                "workspace_id": "ws-sandbox-container-smoke",
                "command": ["echo", "hello-container"],
                "timeout_seconds": 15,
                "network": False,
            },
        )
        if result.get("exit_code") != 0:
            raise RuntimeError(f"container exec failed: {json.dumps(result, ensure_ascii=False)}")
        if "hello-container" not in str(result.get("stdout", "")):
            raise RuntimeError(f"container stdout = {result.get('stdout')!r}, want hello-container")
        print(
            json.dumps(
                {
                    "ok": True,
                    "executor_mode": health.get("executor_mode"),
                    "container_image": health.get("container_image"),
                    "workspace_root": str(work_root / "workspaces"),
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - smoke prints logs for diagnosis.
        print(f"sandbox container smoke failed: {exc}", file=sys.stderr)
        if log_path.exists():
            print(f"\n--- sandbox.log ---\n{log_path.read_text(errors='replace')[-4000:]}", file=sys.stderr)
        return 1
    finally:
        stop_process(process)


def docker_available() -> bool:
    docker_bin = os.environ.get("DOCKER", "docker")
    try:
        subprocess.run([docker_bin, "info"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=8, check=True)
    except (OSError, subprocess.SubprocessError):
        return False
    return True


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_json(url: str, timeout: float) -> dict[str, Any]:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.5) as response:  # noqa: S310 - localhost smoke endpoint.
                return json.loads(response.read().decode("utf-8"))
        except Exception as exc:  # noqa: BLE001 - smoke keeps last startup error.
            last_error = exc
            time.sleep(0.25)
    raise RuntimeError(f"health check timed out for {url}: {last_error}")


def post_json(url: str, token: str, payload: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        method="POST",
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:  # noqa: S310 - localhost smoke endpoint.
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"POST {url} failed with {exc.code}: {body}") from exc


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


if __name__ == "__main__":
    raise SystemExit(main())

