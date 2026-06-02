#!/usr/bin/env python3
"""Verify real-service SSE reconnect replay does not duplicate run events."""

from __future__ import annotations

import contextlib
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
    timeout = float(os.environ.get("SSE_REPLAY_SMOKE_TIMEOUT", "60"))
    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-sse-replay-smoke-token"
    control_port = free_port()
    runtime_port = free_port()
    sandbox_port = free_port()
    slow_port = free_port()
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-sse-replay-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    processes: list[subprocess.Popen[bytes]] = []
    slow_server: ThreadingHTTPServer | None = None

    def base_env() -> dict[str, str]:
        env = os.environ.copy()
        env["PATH"] = "/usr/local/go/bin:" + env.get("PATH", "")
        env["GOCACHE"] = env.get("GOCACHE") or str(Path(tempfile.gettempdir()) / "niceagent-go-cache")
        env["NICEAGENT_ENV"] = "local"
        env["INTERNAL_API_TOKEN"] = token
        env["INTERNAL_API_TOKEN_REQUIRED"] = "true"
        env["SANDBOX_WORKSPACE_ROOT"] = str(work_root / "workspaces")
        env["NO_PROXY"] = merge_no_proxy(env.get("NO_PROXY", ""))
        env["no_proxy"] = merge_no_proxy(env.get("no_proxy", ""))
        return env

    try:
        slow_server = start_slow_server(slow_port)

        sandbox_env = base_env()
        sandbox_env.update({"SANDBOX_EXECUTOR_ADDR": f":{sandbox_port}", "EXECUTOR_MODE": "local"})
        processes.append(
            start_service(ROOT / "services/sandbox-executor", [go_bin, "run", "./cmd"], sandbox_env, log_root / "sandbox.log")
        )
        wait_health(f"http://127.0.0.1:{sandbox_port}/healthz", timeout)

        control_url = f"http://127.0.0.1:{control_port}"
        runtime_env = base_env()
        runtime_env.update(
            {
                "AGENT_RUNTIME_ADDR": f":{runtime_port}",
                "AGENT_RUNTIME_ID": "sse-replay-smoke-runtime",
                "CONTROL_PLANE_URL": control_url,
                "SANDBOX_EXECUTOR_URL": f"http://127.0.0.1:{sandbox_port}",
                "MODEL_PROVIDER": "mock",
                "MODEL_TIMEOUT_SECONDS": "30",
                "RUNTIME_QUEUE_MODE": "disabled",
            }
        )
        processes.append(
            start_service(ROOT / "services/agent-runtime", [go_bin, "run", "./cmd"], runtime_env, log_root / "runtime.log")
        )
        wait_health(f"http://127.0.0.1:{runtime_port}/healthz", timeout)

        control_env = base_env()
        control_env.update(
            {
                "CONTROL_PLANE_ADDR": f":{control_port}",
                "AUTH_MODE": "demo",
                "STORE_DRIVER": "memory",
                "DISPATCH_MODE": "http",
                "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_port}",
                "CONTROL_PLANE_PUBLIC_URL": control_url,
                "EVENT_FANOUT_MODE": "local",
                "WEB_DIST_DIR": str(ROOT / "frontend/dist"),
            }
        )
        processes.append(
            start_service(ROOT / "services/control-plane", [go_bin, "run", "./cmd"], control_env, log_root / "control.log")
        )
        wait_health(f"{control_url}/healthz", timeout)

        chat = post_json(f"{control_url}/api/chats", {"title": "SSE replay smoke"}, token)
        chat_id = str(chat["id"])
        response = post_json(
            f"{control_url}/api/chats/{chat_id}/messages",
            {"content": f"/cli curl -s http://127.0.0.1:{slow_port}/hello-sse-replay"},
            token,
        )
        run_id = str(response["run"]["id"])
        first_events = read_sse_events_until_count(f"{control_url}/api/runs/{run_id}/events", token, 2, timeout)
        last_seq = max(int(event["seq"]) for event in first_events if int(event.get("seq", 0)) > 0)
        replayed_events = read_sse_events_until_type(
            f"{control_url}/api/runs/{run_id}/events?after={last_seq}",
            token,
            "run.succeeded",
            timeout,
        )
        if any(int(event.get("seq", 0)) <= last_seq for event in replayed_events):
            raise RuntimeError(f"replayed duplicate or stale seq after {last_seq}: {replayed_events}")
        merged = first_events + replayed_events
        assert_strictly_increasing([int(event["seq"]) for event in merged if int(event.get("seq", 0)) > 0])
        event_types = [str(event.get("type", "")) for event in merged]
        if "tool.output" not in event_types or "model.token" not in event_types or "run.succeeded" not in event_types:
            raise RuntimeError(f"reconnected SSE did not observe expected event types: {event_types}")
        assistant = wait_assistant_message(control_url, chat_id, run_id, timeout, token)
        if "hello-sse-replay" not in assistant:
            raise RuntimeError(f"assistant message did not include replay smoke output: {assistant!r}")

        print(
            json.dumps(
                {
                    "ok": True,
                    "run_id": run_id,
                    "chat_id": chat_id,
                    "first_connection_events": event_types[: len(first_events)],
                    "replayed_events": [str(event.get("type", "")) for event in replayed_events],
                    "last_seq_before_reconnect": last_seq,
                    "final_seq": max(int(event["seq"]) for event in merged),
                    "assistant": assistant,
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - smoke prints concrete diagnostics.
        print(f"sse replay smoke failed: {exc}", file=sys.stderr)
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


def start_slow_server(port: int) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler uses this naming style.
            time.sleep(2)
            body = b"hello-sse-replay"
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


def read_sse_events_until_count(url: str, token: str, count: int, timeout: float) -> list[dict[str, Any]]:
    return read_sse_events(url, token, timeout, stop_after_count=count)


def read_sse_events_until_type(url: str, token: str, event_type: str, timeout: float) -> list[dict[str, Any]]:
    return read_sse_events(url, token, timeout, stop_after_type=event_type)


def read_sse_events(
    url: str,
    token: str,
    timeout: float,
    *,
    stop_after_count: int = 0,
    stop_after_type: str = "",
) -> list[dict[str, Any]]:
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"}, method="GET")
    deadline = time.time() + timeout
    events: list[dict[str, Any]] = []
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
                    if stop_after_count and len(events) >= stop_after_count:
                        return events
                    if stop_after_type and payload.get("type") == stop_after_type:
                        return events
                event_name = ""
                data_lines = []
                continue
            if text.startswith("event:"):
                event_name = text.removeprefix("event:").strip()
            elif text.startswith("data:"):
                data_lines.append(text.removeprefix("data:").strip())
    raise RuntimeError(f"timed out reading SSE events from {url}; got {events}")


def assert_strictly_increasing(values: list[int]) -> None:
    for previous, current in zip(values, values[1:]):
        if current <= previous:
            raise RuntimeError(f"SSE seq values are not strictly increasing: {values}")


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


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def merge_no_proxy(value: str) -> str:
    entries = [item.strip() for item in value.split(",") if item.strip()]
    for item in ("127.0.0.1", "localhost"):
        if item not in entries:
            entries.append(item)
    return ",".join(entries)


if __name__ == "__main__":
    raise SystemExit(main())
