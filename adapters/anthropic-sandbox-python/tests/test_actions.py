from __future__ import annotations

import base64

from cove_claude_sandbox.actions import AnthropicToolDispatcher, CoordinateScaler, _resolve_keys
from cove_claude_sandbox.client import ExecResult


def test_screenshot_returns_image_content() -> None:
    client = _Client()
    result = AnthropicToolDispatcher(client).dispatch(
        {"id": "u1", "name": "computer", "input": {"action": "screenshot"}}
    )
    assert result["tool_use_id"] == "u1"
    image = result["content"][0]
    assert image["source"]["media_type"] == "image/png"
    assert base64.b64decode(image["source"]["data"]) == b"png"


def test_click_scales_coordinates() -> None:
    client = _Client()
    result = AnthropicToolDispatcher(client, width=3136, height=1960).dispatch(
        {"id": "u1", "name": "computer", "input": {"action": "left_click", "coordinate": [1568, 980]}}
    )
    assert result["content"] == "clicked"
    assert client.mouse_calls == [(3136, 1960, "click", 0)]


def test_keypress_resolves_modifiers() -> None:
    key, modifiers = _resolve_keys(["cmd", "shift", "a"])
    assert key == 0
    assert modifiers == (1 << 20) | (1 << 17)


def test_bash_nonzero_returns_tool_error() -> None:
    client = _Client(exec_result=ExecResult(exit_code=2, stdout="", stderr="nope"))
    result = AnthropicToolDispatcher(client).dispatch(
        {"id": "u1", "name": "bash", "input": {"command": "false"}}
    )
    assert result["is_error"] is True
    assert "exit_code: 2" in result["content"]
    assert "nope" in result["content"]


def test_text_editor_replace_requires_unique_match() -> None:
    client = _Client(files={"/tmp/a": b"same same"})
    result = AnthropicToolDispatcher(client).dispatch(
        {
            "id": "u1",
            "name": "str_replace_based_edit_tool",
            "input": {"command": "str_replace", "path": "/tmp/a", "old_str": "same", "new_str": "new"},
        }
    )
    assert result["is_error"] is True
    assert "matched 2 times" in result["content"]


def test_coordinate_scaler_leaves_small_display_unchanged() -> None:
    scaler = CoordinateScaler(width=1024, height=768)
    assert scaler.display_width == 1024
    assert scaler.display_height == 768
    assert scaler.to_host(7, 9) == (7, 9)


class _Client:
    def __init__(self, *, exec_result: ExecResult | None = None, files: dict[str, bytes] | None = None) -> None:
        self.exec_result = exec_result or ExecResult(exit_code=0, stdout="ok\n", stderr="")
        self.files = dict(files or {})
        self.mouse_calls: list[tuple[int, int, str, int]] = []

    def screenshot(self, **kwargs: object) -> bytes:
        del kwargs
        return b"png"

    def mouse(self, x: int, y: int, action: str, *, button: int = 0) -> None:
        self.mouse_calls.append((x, y, action, button))

    def key(self, key_code: int, *, modifiers: int = 0) -> None:
        del key_code, modifiers

    def text(self, text: str) -> None:
        del text

    def exec(self, command: str) -> ExecResult:
        del command
        return self.exec_result

    def read_file(self, path: str) -> bytes:
        return self.files[path]

    def write_file(self, path: str, data: str | bytes) -> None:
        self.files[path] = data.encode() if isinstance(data, str) else data
