#!/usr/bin/env python3
"""Start three NiceAgent services and verify a chat run end to end."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dispatch-mode", choices=["http", "redis"], default="http")
    parser.add_argument("--redis-addr", default=os.environ.get("REDIS_ADDR", "127.0.0.1:6379"))
    parser.add_argument("--start-redis", action="store_true", help="start a temporary Redis container for redis dispatch smoke")
    parser.add_argument("--runtime-count", type=int, default=1)
    parser.add_argument("--run-count", type=int, default=0)
    parser.add_argument("--timeout", type=float, default=45)
    args = parser.parse_args()
    if args.runtime_count < 1:
        raise SystemExit("--runtime-count must be >= 1")
    if args.dispatch_mode != "redis" and args.runtime_count != 1:
        raise SystemExit("--runtime-count > 1 is only supported with --dispatch-mode redis")
    run_count = args.run_count
    if run_count <= 0:
        run_count = max(1, args.runtime_count)

    repo = Path(__file__).resolve().parents[1]
    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-smoke-token"
    control_port = free_port()
    runtime_port = free_port()
    sandbox_port = free_port()
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    processes: list[subprocess.Popen[bytes]] = []
    stream_name = f"niceagent:smoke:runs:{os.getpid()}:{int(time.time() * 1000)}"
    group_name = f"niceagent-smoke-runtimes-{os.getpid()}"
    dlq_name = stream_name + ":dlq"

    def base_env() -> dict[str, str]:
        env = os.environ.copy()
        env["PATH"] = "/usr/local/go/bin:" + env.get("PATH", "")
        env["GOCACHE"] = env.get("GOCACHE", "/private/tmp/niceagent-go-cache")
        env["NICEAGENT_ENV"] = "local"
        env["INTERNAL_API_TOKEN"] = token
        env["INTERNAL_API_TOKEN_REQUIRED"] = "true"
        env["SANDBOX_WORKSPACE_ROOT"] = str(work_root / "workspaces")
        return env

    try:
        if args.start_redis:
            redis_port = free_port()
            args.redis_addr = f"127.0.0.1:{redis_port}"
            processes.append(
                start_service(
                    repo,
                    [
                        "docker",
                        "run",
                        "--rm",
                        "-p",
                        f"127.0.0.1:{redis_port}:6379",
                        "redis:7-alpine",
                    ],
                    os.environ.copy(),
                    log_root / "redis.log",
                )
            )
            wait_tcp("127.0.0.1", redis_port, args.timeout)

        sandbox_env = base_env()
        sandbox_env.update(
            {
                "SANDBOX_EXECUTOR_ADDR": f":{sandbox_port}",
                "EXECUTOR_MODE": "local",
            }
        )
        processes.append(
            start_service(repo / "services/sandbox-executor", [go_bin, "run", "./cmd"], sandbox_env, log_root / "sandbox.log")
        )
        wait_health(f"http://127.0.0.1:{sandbox_port}/healthz", args.timeout)

        control_url = f"http://127.0.0.1:{control_port}"
        runtime_ports: list[int] = []
        for index in range(args.runtime_count):
            current_runtime_port = runtime_port if index == 0 else free_port()
            runtime_ports.append(current_runtime_port)
            runtime_id = f"smoke-runtime-{index + 1}"
            runtime_env = base_env()
            runtime_env.update(
                {
                    "AGENT_RUNTIME_ADDR": f":{current_runtime_port}",
                    "AGENT_RUNTIME_ID": runtime_id,
                    "CONTROL_PLANE_URL": control_url,
                    "SANDBOX_EXECUTOR_URL": f"http://127.0.0.1:{sandbox_port}",
                    "MODEL_PROVIDER": "mock",
                    "MODEL_TIMEOUT_SECONDS": "30",
                    "RUNTIME_QUEUE_MODE": "redis" if args.dispatch_mode == "redis" else "disabled",
                    "REDIS_ADDR": args.redis_addr,
                    "RUN_QUEUE_STREAM": stream_name,
                    "RUN_QUEUE_GROUP": group_name,
                    "RUN_QUEUE_CONSUMER": runtime_id,
                    "RUN_QUEUE_RECLAIM_MIN_IDLE_SECONDS": "2",
                    "RUN_QUEUE_MAX_DELIVERIES": "3",
                    "RUN_QUEUE_DLQ_STREAM": dlq_name,
                    "RUN_ATTEMPT_LEASE_SECONDS": "30",
                    "RUN_ATTEMPT_HEARTBEAT_SECONDS": "2",
                }
            )
            processes.append(
                start_service(
                    repo / "services/agent-runtime",
                    [go_bin, "run", "./cmd"],
                    runtime_env,
                    log_root / f"runtime-{index + 1}.log",
                )
            )
            wait_health(f"http://127.0.0.1:{current_runtime_port}/healthz", args.timeout)

        control_env = base_env()
        control_env.update(
            {
                "CONTROL_PLANE_ADDR": f":{control_port}",
                "AUTH_MODE": "demo",
                "STORE_DRIVER": "memory",
                "DISPATCH_MODE": args.dispatch_mode,
                "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_ports[0]}",
                "CONTROL_PLANE_PUBLIC_URL": control_url,
                "REDIS_ADDR": args.redis_addr,
                "RUN_QUEUE_STREAM": stream_name,
                "RUN_QUEUE_GROUP": group_name,
                "EVENT_FANOUT_MODE": "local",
                "WEB_DIST_DIR": str(repo / "frontend/dist"),
            }
        )
        processes.append(
            start_service(repo / "services/control-plane", [go_bin, "run", "./cmd"], control_env, log_root / "control.log")
        )
        wait_health(f"{control_url}/healthz", args.timeout)

        results = []
        for index in range(run_count):
            chat = post_json(f"{control_url}/api/chats", {"title": f"Smoke {index + 1}"}, token)
            chat_id = str(chat["id"])
            response = post_json(f"{control_url}/api/chats/{chat_id}/messages", {"content": f"/cli echo hello-{index + 1}"}, token)
            run_id = str(response["run"]["id"])
            assistant = wait_assistant_message(control_url, chat_id, run_id, args.timeout, token)
            if f"hello-{index + 1}" not in assistant.lower():
                raise RuntimeError(f"assistant message did not include CLI output: {assistant!r}")
            run = get_json(f"{control_url}/api/runs/{run_id}", token)
            results.append(
                {
                    "chat_id": chat_id,
                    "run_id": run_id,
                    "claimed_by": run.get("claimed_by", ""),
                    "assistant": assistant,
                }
            )
        validate_redis_claims(args.dispatch_mode, args.runtime_count, results)
        print(
            json.dumps(
                {
                    "ok": True,
                    "dispatch_mode": args.dispatch_mode,
                    "runtime_count": args.runtime_count,
                    "run_count": run_count,
                    "redis_addr": args.redis_addr if args.dispatch_mode == "redis" else "",
                    "stream": stream_name if args.dispatch_mode == "redis" else "",
                    "results": results,
                },
                ensure_ascii=False,
            )
        )
        return 0
    except Exception as exc:
        print(f"smoke failed: {exc}", file=sys.stderr)
        print(f"logs: {log_root}", file=sys.stderr)
        for path in sorted(log_root.glob("*.log")):
            print(f"\n--- {path.name} ---", file=sys.stderr)
            with contextlib.suppress(OSError):
                print(path.read_text(errors="replace")[-4000:], file=sys.stderr)
        return 1
    finally:
        for process in reversed(processes):
            stop_process(process)


def validate_redis_claims(dispatch_mode: str, runtime_count: int, results: list[dict[str, object]]) -> None:
    if dispatch_mode != "redis":
        return
    expected = {f"smoke-runtime-{index + 1}" for index in range(runtime_count)}
    claimed = {str(result.get("claimed_by", "")) for result in results}
    if "" in claimed:
        raise RuntimeError(f"redis smoke expected every run to be claimed by a runtime: {results}")
    unexpected = claimed - expected
    if unexpected:
        raise RuntimeError(f"redis smoke got unexpected runtime claims {sorted(unexpected)}; expected {sorted(expected)}")
    if runtime_count > 1 and len(results) >= runtime_count and len(claimed) < 2:
        raise RuntimeError(f"redis smoke expected multiple runtime consumers to claim work: {results}")


def start_service(cwd: Path, cmd: list[str], env: dict[str, str], log_path: Path) -> subprocess.Popen[bytes]:
    log_file = log_path.open("wb")
    process = subprocess.Popen(cmd, cwd=str(cwd), env=env, stdout=log_file, stderr=subprocess.STDOUT)
    return process


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


def wait_tcp(host: str, port: int, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with socket.create_connection((host, port), timeout=1):
                return
        except Exception as exc:  # noqa: BLE001 - smoke script prints the last concrete failure.
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"tcp check timed out for {host}:{port}: {last_error}")


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


if __name__ == "__main__":
    raise SystemExit(main())
