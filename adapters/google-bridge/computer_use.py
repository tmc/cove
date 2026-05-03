"""Drive a running cove macOS VM as a Google Gemini computer-use target.

This is a thin Python helper. It mirrors the sibling
``adapters/anthropic-bridge`` and the more complete
``adapters/openai-agents-python`` adapter, but does NOT depend on any
Google SDK. It speaks raw HTTP to the
``generativelanguage.googleapis.com`` endpoint, and JSON-line over the
existing cove control socket.

Reference: https://ai.google.dev/gemini-api/docs/computer-use

The Gemini computer-use API is currently in preview. Wire shapes,
function names, and supported environments may shift; treat this helper
as a substrate example, not a stable client.

The only currently-defined ``Tool.computer_use.environment`` value is
``ENVIRONMENT_BROWSER``. Driving a macOS desktop guest with that
environment is best-effort: the model will reason about the screen as if
it were a browser viewport, which is sub-optimal but workable for short
one-shot runs. A future ``ENVIRONMENT_DESKTOP`` (or similar) would be a
better fit; we will switch when one ships.

Vertex AI computer-use is intentionally out of scope here. The simpler
``generativelanguage.googleapis.com`` endpoint with a single API key is
enough for the substrate-only goal.

Usage:

    export GEMINI_API_KEY=...
    python3 adapters/google-bridge/computer_use.py \\
        --vm macos-eval \\
        --task "Open Safari and read the visible page title."
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import socket
import struct
import sys
import time
from pathlib import Path
from typing import Any

try:
    import httpx  # preferred
except ImportError:  # pragma: no cover - fall back to urllib if httpx missing
    httpx = None  # type: ignore[assignment]
    import urllib.request
    import urllib.error


GEMINI_ENDPOINT = (
    "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent"
)


# Modifier bitmask used by cove's control socket "key" command.
# Matches the values already accepted by control_socket.go.
_MOD_SHIFT = 2
_MOD_CONTROL = 1
_MOD_OPTION = 4
_MOD_COMMAND = 8

_MODIFIER_MAP = {
    "shift": _MOD_SHIFT,
    "control": _MOD_CONTROL,
    "ctrl": _MOD_CONTROL,
    "alt": _MOD_OPTION,
    "option": _MOD_OPTION,
    "opt": _MOD_OPTION,
    "meta": _MOD_COMMAND,
    "cmd": _MOD_COMMAND,
    "command": _MOD_COMMAND,
    "win": _MOD_COMMAND,
    "super": _MOD_COMMAND,
}

# macOS keyCode table (subset). See HIToolbox/Events.h for the canonical list.
_KEY_CODES: dict[str, int] = {
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
    "c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
    "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22,
    "5": 23, "9": 25, "7": 26, "8": 28, "0": 29,
    "o": 31, "u": 32, "i": 34, "p": 35, "l": 37, "j": 38, "k": 40,
    "n": 45, "m": 46,
    "return": 36, "enter": 36,
    "tab": 48,
    "space": 49,
    "delete": 51, "backspace": 51,
    "escape": 53, "esc": 53,
    "left": 123, "right": 124, "down": 125, "up": 126,
}


def _resolve_socket_path(vm_name: str, override: str | None) -> str:
    if override:
        return override
    return str(Path("~/.vz/vms").expanduser() / vm_name / "control.sock")


def _resolve_token(vm_name: str, override: str | None) -> str | None:
    if override:
        return override
    token_path = Path("~/.vz/vms").expanduser() / vm_name / "control.token"
    if token_path.exists():
        return token_path.read_text().strip()
    return None


class CoveGeminiBridge:
    """Translate Gemini computer-use function_call parts into cove control
    socket commands."""

    def __init__(
        self,
        vm_name: str,
        control_token: str | None = None,
        socket_path: str | None = None,
    ) -> None:
        self._vm_name = vm_name
        self._socket_path = _resolve_socket_path(vm_name, socket_path)
        self._token = _resolve_token(vm_name, control_token)

    # --- low-level control-socket I/O -------------------------------------

    def _control(self, request: dict[str, Any], timeout: float = 30.0) -> dict[str, Any]:
        if self._token and "auth_token" not in request:
            request = dict(request, auth_token=self._token)
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        try:
            sock.connect(self._socket_path)
            sock.sendall((json.dumps(request) + "\n").encode("utf-8"))
            buf = bytearray()
            while True:
                chunk = sock.recv(65536)
                if not chunk:
                    break
                buf.extend(chunk)
                if b"\n" in chunk:
                    break
        finally:
            sock.close()
        line = bytes(buf).split(b"\n", 1)[0]
        if not line:
            raise RuntimeError("empty response from cove control socket")
        try:
            response: dict[str, Any] = json.loads(line)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"non-JSON response from control socket: {exc}") from exc
        if not response.get("success", False):
            raise RuntimeError(f"control socket error: {response.get('error', response)}")
        return response

    # --- primitives -------------------------------------------------------

    def screenshot(self, scale: float = 1.0, fmt: str = "png") -> bytes:
        request = {
            "type": "screenshot",
            "screenshot": {"scale": scale, "quality": 90, "format": fmt},
        }
        response = self._control(request)
        b64 = ""
        result = response.get("screenshot_result")
        if isinstance(result, dict):
            b64 = result.get("image_data", "") or ""
        if not b64:
            b64 = response.get("data", "") or ""
        if not b64:
            raise RuntimeError("screenshot response missing image data")
        return base64.b64decode(b64)

    def click(self, x: int, y: int, button: str = "left") -> None:
        button_id = {"left": 0, "right": 1, "middle": 2}.get(button.lower(), 0)
        self._control({
            "type": "mouse",
            "mouse": {
                "x": int(x),
                "y": int(y),
                "button": button_id,
                "action": "click",
                "absolute": True,
            },
        })

    def type_text(self, text: str) -> None:
        self._control({"type": "text", "text": {"text": text}})

    def key_combination(self, keys: str) -> None:
        """Translate a Gemini-style key string (e.g. 'Control+C', 'Meta+Tab')
        into a cove key event with modifier bitmask."""

        parts = [p.strip() for p in keys.split("+") if p.strip()]
        if not parts:
            raise RuntimeError("empty key combination")
        modifiers = 0
        target = parts[-1]
        for part in parts[:-1]:
            mod = _MODIFIER_MAP.get(part.lower())
            if mod is None:
                raise RuntimeError(f"unsupported modifier: {part!r}")
            modifiers |= mod
        key_lookup = target.lower()
        # tolerate "ArrowLeft" -> "left" etc.
        if key_lookup.startswith("arrow"):
            key_lookup = key_lookup[len("arrow"):]
        code = _KEY_CODES.get(key_lookup)
        if code is None:
            raise RuntimeError(f"unsupported key: {target!r}")
        self._send_key(code, modifiers, key_down=True)
        self._send_key(code, modifiers, key_down=False)

    def _send_key(self, code: int, modifiers: int, key_down: bool) -> None:
        self._control({
            "type": "key",
            "key": {
                "key_code": code,
                "key_down": key_down,
                "modifiers": modifiers,
                "use_cg_event": True,
            },
        })

    def scroll(self, x: int, y: int, direction: str, magnitude: int = 100) -> None:
        dx, dy = 0, 0
        d = direction.lower()
        if d == "up":
            dy = magnitude
        elif d == "down":
            dy = -magnitude
        elif d == "left":
            dx = -magnitude
        elif d == "right":
            dx = magnitude
        else:
            raise RuntimeError(f"unsupported scroll direction: {direction!r}")
        self._control({
            "type": "mouse",
            "mouse": {
                "x": int(x),
                "y": int(y),
                "action": "scroll",
                "scroll_x": dx,
                "scroll_y": dy,
                "absolute": True,
            },
        })

    def wait(self, seconds: float) -> None:
        time.sleep(max(0.0, float(seconds)))

    def screen_size(self) -> tuple[int, int]:
        png = self.screenshot()
        # PNG magic = 8 bytes; IHDR chunk header at offset 8 (4-byte length +
        # 4-byte type "IHDR"); width/height are the next two big-endian uint32.
        if len(png) < 24 or png[:8] != b"\x89PNG\r\n\x1a\n":
            raise RuntimeError("screenshot is not a valid PNG")
        width, height = struct.unpack(">II", png[16:24])
        return int(width), int(height)


# --- coordinate translation ----------------------------------------------


def normalize_to_pixels(x: int, y: int, width: int, height: int) -> tuple[int, int]:
    """Gemini emits x/y in the 0..999 normalized space. Map to pixels."""
    return (int(x * width / 1000), int(y * height / 1000))


# --- HTTP helpers --------------------------------------------------------


def _http_post_json(url: str, headers: dict[str, str], body: dict[str, Any], timeout: float = 60.0) -> dict[str, Any]:
    payload = json.dumps(body).encode("utf-8")
    if httpx is not None:
        r = httpx.post(url, headers=headers, content=payload, timeout=timeout)
        r.raise_for_status()
        return r.json()
    req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=timeout) as resp:  # type: ignore[union-attr]
        return json.loads(resp.read().decode("utf-8"))


# --- Gemini request shape ------------------------------------------------


def _initial_request(task: str, screenshot_png: bytes) -> dict[str, Any]:
    return {
        "contents": [
            {
                "role": "user",
                "parts": [
                    {"text": task},
                    {
                        "inline_data": {
                            "mime_type": "image/png",
                            "data": base64.b64encode(screenshot_png).decode("ascii"),
                        }
                    },
                ],
            }
        ],
        "tools": [{"computer_use": {"environment": "ENVIRONMENT_BROWSER"}}],
    }


def _function_response_part(name: str, screenshot_png: bytes, extra: dict[str, Any] | None = None) -> dict[str, Any]:
    response: dict[str, Any] = {
        "screenshot": {
            "inline_data": {
                "mime_type": "image/png",
                "data": base64.b64encode(screenshot_png).decode("ascii"),
            }
        }
    }
    if extra:
        response.update(extra)
    return {"function_response": {"name": name, "response": response}}


# --- function_call dispatch ----------------------------------------------


def _dispatch(bridge: CoveGeminiBridge, name: str, args: dict[str, Any], width: int, height: int) -> str:
    """Run the named function_call against the bridge. Returns a short
    log string; the caller appends a fresh screenshot regardless of
    success."""

    def _norm(ax: Any, ay: Any) -> tuple[int, int]:
        return normalize_to_pixels(int(ax), int(ay), width, height)

    if name == "click_at":
        x, y = _norm(args.get("x", 0), args.get("y", 0))
        bridge.click(x, y)
        return f"click_at({x},{y})"
    if name == "type_text_at":
        x, y = _norm(args.get("x", 0), args.get("y", 0))
        text = args.get("text", "")
        bridge.click(x, y)
        if args.get("clear_before_typing"):
            bridge.key_combination("Meta+a")
            bridge.key_combination("Delete")
        bridge.type_text(text)
        if args.get("press_enter"):
            bridge.key_combination("Return")
        return f"type_text_at({x},{y},len={len(text)})"
    if name == "key_combination":
        keys = args.get("keys", "")
        bridge.key_combination(keys)
        return f"key_combination({keys!r})"
    if name == "scroll_at":
        x, y = _norm(args.get("x", 0), args.get("y", 0))
        direction = args.get("direction", "down")
        magnitude = int(args.get("magnitude", 100))
        bridge.scroll(x, y, direction, magnitude)
        return f"scroll_at({x},{y},{direction})"
    if name == "scroll_document":
        direction = args.get("direction", "down")
        bridge.scroll(width // 2, height // 2, direction, 200)
        return f"scroll_document({direction})"
    if name == "wait_5_seconds":
        bridge.wait(5)
        return "wait_5_seconds()"
    if name == "hover_at":
        return "hover_at: not supported on cove control socket (no-op)"
    if name in ("navigate", "open_web_browser", "search", "go_back", "go_forward", "drag_and_drop"):
        return f"{name}: not implemented on macOS guest (no-op)"
    return f"{name}: unknown action (no-op)"


def _extract_function_calls(response: dict[str, Any]) -> list[dict[str, Any]]:
    candidates = response.get("candidates") or []
    if not candidates:
        return []
    parts = candidates[0].get("content", {}).get("parts", []) or []
    return [p["function_call"] for p in parts if isinstance(p, dict) and "function_call" in p]


def _extract_text(response: dict[str, Any]) -> str:
    candidates = response.get("candidates") or []
    if not candidates:
        return ""
    parts = candidates[0].get("content", {}).get("parts", []) or []
    chunks = [p.get("text", "") for p in parts if isinstance(p, dict) and "text" in p]
    return "\n".join(c for c in chunks if c)


# --- main loop -----------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description="Drive a cove macOS VM with the Google Gemini computer-use API.")
    parser.add_argument("--vm", required=True, help="cove VM name (matches ~/.vz/vms/<name>/)")
    parser.add_argument("--task", required=True, help="Natural-language task for the model.")
    parser.add_argument(
        "--model",
        default="gemini-2.5-computer-use-preview-10-2025",
        help="Gemini computer-use model name.",
    )
    parser.add_argument("--max-iterations", type=int, default=5, help="Maximum function_call rounds before stopping.")
    parser.add_argument("--token", default=None, help="Override the cove control-socket auth token.")
    args = parser.parse_args()

    api_key = os.environ.get("GEMINI_API_KEY", "").strip()
    if not api_key:
        print("error: GEMINI_API_KEY is not set", file=sys.stderr)
        sys.exit(2)

    bridge = CoveGeminiBridge(args.vm, control_token=args.token)
    width, height = bridge.screen_size()
    initial_png = bridge.screenshot()

    contents: list[dict[str, Any]] = _initial_request(args.task, initial_png)["contents"]
    tools = [{"computer_use": {"environment": "ENVIRONMENT_BROWSER"}}]
    headers = {"x-goog-api-key": api_key, "Content-Type": "application/json"}
    url = GEMINI_ENDPOINT.format(model=args.model)

    final_text = ""
    for iteration in range(1, args.max_iterations + 1):
        response = _http_post_json(url, headers, {"contents": contents, "tools": tools})
        calls = _extract_function_calls(response)
        text = _extract_text(response)
        if text:
            final_text = text
        if not calls:
            break

        # Append the model's turn so the next round sees it.
        candidate_content = response["candidates"][0].get("content") or {}
        contents.append(candidate_content)

        function_response_parts: list[dict[str, Any]] = []
        for call in calls:
            name = call.get("name", "")
            call_args = call.get("args") or {}
            try:
                log_line = _dispatch(bridge, name, call_args, width, height)
            except Exception as exc:  # noqa: BLE001 - we surface to the model
                log_line = f"{name}: error: {exc}"
            print(f"[iter {iteration}] {log_line}", file=sys.stderr)
            shot = bridge.screenshot()
            function_response_parts.append(_function_response_part(name, shot))

        contents.append({"role": "user", "parts": function_response_parts})

    if final_text:
        print(final_text)
    else:
        print("(no final text produced; iteration cap reached)", file=sys.stderr)


if __name__ == "__main__":
    main()
