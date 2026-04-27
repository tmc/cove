from __future__ import annotations

import base64
import json
import os
import socket
import threading
import uuid
from pathlib import Path

import pytest

from cove_sandbox import CoveClient, CoveComputer, CoveError
from cove_sandbox import backend as backend_module
from cove_sandbox.backend import CoveSandboxClientOptions, CoveSandboxSessionState
from cove_sandbox.computer import _resolve_keys


def test_resolve_keys() -> None:
    key, modifiers = _resolve_keys(["cmd", "shift", "a"])
    assert key == 0
    assert modifiers == (1 << 20) | (1 << 17)


def test_screenshot_reads_typed_image(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True, "screenshot_result": {"image_data": _b64(b"png")}})
    server.start()
    client = CoveClient(socket_path=sock, token="tok")
    assert client.screenshot() == b"png"
    assert server.request["auth_token"] == "tok"
    assert server.request["screenshot"]["format"] == "png"


def test_exec_result(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(
        sock,
        {
            "success": True,
            "agent_exec_result": {
                "exit_code": 2,
                "stdout": "out",
                "stderr": "err",
                "duration_seconds": 0.25,
            },
        },
    )
    server.start()
    result = CoveClient(socket_path=sock).exec("false")
    assert result.exit_code == 2
    assert result.stdout == "out"
    assert result.stderr == "err"
    with pytest.raises(CoveError):
        result.check_returncode()


def test_computer_screenshot_is_base64(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True, "screenshot_result": {"image_data": _b64(b"image")}})
    server.start()
    computer = CoveComputer(CoveClient(socket_path=sock))
    assert base64.b64decode(computer.screenshot()) == b"image"
    assert computer.environment == "mac"
    assert computer.dimensions == (1024, 768)


def test_control_error(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": False, "error": "nope"})
    server.start()
    with pytest.raises(CoveError, match="nope"):
        CoveClient(socket_path=sock).control({"type": "ping"})


def test_write_file_sends_base64(tmp_path: Path) -> None:
    del tmp_path
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True})
    server.start()
    CoveClient(socket_path=sock).write_file("/tmp/file", b"\x00hello")
    assert server.request["agent_write"]["data"] == _b64(b"\x00hello")
    assert server.request["agent_write"]["mode"] == 0o644


def test_backend_options_round_trip_without_agents() -> None:
    opts = CoveSandboxClientOptions(
        parent="base",
        name="eval-001",
        workspace_root="/tmp/work",
        gui=True,
        extra_run_args=("-disposable",),
    )
    assert opts.model_dump()["type"] == "cove"
    assert opts.model_dump()["parent"] == "base"
    assert opts.model_dump()["extra_run_args"] == ("-disposable",)


def test_backend_state_round_trip_without_agents() -> None:
    kwargs = _state_kwargs()
    state = CoveSandboxSessionState(**kwargs)
    payload = state.model_dump()
    restored = CoveSandboxSessionState.model_validate(payload)
    assert restored.type == "cove"
    assert restored.vm == "eval-001"
    assert restored.workspace_root == "/tmp/work"
    assert restored.owned is True


def _state_kwargs() -> dict[str, object]:
    kwargs: dict[str, object] = {
        "vm": "eval-001",
        "workspace_root": "/tmp/work",
        "owned": True,
        "delete_on_close": True,
    }
    if backend_module._AGENTS_AVAILABLE:
        from agents.sandbox.manifest import Manifest
        from agents.sandbox.snapshot import resolve_snapshot

        kwargs["manifest"] = Manifest(root="/tmp/work")
        kwargs["snapshot"] = resolve_snapshot(None, "test")
    return kwargs


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii")


def _short_socket_path() -> Path:
    return Path(f"/tmp/cove-{os.getpid()}-{uuid.uuid4().hex[:8]}.sock")


class _UnixServer:
    def __init__(self, path: Path, response: dict[str, object]) -> None:
        self.path = path
        self.response = response
        self.request: dict[str, object] = {}
        self.ready = threading.Event()
        self.thread = threading.Thread(target=self._serve, daemon=True)

    def start(self) -> None:
        try:
            self.path.unlink()
        except FileNotFoundError:
            pass
        self.thread.start()
        if self.ready.wait(timeout=1):
            return
        raise RuntimeError(f"server did not bind {self.path}")

    def _serve(self) -> None:
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
                sock.bind(str(self.path))
                sock.listen(1)
                self.ready.set()
                conn, _ = sock.accept()
                with conn:
                    line = conn.recv(65536).split(b"\n", 1)[0]
                    self.request = json.loads(line)
                    conn.sendall(json.dumps(self.response).encode() + b"\n")
        finally:
            try:
                self.path.unlink()
            except FileNotFoundError:
                pass
