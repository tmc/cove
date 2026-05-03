"""Anthropic computer-use bridge for cove.

Drives a running cove macOS VM as a Claude computer-use target by
translating Anthropic Messages API tool_use blocks into cove
control-socket commands. One-file, dependency-light: stdlib + httpx
or requests. No anthropic SDK dependency.

This is a substrate-only example. The full SDK adapter
(`AnthropicSandbox`, `SandboxRunConfig`-style backend) is v0.4
design 022 scope.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import socket
import sys
import time
from pathlib import Path
from typing import Any

try:
    import httpx as _http
    _HTTP = "httpx"
except ImportError:
    try:
        import requests as _http  # type: ignore[no-redef]
        _HTTP = "requests"
    except ImportError:
        _http = None
        _HTTP = ""

API_URL = "https://api.anthropic.com/v1/messages"
BETA_HEADER = "computer-use-2025-11-24"
DEFAULT_MODEL = "claude-sonnet-4-6"
DEFAULT_SCREEN_SIZE = (1920, 1080)


class CoveComputerUse:
    """Thin sync client for cove's per-VM control socket."""

    def __init__(self, vm_name: str, control_token: str | None = None,
                 timeout: float = 30.0) -> None:
        self.vm_name = vm_name
        self.socket_path = Path.home() / ".vz" / "vms" / vm_name / "control.sock"
        self.token = control_token if control_token is not None else self._load_token()
        self.timeout = timeout
        self._last_screen_size: tuple[int, int] | None = None

    def _load_token(self) -> str:
        env = os.environ.get("VZ_MACOS_CTL_TOKEN")
        if env:
            return env.strip()
        path = self.socket_path.with_name("control.token")
        try:
            return path.read_text().strip()
        except FileNotFoundError:
            return ""

    def _control(self, request: dict[str, Any], timeout: float | None = None) -> dict[str, Any]:
        req = dict(request)
        if self.token and "auth_token" not in req:
            req["auth_token"] = self.token
        data = (json.dumps(req, separators=(",", ":")) + "\n").encode()
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(timeout or self.timeout)
            sock.connect(str(self.socket_path))
            sock.sendall(data)
            chunks: list[bytes] = []
            while True:
                chunk = sock.recv(65536)
                if not chunk:
                    break
                chunks.append(chunk)
                if b"\n" in chunk:
                    break
        if not chunks:
            raise RuntimeError(f"empty response from {self.socket_path}")
        line = b"".join(chunks).split(b"\n", 1)[0]
        resp = json.loads(line.decode())
        if not resp.get("success"):
            raise RuntimeError(str(resp.get("error") or "control request failed"))
        return resp

    def screenshot(self, scale: float = 1.0, fmt: str = "png") -> bytes:
        resp = self._control({
            "type": "screenshot",
            "screenshot": {"scale": scale, "quality": 90, "format": fmt},
        }, timeout=max(self.timeout, 30))
        result = resp.get("screenshot_result") or {}
        if "width" in result and "height" in result:
            self._last_screen_size = (int(result["width"]), int(result["height"]))
        data = result.get("image_data") or resp.get("data")
        if not isinstance(data, str):
            raise RuntimeError("screenshot returned no image data")
        return base64.b64decode(data)

    def click(self, x: int, y: int, button: str = "left") -> None:
        btn = {"left": 0, "right": 1, "middle": 2}.get(button, 0)
        self._control({
            "type": "mouse",
            "mouse": {"x": x, "y": y, "button": btn, "action": "click", "absolute": True},
        })

    def type_text(self, text: str) -> None:
        self._control(
            {"type": "text", "text": {"text": text}},
            timeout=max(self.timeout, 10 + len(text) / 10),
        )

    def key(self, keycode: int, modifiers: list[str] | None = None) -> None:
        # macOS modifier bitfield (NSEventModifierFlags-shaped):
        # shift=0x20000, ctrl=0x40000, option=0x80000, cmd=0x100000
        mod_bits = {"shift": 0x20000, "ctrl": 0x40000, "option": 0x80000,
                    "alt": 0x80000, "cmd": 0x100000, "command": 0x100000}
        flags = 0
        for m in modifiers or []:
            flags |= mod_bits.get(m.lower(), 0)
        for down in (True, False):
            self._control({
                "type": "key",
                "key": {"key_code": keycode, "key_down": down,
                        "modifiers": flags, "use_cg_event": flags != 0},
            })
            if down:
                time.sleep(0.05)

    def screen_size(self) -> tuple[int, int]:
        if self._last_screen_size is None:
            # Trigger one to populate width/height from the response.
            self.screenshot()
        return self._last_screen_size or DEFAULT_SCREEN_SIZE


