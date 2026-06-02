#!/usr/bin/env python3
"""Verify a real HTTP Skill backend flow through Control Plane and Agent Runtime."""

from __future__ import annotations

import contextlib
import json
import os
import signal
import socket
import ssl
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
    timeout = float(os.environ.get("HTTP_SKILL_BACKEND_SMOKE_TIMEOUT", "60"))
    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-http-skill-smoke-token"
    control_port = free_port()
    runtime_port = free_port()
    sandbox_port = free_port()
    model_port = free_port()
    skill_port = free_port()
    control_url = f"http://127.0.0.1:{control_port}"
    model_url = f"http://127.0.0.1:{model_port}"
    skill_url = f"https://127.0.0.1:{skill_port}/lookup"
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-http-skill-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    processes: list[subprocess.Popen[bytes]] = []
    servers: list[ThreadingHTTPServer] = []

    try:
        cert_path, key_path = generate_localhost_cert(work_root)
        skill_backend = HTTPSkillBackend(skill_port, cert_path, key_path)
        model_backend = FakeToolCallingModel(model_port)
        servers.extend([skill_backend.server, model_backend.server])

        base_env = os.environ.copy()
        base_env["PATH"] = "/usr/local/go/bin:" + base_env.get("PATH", "")
        base_env["GOCACHE"] = base_env.get("GOCACHE") or str(Path(tempfile.gettempdir()) / "niceagent-go-cache")
        base_env["NICEAGENT_ENV"] = "local"
        base_env["INTERNAL_API_TOKEN"] = token
        base_env["INTERNAL_API_TOKEN_REQUIRED"] = "true"
        base_env["SANDBOX_WORKSPACE_ROOT"] = str(work_root / "workspaces")

        sandbox_env = base_env.copy()
        sandbox_env.update({"SANDBOX_EXECUTOR_ADDR": f":{sandbox_port}", "EXECUTOR_MODE": "local"})
        processes.append(
            start_service(ROOT / "services/sandbox-executor", [go_bin, "run", "./cmd"], sandbox_env, log_root / "sandbox.log")
        )
        wait_health(f"http://127.0.0.1:{sandbox_port}/healthz", timeout)

        runtime_env = base_env.copy()
        runtime_env.update(
            {
                "AGENT_RUNTIME_ADDR": f":{runtime_port}",
                "AGENT_RUNTIME_ID": "http-skill-smoke-runtime",
                "CONTROL_PLANE_URL": control_url,
                "SANDBOX_EXECUTOR_URL": f"http://127.0.0.1:{sandbox_port}",
                "MODEL_PROVIDER": "openai-compatible",
                "MODEL_BASE_URL": model_url,
                "MODEL_API_KEY": "sk-http-skill-smoke",
                "MODEL_NAME": "fake-http-skill-tool-model",
                "MODEL_TIMEOUT_SECONDS": "30",
                "HTTP_SKILL_ALLOW_LOCAL_TARGETS": "true",
                "HTTP_SKILL_CA_FILE": str(cert_path),
            }
        )
        processes.append(
            start_service(ROOT / "services/agent-runtime", [go_bin, "run", "./cmd"], runtime_env, log_root / "runtime.log")
        )
        wait_health(f"http://127.0.0.1:{runtime_port}/healthz", timeout)

        control_env = base_env.copy()
        control_env.update(
            {
                "CONTROL_PLANE_ADDR": f":{control_port}",
                "AUTH_MODE": "demo",
                "STORE_DRIVER": "memory",
                "DISPATCH_MODE": "http",
                "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_port}",
                "CONTROL_PLANE_PUBLIC_URL": control_url,
                "WEB_DIST_DIR": str(ROOT / "frontend/dist"),
            }
        )
        processes.append(
            start_service(ROOT / "services/control-plane", [go_bin, "run", "./cmd"], control_env, log_root / "control.log")
        )
        wait_health(f"{control_url}/healthz", timeout)

        skill = post_json(
            f"{control_url}/api/skills/http",
            {
                "name": "Smoke Lookup",
                "description": "Return a deterministic answer from a local HTTPS smoke backend.",
                "method": "POST",
                "url": skill_url,
                "input_schema": json.dumps(
                    {
                        "type": "object",
                        "required": ["query"],
                        "properties": {"query": {"type": "string"}},
                        "additionalProperties": False,
                    }
                ),
                "output_schema": json.dumps(
                    {
                        "type": "object",
                        "required": ["ok", "answer"],
                        "properties": {"ok": {"type": "boolean"}, "answer": {"type": "string"}, "query": {"type": "string"}},
                        "additionalProperties": True,
                    }
                ),
                "timeout_seconds": 5,
                "auth_type": "none",
            },
            token,
        )
        skill_id = str(skill["id"])

        chat = post_json(f"{control_url}/api/chats", {"title": "HTTP Skill backend smoke"}, token)
        chat_id = str(chat["id"])
        response = post_json(
            f"{control_url}/api/chats/{chat_id}/messages",
            {"content": "请调用我的 HTTP Skill 查询 niceagent-smoke"},
            token,
        )
        run_id = str(response["run"]["id"])
        assistant = wait_assistant_message(control_url, chat_id, run_id, timeout, token)
        events = read_sse_events_until_type(f"{control_url}/api/runs/{run_id}/events", token, "run.succeeded", timeout)

        if not skill_backend.requests:
            raise RuntimeError("HTTP Skill backend did not receive a request")
        backend_request = skill_backend.requests[0]
        if backend_request.get("query") != "niceagent-smoke":
            raise RuntimeError(f"HTTP Skill backend request = {backend_request!r}, want query niceagent-smoke")
        if "skill says niceagent-smoke" not in assistant:
            raise RuntimeError(f"assistant message did not include HTTP Skill result: {assistant!r}")
        if not any(event.get("type") == "tool.started" for event in events):
            raise RuntimeError(f"run events did not include tool.started: {events!r}")
        if not any(event.get("type") == "tool.output" and skill_id in json.dumps(event, ensure_ascii=False) for event in events):
            raise RuntimeError(f"run events did not include HTTP Skill tool.output for {skill_id}: {events!r}")
        if model_backend.tool_call_name == "":
            raise RuntimeError("fake model did not observe a user HTTP Skill tool")

        print(
            json.dumps(
                {
                    "ok": True,
                    "chat_id": chat_id,
                    "run_id": run_id,
                    "skill_id": skill_id,
                    "tool_name": model_backend.tool_call_name,
                    "skill_backend_requests": len(skill_backend.requests),
                    "model_requests": model_backend.requests,
                    "assistant": assistant,
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:  # noqa: BLE001 - smoke prints concrete diagnostics.
        print(f"HTTP Skill backend smoke failed: {exc}", file=sys.stderr)
        print(f"logs: {log_root}", file=sys.stderr)
        for path in sorted(log_root.glob("*.log")):
            print(f"\n--- {path.name} ---", file=sys.stderr)
            with contextlib.suppress(OSError):
                print(path.read_text(errors="replace")[-4000:], file=sys.stderr)
        return 1
    finally:
        for server in servers:
            server.shutdown()
            server.server_close()
        for process in reversed(processes):
            stop_process(process)


class HTTPSkillBackend:
    def __init__(self, port: int, cert_path: Path, key_path: Path) -> None:
        self.requests: list[dict[str, Any]] = []

        parent = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler uses this naming style.
                if self.path != "/lookup":
                    self.send_response(404)
                    self.end_headers()
                    return
                length = int(self.headers.get("Content-Length", "0"))
                body = self.rfile.read(length).decode("utf-8")
                payload = json.loads(body) if body else {}
                parent.requests.append(payload)
                query = str(payload.get("query", ""))
                response = json.dumps({"ok": True, "answer": f"skill says {query}", "query": query}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(response)))
                self.end_headers()
                self.wfile.write(response)

            def log_message(self, _format: str, *_args: object) -> None:
                return

        server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(cert_path), str(key_path))
        server.socket = context.wrap_socket(server.socket, server_side=True)
        self.server = server
        threading.Thread(target=server.serve_forever, daemon=True).start()


class FakeToolCallingModel:
    def __init__(self, port: int) -> None:
        self.requests = 0
        self.tool_call_name = ""
        parent = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler uses this naming style.
                if self.path not in {"/chat/completions", "/v1/chat/completions"}:
                    self.send_response(404)
                    self.end_headers()
                    return
                parent.requests += 1
                length = int(self.headers.get("Content-Length", "0"))
                request = json.loads(self.rfile.read(length).decode("utf-8"))
                messages = request.get("messages", [])
                tool_message = next((message for message in reversed(messages) if message.get("role") == "tool"), None)
                if tool_message is not None:
                    content = "HTTP Skill 后端返回：" + str(tool_message.get("content", ""))
                    parent.write_json(self, {
                        "id": "chatcmpl-http-skill-final",
                        "object": "chat.completion",
                        "created": int(time.time()),
                        "model": request.get("model", "fake-http-skill-tool-model"),
                        "choices": [{"index": 0, "message": {"role": "assistant", "content": content}, "finish_reason": "stop"}],
                        "usage": {"prompt_tokens": 18, "completion_tokens": 9, "total_tokens": 27},
                    })
                    return
                tool_name = parent.user_tool_name(request.get("tools", []))
                parent.tool_call_name = tool_name
                parent.write_json(self, {
                    "id": "chatcmpl-http-skill-tool",
                    "object": "chat.completion",
                    "created": int(time.time()),
                    "model": request.get("model", "fake-http-skill-tool-model"),
                    "choices": [{
                        "index": 0,
                        "message": {
                            "role": "assistant",
                            "content": "",
                            "tool_calls": [{
                                "id": "call_http_skill_smoke",
                                "type": "function",
                                "function": {"name": tool_name, "arguments": json.dumps({"query": "niceagent-smoke"})},
                            }],
                        },
                        "finish_reason": "tool_calls",
                    }],
                    "usage": {"prompt_tokens": 11, "completion_tokens": 4, "total_tokens": 15},
                })

            def log_message(self, _format: str, *_args: object) -> None:
                return

        self.server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def user_tool_name(self, tools: list[dict[str, Any]]) -> str:
        for item in tools:
            name = str(item.get("function", {}).get("name", ""))
            if name and name not in {"cli_exec", "workspace_read"}:
                return name
        raise RuntimeError(f"fake model did not receive a user HTTP Skill tool: {tools!r}")

    def write_json(self, handler: BaseHTTPRequestHandler, payload: dict[str, Any]) -> None:
        body = json.dumps(payload).encode("utf-8")
        handler.send_response(200)
        handler.send_header("Content-Type", "application/json")
        handler.send_header("Content-Length", str(len(body)))
        handler.end_headers()
        handler.wfile.write(body)


def generate_localhost_cert(root: Path) -> tuple[Path, Path]:
    cert_path = root / "skill-backend.crt"
    key_path = root / "skill-backend.key"
    config_path = root / "openssl.cnf"
    config_path.write_text(
        """
[req]
distinguished_name=req_distinguished_name
x509_extensions=v3_req
prompt=no

[req_distinguished_name]
CN=127.0.0.1

[v3_req]
subjectAltName=@alt_names

[alt_names]
IP.1=127.0.0.1
DNS.1=localhost
""".strip()
        + "\n",
        encoding="utf-8",
    )
    subprocess.run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-keyout",
            str(key_path),
            "-out",
            str(cert_path),
            "-days",
            "1",
            "-config",
            str(config_path),
        ],
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    return cert_path, key_path


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


def wait_assistant_message(control_url: str, chat_id: str, run_id: str, timeout: float, token: str) -> str:
    deadline = time.time() + timeout
    while time.time() < deadline:
        run = get_json(f"{control_url}/api/runs/{run_id}", token)
        chat = get_json(f"{control_url}/api/chats/{chat_id}", token)
        for message in chat.get("messages", []):
            if message.get("role") == "assistant" and message.get("run_id") == run_id:
                return str(message.get("content", ""))
        if run.get("status") in {"failed", "canceled"}:
            raise RuntimeError(f"run ended with status {run.get('status')}: {run}")
        time.sleep(0.4)
    raise RuntimeError(f"timed out waiting for assistant message for run {run_id}")


def read_sse_events_until_type(url: str, token: str, event_type: str, timeout: float) -> list[dict[str, Any]]:
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
                    if payload.get("type") == event_type:
                        return events
                event_name = ""
                data_lines = []
                continue
            if text.startswith("event:"):
                event_name = text.removeprefix("event:").strip()
            elif text.startswith("data:"):
                data_lines.append(text.removeprefix("data:").strip())
    raise RuntimeError(f"timed out reading SSE event {event_type} from {url}; got {events}")


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
