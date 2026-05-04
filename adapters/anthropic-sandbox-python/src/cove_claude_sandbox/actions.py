from __future__ import annotations

import base64
import time
from dataclasses import dataclass
from typing import Any

from .client import CoveClient


_KEY_CODES = {
    "a": 0,
    "s": 1,
    "d": 2,
    "f": 3,
    "h": 4,
    "g": 5,
    "z": 6,
    "x": 7,
    "c": 8,
    "v": 9,
    "b": 11,
    "q": 12,
    "w": 13,
    "e": 14,
    "r": 15,
    "y": 16,
    "t": 17,
    "o": 31,
    "u": 32,
    "i": 34,
    "p": 35,
    "l": 37,
    "j": 38,
    "k": 40,
    "n": 45,
    "m": 46,
    ".": 47,
    "enter": 36,
    "return": 36,
    "tab": 48,
    "space": 49,
    "backspace": 51,
    "delete": 51,
    "escape": 53,
    "esc": 53,
    "pageup": 116,
    "pagedown": 121,
    "left": 123,
    "right": 124,
    "down": 125,
    "up": 126,
}

_MODIFIERS = {
    "shift": 1 << 17,
    "ctrl": 1 << 18,
    "control": 1 << 18,
    "alt": 1 << 19,
    "option": 1 << 19,
    "cmd": 1 << 20,
    "command": 1 << 20,
    "meta": 1 << 20,
}


class AnthropicToolError(RuntimeError):
    pass


@dataclass(frozen=True)
class CoordinateScaler:
    width: int
    height: int
    max_long_edge: int = 1568

    @property
    def scale(self) -> float:
        edge = max(self.width, self.height)
        if edge <= self.max_long_edge:
            return 1.0
        return self.max_long_edge / edge

    @property
    def display_width(self) -> int:
        return round(self.width * self.scale)

    @property
    def display_height(self) -> int:
        return round(self.height * self.scale)

    def to_host(self, x: int, y: int) -> tuple[int, int]:
        scale = self.scale
        if scale == 1.0:
            return x, y
        return round(x / scale), round(y / scale)


