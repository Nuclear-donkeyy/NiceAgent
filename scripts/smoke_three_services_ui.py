#!/usr/bin/env python3
"""Start NiceAgent services plus the React UI and verify a real browser flow."""

from __future__ import annotations

import argparse
import contextlib
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import textwrap
import time
import urllib.request
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--timeout", type=float, default=60)
    parser.add_argument("--keep-screenshot", action="store_true")
    args = parser.parse_args()

    repo = Path(__file__).resolve().parents[1]
    frontend = repo / "frontend"
    if not (frontend / "node_modules").exists():
        raise SystemExit("frontend/node_modules 不存在，请先运行 cd frontend && npm install")

    go_bin = os.environ.get("GO_BIN", "/usr/local/go/bin/go")
    if not Path(go_bin).exists():
        go_bin = "go"

    token = "niceagent-ui-smoke-token"
    control_port = free_port()
    runtime_port = free_port()
    sandbox_port = free_port()
    work_root = Path(tempfile.mkdtemp(prefix="niceagent-ui-smoke-"))
    log_root = work_root / "logs"
    log_root.mkdir(parents=True, exist_ok=True)
    screenshot_path = work_root / "ui-smoke.png"
    processes: list[subprocess.Popen[bytes]] = []

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
        run_frontend_build(frontend, args.timeout)

        sandbox_env = base_env()
        sandbox_env.update({"SANDBOX_EXECUTOR_ADDR": f":{sandbox_port}", "EXECUTOR_MODE": "local"})
        processes.append(
            start_service(repo / "services/sandbox-executor", [go_bin, "run", "./cmd"], sandbox_env, log_root / "sandbox.log")
        )
        wait_health(f"http://127.0.0.1:{sandbox_port}/healthz", args.timeout)

        control_url = f"http://127.0.0.1:{control_port}"
        runtime_env = base_env()
        runtime_env.update(
            {
                "AGENT_RUNTIME_ADDR": f":{runtime_port}",
                "AGENT_RUNTIME_ID": "ui-smoke-runtime",
                "CONTROL_PLANE_URL": control_url,
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

        control_env = base_env()
        control_env.update(
            {
                "CONTROL_PLANE_ADDR": f":{control_port}",
                "AUTH_MODE": "demo",
                "STORE_DRIVER": "memory",
                "DISPATCH_MODE": "http",
                "AGENT_RUNTIME_URL": f"http://127.0.0.1:{runtime_port}",
                "CONTROL_PLANE_PUBLIC_URL": control_url,
                "WEB_DIST_DIR": str(repo / "frontend/dist"),
            }
        )
        processes.append(
            start_service(repo / "services/control-plane", [go_bin, "run", "./cmd"], control_env, log_root / "control.log")
        )
        wait_health(f"{control_url}/healthz", args.timeout)

        wait_http_text(control_url, args.timeout)

        result = run_playwright(frontend, control_url, screenshot_path, args.timeout)
        result["screenshot"] = str(screenshot_path)
        print(json.dumps({"ok": True, **result}, ensure_ascii=False))
        if args.keep_screenshot:
            print(f"screenshot: {screenshot_path}", file=sys.stderr)
        return 0
    except Exception as exc:
        print(f"ui smoke failed: {exc}", file=sys.stderr)
        print(f"logs: {log_root}", file=sys.stderr)
        for path in sorted(log_root.glob("*.log")):
            print(f"\n--- {path.name} ---", file=sys.stderr)
            with contextlib.suppress(OSError):
                print(path.read_text(errors="replace")[-4000:], file=sys.stderr)
        return 1
    finally:
        for process in reversed(processes):
            stop_process(process)
        if not args.keep_screenshot:
            shutil.rmtree(work_root, ignore_errors=True)


def run_playwright(frontend: Path, base_url: str, screenshot_path: Path, timeout: float) -> dict[str, object]:
    script = textwrap.dedent(
        f"""
        import {{ chromium }} from "playwright";

        const timeout = {int(timeout * 1000)};
        const browser = await chromium.launch({{ headless: true }});
        const page = await browser.newPage({{ viewport: {{ width: 1440, height: 1000 }} }});
        const apiRequests = [];
        const consoleMessages = [];
        page.on("request", (request) => {{
          const url = request.url();
          if (url.includes("/api/")) apiRequests.push(`${{request.method()}} ${{url}}`);
        }});
        page.on("console", (message) => {{
          if (message.type() === "error" || message.type() === "warning") {{
            consoleMessages.push(`${{message.type()}}: ${{message.text()}}`);
          }}
        }});
        page.on("pageerror", (error) => consoleMessages.push(`pageerror: ${{error.message}}`));
        await page.goto({json.dumps(base_url)}, {{ waitUntil: "networkidle", timeout }});
        await page.getByPlaceholder("发送消息给远端 agent").fill("/cli echo hello-ui-smoke");
        await page.getByRole("button", {{ name: "发送" }}).click();
        await page.getByText("hello-ui-smoke").waitFor({{ timeout }});
        try {{
          await page.waitForFunction(
            () => document.body.innerText.includes("我通过系统 CLI 获取到结果"),
            null,
            {{ timeout }},
          );
          await page.locator("header").getByText("已完成").waitFor({{ timeout }});
        }} catch (error) {{
          throw new Error(`${{error.message}}\\nAPI requests:\\n${{apiRequests.join("\\n")}}\\nConsole:\\n${{consoleMessages.join("\\n")}}`);
        }}

        const requestText = apiRequests.join("\\n");
        const runMatch = requestText.match(/run_[a-zA-Z0-9_]+/);
        if (!runMatch) {{
          throw new Error("run id was not visible in the UI");
        }}
        const runID = runMatch[0];
        const artifacts = await page.request.get(`${{new URL(`/api/runs/${{runID}}/artifacts`, {json.dumps(base_url)}).toString()}}`);
        if (!artifacts.ok()) {{
          throw new Error(`artifact list returned ${{artifacts.status()}}`);
        }}
        const artifactBody = await artifacts.json();
        if (!Array.isArray(artifactBody.artifacts)) {{
          throw new Error("artifact list response did not contain artifacts array");
        }}

        await page.reload({{ waitUntil: "networkidle", timeout }});
        await page.waitForFunction(
          () => document.body.innerText.includes("hello-ui-smoke")
            && document.body.innerText.includes("我通过系统 CLI 获取到结果"),
          null,
          {{ timeout }},
        );
        await page.screenshot({{ path: {json.dumps(str(screenshot_path))}, fullPage: true }});
        const finalText = await page.locator("body").innerText();
        await browser.close();
        console.log(JSON.stringify({{
          run_id: runID,
          artifact_count: artifactBody.artifacts.length,
          recovered_after_reload: finalText.includes("hello-ui-smoke"),
        }}));
        """
    )
    env = os.environ.copy()
    env["NO_PROXY"] = merge_no_proxy(env.get("NO_PROXY", ""))
    env["no_proxy"] = merge_no_proxy(env.get("no_proxy", ""))
    completed = subprocess.run(
        ["node", "--input-type=module", "-e", script],
        cwd=str(frontend),
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout + 20,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"Playwright failed:\nSTDOUT:\n{completed.stdout}\nSTDERR:\n{completed.stderr}")
    return json.loads(completed.stdout.strip().splitlines()[-1])


def start_service(cwd: Path, cmd: list[str], env: dict[str, str], log_path: Path) -> subprocess.Popen[bytes]:
    log_file = log_path.open("wb")
    return subprocess.Popen(cmd, cwd=str(cwd), env=env, stdout=log_file, stderr=subprocess.STDOUT)


def run_frontend_build(frontend: Path, timeout: float) -> None:
    completed = subprocess.run(
        ["npm", "run", "build"],
        cwd=str(frontend),
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=max(timeout, 30),
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"frontend build failed:\n{completed.stdout}")


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


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
        except Exception as exc:  # noqa: BLE001
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"health check timed out for {url}: {last_error}")


def wait_http_text(url: str, timeout: float) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.5) as response:
                body = response.read(512).decode("utf-8", errors="replace")
                if response.status == 200 and "NiceAgent" in body:
                    return
        except Exception as exc:  # noqa: BLE001
            last_error = exc
        time.sleep(0.25)
    raise RuntimeError(f"web check timed out for {url}: {last_error}")


def merge_no_proxy(current: str) -> str:
    items = [item.strip() for item in current.split(",") if item.strip()]
    for value in ("127.0.0.1", "localhost", "::1"):
        if value not in items:
            items.append(value)
    return ",".join(items)


if __name__ == "__main__":
    raise SystemExit(main())
