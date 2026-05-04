#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import socket
import subprocess
import sys
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Callable


ROOT = Path(__file__).resolve().parents[2]
OPENAI_SRC = ROOT / "adapters" / "openai-agents-python" / "src"
ANTHROPIC_SRC = ROOT / "adapters" / "anthropic-sandbox-python" / "src"
GEMINI_BRIDGE = ROOT / "adapters" / "google-bridge" / "computer_use.py"
VERTEX_BRIDGE = ROOT / "adapters" / "google-bridge" / "vertex-ai" / "computer_use.py"


@dataclass
class SmokeContext:
    vm: str
    cove: str
    socket_path: Path
    token: str


@dataclass
class Adapter:
    name: str
    screenshot: Callable[[SmokeContext], bytes]


class ControlClient:
    def __init__(self, socket_path: Path, token: str, timeout: float = 30.0) -> None:
        self.socket_path = socket_path
        self.token = token
        self.timeout = timeout

    def request(self, body: dict, timeout: float | None = None) -> dict:
        req = dict(body)
        if self.token and "auth_token" not in req:
            req["auth_token"] = self.token
        payload = (json.dumps(req, separators=(",", ":")) + "\n").encode()
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(timeout or self.timeout)
            sock.connect(str(self.socket_path))
            sock.sendall(payload)
            line = _read_line(sock)
        resp = json.loads(line)
        if not resp.get("success"):
            raise RuntimeError(str(resp.get("error") or "control request failed"))
        return resp

    def exec(self, command: str, timeout: float = 60.0) -> dict:
        return self.request(
            {
                "type": "agent-exec",
                "agent_exec": {
                    "args": ["/bin/zsh", "-lc", command],
                    "working_dir": "",
                },
            },
            timeout=timeout,
        )

    def stop(self) -> None:
        self.request({"type": "agent-shutdown", "agent_shutdown": {}}, timeout=30)


def wait_ready(control: ControlClient, proc: subprocess.Popen[bytes], timeout: float) -> None:
    deadline = time.monotonic() + timeout
    last: Exception | None = None
    while time.monotonic() < deadline:
        code = proc.poll()
        if code is not None:
            raise RuntimeError(f"vm run exited before guest agent was ready: exit {code}")
        try:
            control.request({"type": "agent-ping"}, timeout=10)
            return
        except Exception as exc:  # noqa: BLE001
            last = exc
            time.sleep(1)
    raise RuntimeError(f"timed out waiting for guest agent: {last}")


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run the same browser screenshot smoke against a cove computer-use adapter."
    )
    parser.add_argument("--adapter", required=True, choices=sorted(adapters()))
    parser.add_argument("--parent", default=os.environ.get("COVE_PARITY_PARENT"), help="parent VM to fork")
    parser.add_argument("--name", help="fork VM name")
    parser.add_argument("--cove", default=os.environ.get("COVE_BIN", str(ROOT / "cove")))
    parser.add_argument("--url", default="https://example.com")
    parser.add_argument("--results-dir", default=str(Path(__file__).with_name("results")))
    parser.add_argument("--ready-timeout", type=float, default=180.0)
    parser.add_argument("--browser-wait", type=float, default=5.0)
    parser.add_argument("--keep-vm", action="store_true", help="leave the forked VM on disk")
    parser.add_argument("--vertex-project", default=os.environ.get("GOOGLE_CLOUD_PROJECT", "parity-smoke"))
    parser.add_argument("--vertex-region", default=os.environ.get("GOOGLE_CLOUD_REGION", "us-central1"))
    args = parser.parse_args()

    if not args.parent:
        parser.error("--parent or COVE_PARITY_PARENT is required")

    cove = str(Path(args.cove)) if Path(args.cove).exists() else args.cove
    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    child = args.name or f"parity-{args.adapter}-{stamp}"
    results_dir = Path(args.results_dir)
    results_dir.mkdir(parents=True, exist_ok=True)
    result_path = results_dir / f"{args.adapter}-{stamp}.json"

    started = datetime.now().astimezone()
    t0 = time.monotonic()
    run_proc: subprocess.Popen[bytes] | None = None
    frame_count = 0
    deleted = False
    result: dict = {
        "adapter": args.adapter,
        "parent_vm": args.parent,
        "vm_name": child,
        "url": args.url,
        "started_at": started.isoformat(),
        "result_path": str(result_path),
    }

    try:
        run([cove, "fork", args.parent, child], timeout=120)
        ctx = SmokeContext(
            vm=child,
            cove=cove,
            socket_path=vm_dir(child) / "control.sock",
            token=read_token(child),
        )
        run_proc = subprocess.Popen([cove, "-vm", child, "run", "-gui"])
        control = ControlClient(ctx.socket_path, ctx.token)
        wait_ready(control, run_proc, args.ready_timeout)

        adapter = adapters(args.vertex_project, args.vertex_region)[args.adapter]
        first = wait_for_screenshot(adapter, ctx, args.ready_timeout)
        frame_count += 1
        require_png(first)

        control.exec(f"open -a Safari {shell_quote(args.url)}", timeout=60)
        time.sleep(max(0.0, args.browser_wait))

        shot = adapter.screenshot(ctx)
        frame_count += 1
        width, height = png_size(shot)
        result.update(
            {
                "status": "ok",
                "frame_count": frame_count,
                "screenshot_bytes": len(shot),
                "screenshot_sha256": hashlib.sha256(shot).hexdigest(),
                "screenshot_width": width,
                "screenshot_height": height,
            }
        )
    except Exception as exc:  # noqa: BLE001
        result.update({"status": "error", "error": str(exc), "frame_count": frame_count})
        raise
    finally:
        result["ended_at"] = datetime.now().astimezone().isoformat()
        result["wall_clock_seconds"] = round(time.monotonic() - t0, 3)
        if run_proc is not None:
            try:
                ControlClient(vm_dir(child) / "control.sock", read_token(child)).stop()
            except Exception as exc:  # noqa: BLE001
                result["stop_error"] = str(exc)
            try:
                run_proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                run_proc.terminate()
        if not args.keep_vm:
            try:
                delete_vm(cove, child)
                deleted = True
            except Exception as exc:  # noqa: BLE001
                result["delete_error"] = str(exc)
        result["vm_deleted"] = deleted
        result_path.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
        print(result_path)

    return 0