class AnthropicToolDispatcher:
    def __init__(self, client: CoveClient, *, width: int = 1024, height: int = 768) -> None:
        self.client = client
        self.scaler = CoordinateScaler(width=width, height=height)

    def dispatch(self, tool_use: Any) -> dict[str, Any]:
        tool_id = _field(tool_use, "id")
        try:
            content = self._dispatch(tool_use)
            return {"type": "tool_result", "tool_use_id": tool_id, "content": content}
        except Exception as exc:  # noqa: BLE001
            return {
                "type": "tool_result",
                "tool_use_id": tool_id,
                "is_error": True,
                "content": _short_error(exc),
            }

    def _dispatch(self, tool_use: Any) -> str | list[dict[str, Any]]:
        name = str(_field(tool_use, "name"))
        args = dict(_field(tool_use, "input") or {})
        if name == "computer":
            return self._computer(args)
        if name == "bash":
            return self._bash(args)
        if name in {"str_replace_based_edit_tool", "str_replace_editor", "text_editor"}:
            return self._text_editor(args)
        raise AnthropicToolError(f"unsupported tool {name}")

    def _computer(self, args: dict[str, Any]) -> str | list[dict[str, Any]]:
        action = str(args.get("action", ""))
        if action == "screenshot":
            return self._screenshot_result()
        if action in {"mouse_move", "move"}:
            x, y = self._coordinates(args)
            self.client.mouse(x, y, "move")
            return "moved"
        if action in {"left_click", "click"}:
            x, y = self._coordinates(args)
            self.client.mouse(x, y, "click", button=0)
            return "clicked"
        if action == "right_click":
            x, y = self._coordinates(args)
            self.client.mouse(x, y, "click", button=1)
            return "right clicked"
        if action == "double_click":
            x, y = self._coordinates(args)
            for _ in range(2):
                self.client.mouse(x, y, "click", button=0)
                time.sleep(0.05)
            return "double clicked"
        if action == "triple_click":
            x, y = self._coordinates(args)
            for _ in range(3):
                self.client.mouse(x, y, "click", button=0)
                time.sleep(0.05)
            return "triple clicked"
        if action == "left_mouse_down":
            x, y = self._coordinates(args)
            self.client.mouse(x, y, "down", button=0)
            return "mouse down"
        if action == "left_mouse_up":
            x, y = self._coordinates(args)
            self.client.mouse(x, y, "up", button=0)
            return "mouse up"
        if action == "type":
            self.client.text(str(args.get("text", "")))
            return "typed"
        if action in {"key", "keypress"}:
            keys = _keys(args)
            key_code, modifiers = _resolve_keys(keys)
            self.client.key(key_code, modifiers=modifiers)
            return "pressed"
        if action == "scroll":
            scroll_y = int(args.get("scroll_y", args.get("scroll_amount", 0)) or 0)
            key = "pagedown" if scroll_y > 0 else "pageup"
            key_code, modifiers = _resolve_keys([key])
            for _ in range(max(1, min(5, abs(scroll_y) // 400 or 1))):
                self.client.key(key_code, modifiers=modifiers)
            return "scrolled"
        if action == "wait":
            time.sleep(float(args.get("duration", 0.5) or 0.5))
            return "waited"
        raise AnthropicToolError(f"unsupported computer action {action}")

    def _bash(self, args: dict[str, Any]) -> str:
        if args.get("restart"):
            return "bash session restart is not needed for cove guest exec"
        command = str(args.get("command", ""))
        if not command:
            raise AnthropicToolError("bash command is required")
        result = self.client.exec(command)
        text = _exec_text(result.exit_code, result.stdout, result.stderr)
        if result.exit_code != 0:
            raise AnthropicToolError(text)
        return text

    def _text_editor(self, args: dict[str, Any]) -> str:
        command = str(args.get("command", ""))
        path = str(args.get("path", ""))
        if not path:
            raise AnthropicToolError("text editor path is required")
        if command == "view":
            return self.client.read_file(path).decode("utf-8", errors="replace")
        if command == "create":
            self.client.write_file(path, str(args.get("file_text", "")))
            return f"created {path}"
        if command == "str_replace":
            old = str(args.get("old_str", ""))
            new = str(args.get("new_str", ""))
            if not old:
                raise AnthropicToolError("old_str is required")
            text = self.client.read_file(path).decode("utf-8", errors="replace")
            count = text.count(old)
            if count != 1:
                raise AnthropicToolError(f"old_str matched {count} times")
            self.client.write_file(path, text.replace(old, new, 1))
            return f"updated {path}"
        if command == "insert":
            line = int(args.get("insert_line", 0))
            text = self.client.read_file(path).decode("utf-8", errors="replace")
            lines = text.splitlines(keepends=True)
            index = max(0, min(line, len(lines)))
            lines.insert(index, str(args.get("new_str", "")))
            self.client.write_file(path, "".join(lines))
            return f"updated {path}"
        raise AnthropicToolError(f"unsupported text editor command {command}")

    def _coordinates(self, args: dict[str, Any]) -> tuple[int, int]:
        if "coordinate" in args:
            raw = args["coordinate"]
            if not isinstance(raw, (list, tuple)) or len(raw) != 2:
                raise AnthropicToolError("coordinate must be [x, y]")
            x, y = int(raw[0]), int(raw[1])
        else:
            x, y = int(args.get("x", 0)), int(args.get("y", 0))
        return self.scaler.to_host(x, y)

    def _screenshot_result(self) -> list[dict[str, Any]]:
        image = self.client.screenshot(scale=self.scaler.scale, fmt="png")
        return [
            {
                "type": "image",
                "source": {
                    "type": "base64",
                    "media_type": "image/png",
                    "data": base64.b64encode(image).decode("ascii"),
                },
            }
        ]


def _field(block: Any, name: str) -> Any:
    if isinstance(block, dict):
        return block.get(name)
    return getattr(block, name)


def _keys(args: dict[str, Any]) -> list[str]:
    value = args.get("key", args.get("keys", args.get("text", "")))
    if isinstance(value, str):
        return [part for part in value.replace("+", " ").split() if part]
    if isinstance(value, list):
        return [str(part) for part in value]
    raise AnthropicToolError("key must be a string or list")


def _resolve_keys(keys: list[str]) -> tuple[int, int]:
    if not keys:
        raise AnthropicToolError("keys is empty")
    modifiers = 0
    key_code: int | None = None
    for key in keys:
        normalized = key.lower().replace("_", "").replace(" ", "")
        if normalized in _MODIFIERS:
            modifiers |= _MODIFIERS[normalized]
            continue
        if normalized not in _KEY_CODES:
            raise AnthropicToolError(f"unknown key {key!r}")
        key_code = _KEY_CODES[normalized]
    if key_code is None:
        raise AnthropicToolError("keypress requires a non-modifier key")
    return key_code, modifiers


def _exec_text(exit_code: int, stdout: str, stderr: str) -> str:
    parts = [f"exit_code: {exit_code}"]
    if stdout:
        parts.append(f"stdout:\n{stdout}")
    if stderr:
        parts.append(f"stderr:\n{stderr}")
    return "\n".join(parts)


def _short_error(exc: Exception) -> str:
    text = str(exc).strip()
    return text if text else exc.__class__.__name__.lower()
