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