def adapters(vertex_project: str = "parity-smoke", vertex_region: str = "us-central1") -> dict[str, Adapter]:
    return {
        "openai": Adapter("openai", openai_screenshot),
        "anthropic": Adapter("anthropic", anthropic_screenshot),
        "gemini": Adapter("gemini", gemini_screenshot),
        "vertex": Adapter("vertex", lambda ctx: vertex_screenshot(ctx, vertex_project, vertex_region)),
    }


def openai_screenshot(ctx: SmokeContext) -> bytes:
    if str(OPENAI_SRC) not in sys.path:
        sys.path.insert(0, str(OPENAI_SRC))
    from cove_sandbox import CoveSandbox

    sandbox = CoveSandbox(vm=ctx.vm, cove=ctx.cove, token=ctx.token)
    encoded = sandbox.computer().screenshot()
    import base64

    return base64.b64decode(encoded)


def anthropic_screenshot(ctx: SmokeContext) -> bytes:
    if str(ANTHROPIC_SRC) not in sys.path:
        sys.path.insert(0, str(ANTHROPIC_SRC))
    from cove_claude_sandbox.actions import AnthropicToolDispatcher
    from cove_claude_sandbox.client import CoveClient

    client = CoveClient(vm=ctx.vm, cove=ctx.cove, token=ctx.token)
    dispatcher = AnthropicToolDispatcher(client)
    result = dispatcher._computer({"action": "screenshot"})
    if not isinstance(result, list) or not result:
        raise RuntimeError("anthropic screenshot returned no image content")
    source = result[0].get("source", {})
    data = source.get("data")
    if not isinstance(data, str):
        raise RuntimeError("anthropic screenshot returned no base64 data")
    import base64

    return base64.b64decode(data)


def gemini_screenshot(ctx: SmokeContext) -> bytes:
    module = load_module("cove_gemini_bridge", GEMINI_BRIDGE)
    bridge = module.CoveGeminiBridge(ctx.vm, control_token=ctx.token, socket_path=str(ctx.socket_path))
    return bridge.screenshot()


def vertex_screenshot(ctx: SmokeContext, project: str, region: str) -> bytes:
    module = load_module("cove_vertex_bridge", VERTEX_BRIDGE)
    bridge = module.CoveVertexBridge(
        ctx.vm,
        project=project,
        region=region,
        control_token=ctx.token,
        socket_path=str(ctx.socket_path),
    )
    return bridge.screenshot()


def wait_for_screenshot(adapter: Adapter, ctx: SmokeContext, timeout: float) -> bytes:
    deadline = time.monotonic() + timeout
    last: Exception | None = None
    while time.monotonic() < deadline:
        try:
            shot = adapter.screenshot(ctx)
            require_png(shot)
            return shot
        except Exception as exc:  # noqa: BLE001
            last = exc
            time.sleep(1)
    raise RuntimeError(f"timed out waiting for screenshot: {last}")


def run(args: list[str], timeout: float) -> str:
    proc = subprocess.run(args, text=True, input="", stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if proc.returncode != 0:
        raise RuntimeError(f"{' '.join(args)}: {proc.stderr.strip()}")
    return proc.stdout


def delete_vm(cove: str, name: str) -> None:
    proc = subprocess.run(
        [cove, "vm", "delete", name],
        input="y\n",
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=120,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip())


def read_token(vm: str) -> str:
    path = vm_dir(vm) / "control.token"
    try:
        return path.read_text().strip()
    except FileNotFoundError:
        return ""


def vm_dir(vm: str) -> Path:
    return Path.home() / ".vz" / "vms" / vm


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def require_png(data: bytes) -> None:
    png_size(data)


def png_size(data: bytes) -> tuple[int, int]:
    if len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n":
        raise RuntimeError("screenshot is not a valid PNG")
    return (
        int.from_bytes(data[16:20], "big"),
        int.from_bytes(data[20:24], "big"),
    )


def _read_line(sock: socket.socket) -> str:
    chunks: list[bytes] = []
    while True:
        chunk = sock.recv(65536)
        if not chunk:
            break
        chunks.append(chunk)
        if b"\n" in chunk:
            break
    if not chunks:
        raise RuntimeError("control socket returned no response")
    return b"".join(chunks).split(b"\n", 1)[0].decode()


def shell_quote(s: str) -> str:
    return "'" + s.replace("'", "'\\''") + "'"


if __name__ == "__main__":
    raise SystemExit(main())
