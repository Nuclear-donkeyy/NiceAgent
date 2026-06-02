#!/usr/bin/env python3
"""Run an opt-in Redis Streams capacity smoke and emit a JSON report."""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    parser = argparse.ArgumentParser(description="Measure a small Redis Streams enqueue/consume/ack path.")
    parser.add_argument("--redis-addr", default=os.environ.get("REDIS_ADDR", ""))
    parser.add_argument("--start-redis", action="store_true", help="start a temporary Redis container")
    parser.add_argument("--messages", type=int, default=int(os.environ.get("REDIS_CAPACITY_MESSAGES", "200")))
    parser.add_argument("--batch-size", type=int, default=int(os.environ.get("REDIS_CAPACITY_BATCH_SIZE", "50")))
    parser.add_argument("--report", default=os.environ.get("REDIS_CAPACITY_REPORT", ""))
    parser.add_argument("--require-redis", action="store_true", help="fail instead of SKIP when Redis cannot be reached")
    args = parser.parse_args()

    if args.messages < 1:
        raise SystemExit("--messages must be >= 1")
    if args.batch_size < 1:
        raise SystemExit("--batch-size must be >= 1")

    work_root = Path(tempfile.mkdtemp(prefix="niceagent-redis-capacity-"))
    redis_process: subprocess.Popen[bytes] | None = None
    redis_addr = args.redis_addr
    try:
        if args.start_redis or not redis_addr:
            if not docker_available():
                return finish_skip("Docker CLI or daemon is not available for temporary Redis", args, work_root)
            port = free_port()
            redis_addr = f"127.0.0.1:{port}"
            redis_process = subprocess.Popen(
                [
                    os.environ.get("DOCKER", "docker"),
                    "run",
                    "--rm",
                    "-p",
                    f"127.0.0.1:{port}:6379",
                    "redis:7-alpine",
                ],
                stdout=(work_root / "redis.log").open("wb"),
                stderr=subprocess.STDOUT,
            )
            wait_tcp("127.0.0.1", port, timeout=20)
            wait_redis_ping("127.0.0.1", port, timeout=20)

        host, port = parse_addr(redis_addr)
        try:
            report = run_capacity_smoke(host, port, args.messages, args.batch_size)
        except Exception as exc:  # noqa: BLE001 - smoke reports concrete failure or skip.
            if args.require_redis:
                raise
            return finish_skip(f"Redis capacity smoke skipped: {exc}", args, work_root)
        write_report(report, args.report)
        print(json.dumps(report, ensure_ascii=False, sort_keys=True))
        return 0
    except Exception as exc:  # noqa: BLE001 - smoke prints concrete failure details.
        failure = build_failure_report(str(exc), args.messages, args.batch_size)
        write_report(failure, args.report)
        print(f"redis capacity smoke failed: {exc}", file=sys.stderr)
        if (work_root / "redis.log").exists():
            print(f"\n--- redis.log ---\n{(work_root / 'redis.log').read_text(errors='replace')[-4000:]}", file=sys.stderr)
        return 1
    finally:
        if redis_process is not None:
            stop_process(redis_process)


def run_capacity_smoke(host: str, port: int, messages: int, batch_size: int) -> dict[str, Any]:
    suffix = f"{os.getpid()}:{int(time.time() * 1000)}"
    stream = f"niceagent:capacity:runs:{suffix}"
    group = f"niceagent-capacity-{os.getpid()}"
    consumer = f"capacity-consumer-{os.getpid()}"
    client = RedisClient(host, port)
    started = time.monotonic()
    try:
        if client.execute("PING") != "PONG":
            raise RuntimeError("Redis PING did not return PONG")
        with suppress_redis_error("cleanup before smoke"):
            client.execute("DEL", stream)
        client.execute("XGROUP", "CREATE", stream, group, "$", "MKSTREAM")

        enqueue_started = time.monotonic()
        for index in range(messages):
            client.execute(
                "XADD",
                stream,
                "*",
                "run_id",
                f"run-{index + 1}",
                "attempt_id",
                f"attempt-{index + 1}",
                "enqueued_at",
                str(int(time.time() * 1000)),
            )
        enqueue_duration = time.monotonic() - enqueue_started

        consumed = 0
        acked = 0
        consume_started = time.monotonic()
        while consumed < messages:
            response = client.execute(
                "XREADGROUP",
                "GROUP",
                group,
                consumer,
                "COUNT",
                str(min(batch_size, messages - consumed)),
                "BLOCK",
                "2000",
                "STREAMS",
                stream,
                ">",
            )
            entries = parse_xread_entries(response)
            if not entries:
                raise RuntimeError(f"timed out reading stream after {consumed}/{messages} messages")
            consumed += len(entries)
            acked += int(client.execute("XACK", stream, group, *entries))
        consume_duration = time.monotonic() - consume_started

        pending = parse_pending_count(client.execute("XPENDING", stream, group))
        group_info = parse_group_info(client.execute("XINFO", "GROUPS", stream), group)
        duration = time.monotonic() - started
        report = build_success_report(
            messages=messages,
            batch_size=batch_size,
            stream=stream,
            group=group,
            consumer=consumer,
            duration_seconds=duration,
            enqueue_seconds=enqueue_duration,
            consume_seconds=consume_duration,
            consumed=consumed,
            acked=acked,
            pending_count=pending,
            lag=group_info.get("lag"),
        )
        return report
    finally:
        with suppress_redis_error("cleanup after smoke"):
            client.execute("DEL", stream)
        client.close()


