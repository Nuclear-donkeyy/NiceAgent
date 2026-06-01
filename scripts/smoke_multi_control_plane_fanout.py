#!/usr/bin/env python3
"""Verify Redis event fanout across two Control Plane processes."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import shutil
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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--timeout", type=float, default=60)
    args = parser.parse_args()

    repo = Path(__file__).resolve().parents[1]
    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-fanout-smoke-token"
    control_a_port = free_port()
    control_b_port = free_port()
    runtime_port = free_port()
    sandbox_port = free_port()
    redis_port = free_port()
    postgres_port = free_port()
    slow_port = free_port()
    suffix = f"{os.getpid()}-{int(time.time() * 1000)}"
    postgres_name = f"niceagent-fanout-postgres-{suffix}"
    redis_name = f"niceagent-fanout-redis-{suffix}"
    fanout_prefix = f"niceagent:fanout-smoke:{suffix}"
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-fanout-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    processes: list[subprocess.Popen[bytes]] = []
    slow_server: ThreadingHTTPServer | None = None

    def base_env() -> dict[str, str]:
        env = os.environ.copy()
        env["PATH"] = "/usr/local/go/bin:" + env.get("PATH", "")
        env["GOCACHE"] = env.get("GOCACHE", "/private/tmp/niceagent-go-cache")
        env["NICEAGENT_ENV"] = "local"
        env["INTERNAL_API_TOKEN"] = token
        env["INTERNAL_API_TOKEN_REQUIRED"] = "true"
        env["SANDBOX_WORKSPACE_ROOT"] = str(work_root / "workspaces")
        env["NO_PROXY"] = merge_no_proxy(env.get("NO_PROXY", ""))
        env["no_proxy"] = merge_no_proxy(env.get("no_proxy", ""))
        return env

    try:
        processes.append(
            start_service(
                repo,
                [
                    "docker",
                    "run",
                    "--rm",
                    "--name",
                    postgres_name,
                    "-e",
                    "POSTGRES_DB=niceagent",
                    "-e",
                    "POSTGRES_USER=niceagent",
                    "-e",
                    "POSTGRES_PASSWORD=niceagent",
                    "-p",
                    f"127.0.0.1:{postgres_port}:5432",
                    "-v",
                    f"{repo / 'migrations'}:/docker-entrypoint-initdb.d:ro",
                    "postgres:16-alpine",
                ],
                os.environ.copy(),
                log_root / "postgres.log",
            )
        )
        wait_postgres_container(postgres_name, args.timeout)

        processes.append(
            start_service(
                repo,
                [
                    "docker",
                    "run",
                    "--rm",
                    "--name",
                    redis_name,
                    "-p",
                    f"127.0.0.1:{redis_port}:6379",
                    "redis:7-alpine",
                ],
                os.environ.copy(),
                log_root / "redis.log",
            )
        )
        wait_tcp("127.0.0.1", redis_port, args.timeout)

        slow_server = start_slow_server(slow_port)

        sandbox_env = base_env()
        sandbox_env.update({"SANDBOX_EXECUTOR_ADDR": f":{sandbox_port}", "EXECUTOR_MODE": "local"})
        processes.append(
            start_service(repo / "services/sandbox-executor", [go_bin, "run", "./cmd"], sandbox_env, log_root / "sandbox.log")
        )
        wait_health(f"http://127.0.0.1:{sandbox_port}/healthz", args.timeout)

        control_a_url = f"http://127.0.0.1:{control_a_port}"
        control_b_url = f"http://127.0.0.1:{control_b_port}"
        runtime_env = base_env()
        runtime_env.update(
            {
                "AGENT_RUNTIME_ADDR": f":{runtime_port}",
                "AGENT_RUNTIME_ID": "fanout-smoke-runtime",
                "CONTROL_PLANE_URL": control_a_url,
                "SANDBOX_EXECUTOR_URL": f"http://127.0.0.1:{sandbox_port}",
                "MODEL_PROVIDER": "mock",
                "MODEL_TIMEOUT_SECONDS": "30",
                "RUNTIME_QUEUE_MODE": "disabled",
            }
        )
        processes.append(
            start_service(repo / "services/agent-runtime", [go_bin, "run", "./cmd"], runtime_env, log_root / "runtime.log")
        )
        wait_health(f"http://127.0.0.1:{runtime_port}/healthz", args.timeout)

        database_url = f"postgres://niceagent:niceagent@127.0.0.1:{postgres_port}/niceagent?sslmode=disable"
        redis_addr = f"127.0.0.1:{redis_port}"
        for name, port, public_url, log_name in (
            ("control-a", control_a_port, control_a_url, "control-a.log"),
            ("control-b", control_b_port, control_b_url, "control-b.log"),
        ):
            control_env = base_env()
            control_env.update(
                {
                    "CONTROL_PLANE_ADDR": f":{port}",
                    "AUTH_MODE": "demo",
                    "STORE_DRIVER": "postgres",
                    "DATABASE_URL": database_url,
                    "DISPATCH_MODE": "http",
                    "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_port}",
                    "CONTROL_PLANE_PUBLIC_URL": public_url,
                    "EVENT_FANOUT_MODE": "redis",
                    "EVENT_FANOUT_PREFIX": fanout_prefix,
                    "REDIS_ADDR": redis_addr,
                    "WEB_DIST_DIR": str(repo / "frontend/dist"),
                }
            )
            processes.append(
                start_service(repo / "services/control-plane", [go_bin, "run", "./cmd"], control_env, log_root / log_name)
            )
            wait_health(f"http://127.0.0.1:{port}/healthz", args.timeout)

        chat = post_json(f"{control_a_url}/api/chats", {"title": "Fanout smoke"}, token)
        chat_id = str(chat["id"])
        response = post_json(
            f"{control_a_url}/api/chats/{chat_id}/messages",
            {"content": f"/cli curl -s http://127.0.0.1:{slow_port}/hello-fanout"},
            token,
        )
        run_id = str(response["run"]["id"])
        events = read_sse_events(f"{control_b_url}/api/runs/{run_id}/events", token, args.timeout)
        assistant = wait_assistant_message(control_b_url, chat_id, run_id, args.timeout, token)
        event_types = [event.get("type") for event in events]
        if "run.succeeded" not in event_types:
            raise RuntimeError(f"control-b SSE did not receive run.succeeded: {event_types}")
        if "tool.output" not in event_types or "model.token" not in event_types:
            raise RuntimeError(f"control-b SSE did not receive tool output and model token events: {event_types}")
        if "hello-fanout" not in json.dumps(events, ensure_ascii=False) or "hello-fanout" not in assistant:
            raise RuntimeError(f"fanout smoke did not observe slow CLI output: assistant={assistant!r} events={events!r}")

        print(
            json.dumps(
                {
                    "ok": True,
                    "run_id": run_id,
                    "chat_id": chat_id,
                    "control_plane_writer": control_a_url,
                    "control_plane_sse": control_b_url,
                    "event_types": event_types,
                    "assistant": assistant,
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:
        print(f"fanout smoke failed: {exc}", file=sys.stderr)
        print(f"logs: {log_root}", file=sys.stderr)
        for path in sorted(log_root.glob("*.log")):
            print(f"\n--- {path.name} ---", file=sys.stderr)
            with contextlib.suppress(OSError):
                print(path.read_text(errors="replace")[-4000:], file=sys.stderr)
        return 1
    finally:
        if slow_server is not None:
            slow_server.shutdown()
            slow_server.server_close()
        for process in reversed(processes):
            stop_process(process)
        shutil.rmtree(work_root, ignore_errors=True)


def start_slow_server(port: int) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler uses this naming style.
            time.sleep(2)
            body = b"hello-fanout"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, _format: str, *_args: object) -> None:
            return

    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def read_sse_events(url: str, token: str, timeout: float) -> list[dict[str, object]]:
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"}, method="GET")
    deadline = time.time() + timeout
    events: list[dict[str, object]] = []
    event_name = ""
    data_lines: list[str] = []
    with urllib.request.urlopen(request, timeout=timeout) as response:
        while time.time() < deadline:
            line = response.readline()
            if not line:
                break
            text = line.decode("utf-8", errors="replace").rstrip("\n")
            if text.endswith("\r"):
                text = text[:-1]
            if text == "":
                if data_lines:
                    payload = json.loads("\n".join(data_lines))
                    payload["_sse_event"] = event_name
                    events.append(payload)
                    if payload.get("type") == "run.succeeded":
                        return events
                event_name = ""
                data_lines = []
                continue
            if text.startswith("event:"):
                event_name = text.removeprefix("event:").strip()
            elif text.startswith("data:"):
                data_lines.append(text.removeprefix("data:").strip())
    raise RuntimeError(f"timed out waiting for run.succeeded from {url}; got {events}")


def start_service(cwd: Path, cmd: list[str], env: dict[str, str], log_path: Path) -> subprocess.Popen[bytes]:
    log_file = log_path.open("wb")
    return subprocess.Popen(cmd, cwd=str(cwd), env=env, stdout=log_file, stderr=subprocess.STDOUT)


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_tcp(host: str, port: int, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with socket.create_connection((host, port), timeout=1):
                return
        except Exception as exc:  # noqa: BLE001
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"tcp check timed out for {host}:{port}: {last_error}")


def wait_health(url: str, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.5) as response:
                if response.status == 200:
                    return
        except Exception as exc:  # noqa: BLE001
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"health check timed out for {url}: {last_error}")


def wait_postgres_container(name: str, timeout: float) -> None:
    deadline = time.time() + timeout
    last_output = ""
    while time.time() < deadline:
        completed = subprocess.run(
            ["docker", "exec", name, "pg_isready", "-U", "niceagent", "-d", "niceagent"],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=3,
            check=False,
        )
        last_output = completed.stdout.strip()
        if completed.returncode == 0:
            return
        time.sleep(0.5)
    raise RuntimeError(f"postgres container did not become ready: {last_output}")


def post_json(url: str, payload: dict[str, object], token: str) -> dict[str, object]:
    data = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {token}"},
        method="POST",
    )
    return read_json(request)


def get_json(url: str, token: str) -> dict[str, object]:
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"}, method="GET")
    return read_json(request)


def read_json(request: urllib.request.Request) -> dict[str, object]:
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{request.full_url} returned {exc.code}: {body}") from exc


def wait_assistant_message(control_url: str, chat_id: str, run_id: str, timeout: float, token: str) -> str:
    deadline = time.time() + timeout
    while time.time() < deadline:
        run = get_json(f"{control_url}/api/runs/{run_id}", token)
        status = run.get("status")
        chat = get_json(f"{control_url}/api/chats/{chat_id}", token)
        for message in chat.get("messages", []):
            if message.get("role") == "assistant" and message.get("run_id") == run_id:
                return str(message.get("content", ""))
        if status in {"failed", "canceled"}:
            raise RuntimeError(f"run ended with status {status}: {run}")
        time.sleep(0.4)
    raise RuntimeError(f"timed out waiting for assistant message for run {run_id}")


def merge_no_proxy(current: str) -> str:
    items = [item.strip() for item in current.split(",") if item.strip()]
    for value in ("127.0.0.1", "localhost", "::1"):
        if value not in items:
            items.append(value)
    return ",".join(items)


if __name__ == "__main__":
    raise SystemExit(main())
