#!/usr/bin/env python3
"""Verify artifact expiration cleanup and local file deletion through real Control Plane APIs."""

from __future__ import annotations

import contextlib
import datetime as dt
import hashlib
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    timeout = float(os.environ.get("ARTIFACT_CLEANUP_SMOKE_TIMEOUT", "45"))
    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-artifact-cleanup-smoke-token"
    control_port = free_port()
    runtime_port = free_port()
    control_url = f"http://127.0.0.1:{control_port}"
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-artifact-cleanup-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    workspace_root = work_root / "workspaces"
    process: subprocess.Popen[bytes] | None = None
    fake_runtime: ThreadingHTTPServer | None = None

    env = os.environ.copy()
    env.update(
        {
            "PATH": "/usr/local/go/bin:" + env.get("PATH", ""),
            "GOCACHE": env.get("GOCACHE") or str(Path(tempfile.gettempdir()) / "niceagent-go-cache"),
            "NICEAGENT_ENV": "local",
            "CONTROL_PLANE_ADDR": f":{control_port}",
            "CONTROL_PLANE_PUBLIC_URL": control_url,
            "AUTH_MODE": "demo",
            "STORE_DRIVER": "memory",
            "DISPATCH_MODE": "http",
            "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_port}",
            "INTERNAL_API_TOKEN": token,
            "INTERNAL_API_TOKEN_REQUIRED": "true",
            "SANDBOX_WORKSPACE_ROOT": str(workspace_root),
            "WEB_DIST_DIR": str(ROOT / "frontend/dist"),
        }
    )

    try:
        fake_runtime = start_blocking_runtime(runtime_port)
        process = start_service(ROOT / "services/control-plane", [go_bin, "run", "./cmd"], env, log_root / "control.log")
        wait_health(f"{control_url}/healthz", timeout)

        chat = post_json(f"{control_url}/api/chats", {"title": "Artifact cleanup smoke"}, token)
        chat_id = str(chat["id"])
        response = post_json(
            f"{control_url}/api/chats/{chat_id}/messages",
            {"content": "artifact cleanup smoke"},
            token,
        )
        run = response["run"]
        run_id = str(run["id"])
        workspace_id = str(run["workspace_id"])
        attempt_id = "attempt-artifact-cleanup-smoke"
        post_json(
            f"{control_url}/internal/runs/{run_id}/claim",
            {"attempt_id": attempt_id, "claimed_by": "artifact-cleanup-smoke", "lease_seconds": 60},
            token,
        )

        output_dir = workspace_root / workspace_id / "output"
        output_dir.mkdir(parents=True, exist_ok=True)
        artifact_path = output_dir / "expired.txt"
        content = b"expired artifact smoke"
        artifact_path.write_bytes(content)
        expires_at = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1)).isoformat().replace("+00:00", "Z")
        registered = post_json(
            f"{control_url}/internal/runs/{run_id}/artifacts",
            {
                "attempt_id": attempt_id,
                "artifacts": [
                    {
                        "path": "output/expired.txt",
                        "name": "expired.txt",
                        "mime_type": "text/plain",
                        "size_bytes": len(content),
                        "sha256": hashlib.sha256(content).hexdigest(),
                        "storage_backend": "local",
                        "expires_at": expires_at,
                    }
                ],
            },
            token,
        )
        artifacts = registered.get("artifacts", [])
        if len(artifacts) != 1:
            raise RuntimeError(f"artifact registration returned {registered!r}")
        artifact_id = str(artifacts[0]["id"])

        visible = get_json(f"{control_url}/api/runs/{run_id}/artifacts", token)
        if visible.get("artifacts") != []:
            raise RuntimeError(f"expired artifact should be hidden before cleanup, got {visible!r}")

        cleanup = post_json(
            f"{control_url}/internal/artifacts/cleanup-expired",
            {"limit": 10, "delete_files": True},
            token,
        )
        cleaned = cleanup.get("artifacts", [])
        if len(cleaned) != 1 or cleaned[0].get("id") != artifact_id:
            raise RuntimeError(f"cleanup did not return expired artifact {artifact_id}: {cleanup!r}")
        if cleanup.get("deleted_files") != 1 or cleanup.get("file_errors"):
            raise RuntimeError(f"cleanup local file result was unexpected: {cleanup!r}")
        if artifact_path.exists():
            raise RuntimeError(f"expired local artifact file still exists: {artifact_path}")

        after_cleanup = get_json(f"{control_url}/api/runs/{run_id}/artifacts", token)
        if after_cleanup.get("artifacts") != []:
            raise RuntimeError(f"deleted artifact should remain hidden after cleanup, got {after_cleanup!r}")

        print(
            json.dumps(
                {
                    "ok": True,
                    "run_id": run_id,
                    "chat_id": chat_id,
                    "workspace_id": workspace_id,
                    "artifact_id": artifact_id,
                    "deleted_files": cleanup.get("deleted_files"),
                    "file_errors": cleanup.get("file_errors", []),
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - smoke prints concrete diagnostics.
        print(f"artifact cleanup smoke failed: {exc}", file=sys.stderr)
        print(f"logs: {log_root}", file=sys.stderr)
        for path in sorted(log_root.glob("*.log")):
            print(f"\n--- {path.name} ---", file=sys.stderr)
            with contextlib.suppress(OSError):
                print(path.read_text(errors="replace")[-4000:], file=sys.stderr)
        return 1
    finally:
        if fake_runtime is not None:
            fake_runtime.shutdown()
            fake_runtime.server_close()
        if process is not None:
            stop_process(process)


def start_blocking_runtime(port: int) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler uses this naming style.
            if self.path == "/internal/runs/execute":
                time.sleep(30)
                body = b'{"status":"accepted"}'
                self.send_response(202)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                with contextlib.suppress(BrokenPipeError):
                    self.wfile.write(body)
                return
            self.send_response(404)
            self.end_headers()

        def log_message(self, _format: str, *_args: object) -> None:
            return

    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def start_service(cwd: Path, cmd: list[str], env: dict[str, str], log_path: Path) -> subprocess.Popen[bytes]:
    log_file = log_path.open("wb")
    return subprocess.Popen(cmd, cwd=str(cwd), env=env, stdout=log_file, stderr=subprocess.STDOUT)


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_health(url: str, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.5) as response:
                if response.status == 200:
                    return
        except Exception as exc:  # noqa: BLE001 - smoke script prints the last concrete failure.
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"health check timed out for {url}: {last_error}")


def post_json(url: str, payload: dict[str, object], token: str) -> dict[str, Any]:
    data = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"},
        method="POST",
    )
    return read_json(request)


def get_json(url: str, token: str) -> dict[str, Any]:
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"}, method="GET")
    return read_json(request)


def read_json(request: urllib.request.Request) -> dict[str, Any]:
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{request.full_url} returned {exc.code}: {body}") from exc


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


if __name__ == "__main__":
    raise SystemExit(main())