def _post_messages(api_key: str, payload: dict[str, Any]) -> dict[str, Any]:
    if _http is None:
        raise SystemExit("install httpx or requests: pip install httpx")
    headers = {
        "x-api-key": api_key,
        "anthropic-version": "2023-06-01",
        "anthropic-beta": BETA_HEADER,
        "content-type": "application/json",
    }
    if _HTTP == "httpx":
        r = _http.post(API_URL, headers=headers, json=payload, timeout=120.0)
        r.raise_for_status()
        return r.json()
    r = _http.post(API_URL, headers=headers, json=payload, timeout=120)
    r.raise_for_status()
    return r.json()


def _execute_tool(cove: CoveComputerUse, action: str, params: dict[str, Any]) -> dict[str, Any]:
    """Translate an Anthropic computer-use tool_use input into a cove call.
    Returns the tool_result content (image for screenshot, text otherwise)."""
    if action == "screenshot":
        png = cove.screenshot()
        return {"type": "image", "source": {
            "type": "base64", "media_type": "image/png",
            "data": base64.b64encode(png).decode("ascii"),
        }}
    if action in ("left_click", "right_click"):
        x, y = params.get("coordinate", [0, 0])
        cove.click(int(x), int(y), "right" if action == "right_click" else "left")
        return {"type": "text", "text": "ok"}
    if action == "type":
        cove.type_text(str(params.get("text", "")))
        return {"type": "text", "text": "ok"}
    if action == "key":
        # The Anthropic API uses xdotool-style key names ("Return", "cmd+c").
        # Mapping is intentionally minimal here -- real adapters need a table.
        text = str(params.get("text", ""))
        parts = text.split("+")
        mods, name = parts[:-1], parts[-1]
        keymap = {"Return": 36, "Escape": 53, "Tab": 48, "space": 49,
                  "BackSpace": 51, "Left": 123, "Right": 124, "Up": 126, "Down": 125,
                  "c": 8, "v": 9, "a": 0}
        kc = keymap.get(name)
        if kc is None:
            return {"type": "text", "text": f"unknown key: {name}"}
        cove.key(kc, modifiers=mods)
        return {"type": "text", "text": "ok"}
    if action == "cursor_position":
        # Not exposed by cove's control socket today.
        return {"type": "text", "text": "0,0"}
    return {"type": "text", "text": f"unsupported action: {action}"}


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--vm", required=True, help="VM name (looks up ~/.vz/vms/<vm>/control.sock)")
    p.add_argument("--task", required=True, help="prompt to drive the agent loop with")
    p.add_argument("--model", default=DEFAULT_MODEL)
    p.add_argument("--max-iters", type=int, default=5)
    args = p.parse_args()

    api_key = os.environ.get("ANTHROPIC_API_KEY", "")
    if not api_key:
        print("ANTHROPIC_API_KEY not set", file=sys.stderr)
        return 2

    cove = CoveComputerUse(vm_name=args.vm)
    width, height = cove.screen_size()
    tools = [{
        "type": "computer_20250124",
        "name": "computer",
        "display_width_px": width,
        "display_height_px": height,
        "display_number": 1,
    }]
    messages: list[dict[str, Any]] = [{"role": "user", "content": args.task}]

    for i in range(args.max_iters):
        resp = _post_messages(api_key, {
            "model": args.model, "max_tokens": 1024,
            "tools": tools, "messages": messages,
        })
        messages.append({"role": "assistant", "content": resp.get("content", [])})
        if resp.get("stop_reason") == "end_turn":
            print(f"end_turn after {i + 1} iters", file=sys.stderr)
            break
        tool_results = []
        for block in resp.get("content", []):
            if block.get("type") != "tool_use":
                continue
            inp = block.get("input", {}) or {}
            content = _execute_tool(cove, inp.get("action", ""), inp)
            tool_results.append({
                "type": "tool_result",
                "tool_use_id": block.get("id"),
                "content": [content],
            })
        if not tool_results:
            break
        messages.append({"role": "user", "content": tool_results})
    else:
        print(f"hit --max-iters={args.max_iters}", file=sys.stderr)

    for block in messages[-1].get("content", []):
        if isinstance(block, dict) and block.get("type") == "text":
            print(block.get("text", ""))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
