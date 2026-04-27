from __future__ import annotations

import base64
import time
from typing import Any

from .client import CoveClient

try:  # pragma: no cover - exercised only when openai-agents is installed.
    from agents.computer import Computer as _Computer
except Exception:  # noqa: BLE001
    _Computer = object


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


class CoveComputer(_Computer):
    def __init__(self, client: CoveClient, *, width: int = 1024, height: int = 768) -> None:
        self.client = client
        self._dimensions = (width, height)

    @property
    def environment(self) -> str:
        return "mac"

    @property
    def dimensions(self) -> tuple[int, int]:
        return self._dimensions

    def screenshot(self) -> str:
        return base64.b64encode(self.client.screenshot(fmt="png")).decode("ascii")

    def click(self, x: int, y: int, button: Any) -> None:
        self.client.mouse(x, y, "click", button=_button_number(button))

    def double_click(self, x: int, y: int) -> None:
        self.click(x, y, "left")
        time.sleep(0.05)
        self.click(x, y, "left")

    def scroll(self, x: int, y: int, scroll_x: int, scroll_y: int) -> None:
        del x, y, scroll_x
        key = "pagedown" if scroll_y > 0 else "pageup"
        for _ in range(max(1, min(5, abs(scroll_y) // 400 or 1))):
            self.keypress([key])

    def type(self, text: str) -> None:
        self.client.text(text)

    def wait(self) -> None:
        time.sleep(0.5)

    def move(self, x: int, y: int) -> None:
        self.client.mouse(x, y, "move")

    def keypress(self, keys: list[str]) -> None:
        key_code, modifiers = _resolve_keys(keys)
        self.client.key(key_code, modifiers=modifiers)

    def drag(self, path: list[tuple[int, int]]) -> None:
        if not path:
            return
        first = path[0]
        self.client.mouse(first[0], first[1], "move")
        self.client.mouse(first[0], first[1], "down")
        for x, y in path[1:]:
            self.client.mouse(x, y, "move")
        last = path[-1]
        self.client.mouse(last[0], last[1], "up")


def _button_number(button: Any) -> int:
    name = getattr(button, "value", button)
    name = str(name).lower()
    if name in {"right", "secondary", "2"}:
        return 1
    return 0


def _resolve_keys(keys: list[str]) -> tuple[int, int]:
    if not keys:
        raise ValueError("keys is empty")
    modifiers = 0
    key_code: int | None = None
    for key in keys:
        normalized = key.lower().replace("_", "").replace(" ", "")
        if normalized in _MODIFIERS:
            modifiers |= _MODIFIERS[normalized]
            continue
        if normalized not in _KEY_CODES:
            raise ValueError(f"unknown key {key!r}")
        key_code = _KEY_CODES[normalized]
    if key_code is None:
        raise ValueError("keypress requires a non-modifier key")
    return key_code, modifiers