class RedisClient:
    def __init__(self, host: str, port: int) -> None:
        self.sock = socket.create_connection((host, port), timeout=5)
        self.file = self.sock.makefile("rb")

    def execute(self, *parts: object) -> Any:
        payload = encode_command(parts)
        self.sock.sendall(payload)
        value = read_resp(self.file)
        if isinstance(value, RedisError):
            raise RuntimeError(value.message)
        return value

    def close(self) -> None:
        with suppress_all():
            self.file.close()
        with suppress_all():
            self.sock.close()


class RedisError:
    def __init__(self, message: str) -> None:
        self.message = message


def encode_command(parts: tuple[object, ...]) -> bytes:
    encoded = [f"*{len(parts)}\r\n".encode("ascii")]
    for part in parts:
        data = str(part).encode("utf-8")
        encoded.append(f"${len(data)}\r\n".encode("ascii"))
        encoded.append(data + b"\r\n")
    return b"".join(encoded)


def read_resp(file: Any) -> Any:
    prefix = file.read(1)
    if not prefix:
        raise RuntimeError("Redis connection closed")
    line = file.readline().rstrip(b"\r\n")
    if prefix == b"+":
        return line.decode("utf-8")
    if prefix == b"-":
        return RedisError(line.decode("utf-8", errors="replace"))
    if prefix == b":":
        return int(line)
    if prefix == b"$":
        length = int(line)
        if length == -1:
            return None
        data = file.read(length)
        file.read(2)
        return data.decode("utf-8", errors="replace")
    if prefix == b"*":
        length = int(line)
        if length == -1:
            return None
        return [read_resp(file) for _ in range(length)]
    raise RuntimeError(f"unsupported Redis RESP prefix {prefix!r}")


class suppress_redis_error:
    def __init__(self, _: str) -> None:
        pass

    def __enter__(self) -> None:
        return None

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> bool:
        return exc is not None


class suppress_all:
    def __enter__(self) -> None:
        return None

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> bool:
        return exc is not None


def parse_xread_entries(response: Any) -> list[str]:
    entries: list[str] = []
    if not response:
        return entries
    for stream_entry in response:
        if not isinstance(stream_entry, list) or len(stream_entry) < 2:
            continue
        for item in stream_entry[1] or []:
            if isinstance(item, list) and item:
                entries.append(str(item[0]))
    return entries


def parse_pending_count(response: Any) -> int:
    if isinstance(response, list) and response:
        return int(response[0] or 0)
    return 0


def parse_group_info(response: Any, group: str) -> dict[str, Any]:
    if not isinstance(response, list):
        return {}
    for item in response:
        if not isinstance(item, list):
            continue
        values = dict(zip(item[0::2], item[1::2]))
        if values.get("name") == group:
            return values
    return {}


def build_success_report(
    *,
    messages: int,
    batch_size: int,
    stream: str,
    group: str,
    consumer: str,
    duration_seconds: float,
    enqueue_seconds: float,
    consume_seconds: float,
    consumed: int,
    acked: int,
    pending_count: int,
    lag: Any,
) -> dict[str, Any]:
    return {
        "kind": "redis_capacity_smoke",
        "result": "passed",
        "messages": messages,
        "batch_size": batch_size,
        "stream": stream,
        "group": group,
        "consumer": consumer,
        "duration_ms": round(duration_seconds * 1000, 2),
        "enqueue_duration_ms": round(enqueue_seconds * 1000, 2),
        "consume_duration_ms": round(consume_seconds * 1000, 2),
        "enqueue_rate_per_second": round(messages / enqueue_seconds, 2) if enqueue_seconds > 0 else messages,
        "consume_rate_per_second": round(consumed / consume_seconds, 2) if consume_seconds > 0 else consumed,
        "consumed": consumed,
        "acked": acked,
        "pending_count": pending_count,
        "lag": lag,
    }


def build_skip_report(reason: str, messages: int, batch_size: int) -> dict[str, Any]:
    return {
        "kind": "redis_capacity_smoke",
        "result": "skipped",
        "reason": reason,
        "messages": messages,
        "batch_size": batch_size,
    }


def build_failure_report(reason: str, messages: int, batch_size: int) -> dict[str, Any]:
    return {
        "kind": "redis_capacity_smoke",
        "result": "failed",
        "reason": reason,
        "messages": messages,
        "batch_size": batch_size,
    }


def finish_skip(reason: str, args: argparse.Namespace, work_root: Path) -> int:
    if args.require_redis:
        raise RuntimeError(reason)
    report = build_skip_report(reason, args.messages, args.batch_size)
    write_report(report, args.report)
    print(f"SKIP redis capacity smoke: {reason}")
    if (work_root / "redis.log").exists():
        print(f"logs: {work_root / 'redis.log'}")
    return 0


def write_report(report: dict[str, Any], path: str) -> None:
    if not path:
        return
    report_path = Path(path)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True) + "\n")


def docker_available() -> bool:
    docker_bin = os.environ.get("DOCKER", "docker")
    try:
        subprocess.run([docker_bin, "info"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=8, check=True)
    except (OSError, subprocess.SubprocessError):
        return False
    return True


def parse_addr(addr: str) -> tuple[str, int]:
    if ":" not in addr:
        raise RuntimeError(f"invalid Redis address {addr!r}, want host:port")
    host, port = addr.rsplit(":", 1)
    return host, int(port)


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
        except Exception as exc:  # noqa: BLE001 - smoke keeps last startup error.
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"tcp check timed out for {host}:{port}: {last_error}")


def wait_redis_ping(host: str, port: int, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        client: RedisClient | None = None
        try:
            client = RedisClient(host, port)
            if client.execute("PING") == "PONG":
                return
        except Exception as exc:  # noqa: BLE001 - smoke keeps last startup error.
            last_error = exc
        finally:
            if client is not None:
                client.close()
        time.sleep(0.25)
    raise RuntimeError(f"Redis PING timed out for {host}:{port}: {last_error}")


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
