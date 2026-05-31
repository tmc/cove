from __future__ import annotations

import base64
import json
import os
import socket
import threading
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pytest

from cove_sandbox import CoveClient, CoveComputer, CoveError, CoveFleetClient
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
    sock = _short_socket_path()
    server = _UnixServer(sock, {"success": True, "screenshot_result": {"image_data": _b64(b"image")}})
    server.start()
    screenshots = tmp_path / "screens"
    events = tmp_path / "events.jsonl"
    computer = CoveComputer(CoveClient(socket_path=sock), screenshot_dir=str(screenshots), events_jsonl=str(events))
    assert base64.b64decode(computer.screenshot()) == b"image"
    assert (screenshots / "step-001.png").read_bytes() == b"image"
    row = json.loads(events.read_text().splitlines()[0])
    assert row["action"] == "screenshot"
    assert row["path"].endswith("step-001.png")
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
        provider="cloud",
        parent="base",
        name="eval-001",
        fleet_url="http://127.0.0.1:9758",
        workspace_root="/tmp/work",
        gui=True,
        extra_run_args=("-disposable",),
    )
    assert opts.model_dump()["type"] == "cove"
    assert opts.model_dump()["provider"] == "cloud"
    assert opts.model_dump()["parent"] == "base"
    assert opts.model_dump()["fleet_url"] == "http://127.0.0.1:9758"
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


def test_fleet_client_create_wait_exec_and_delete() -> None:
    server = _FleetHTTPServer()
    server.start()
    try:
        client = CoveFleetClient.create_sandbox(
            fleet_url=server.url,
            api_key="secret",
            image_ref="base:v1",
            sandbox_id="job-1",
        )
        client.wait_ready(timeout=1)
        result = client.exec(["/bin/echo", "ok"], env={"A": "1"}, timeout=2.5)
        assert result.exit_code == 7
        assert result.stdout == "out"
        assert result.stderr == "err"
        client.delete_vm()

        paths = [req["path"] for req in server.requests]
        assert paths == [
            "/v1/sandboxes",
            "/v1/sandboxes/job-1",
            "/v1/sandboxes/job-1/exec",
            "/v1/sandboxes/job-1",
        ]
        create = server.requests[0]
        assert create["authorization"] == "Bearer secret"
        assert create["body"]["image_ref"] == "base:v1"
        exec_req = server.requests[2]
        assert exec_req["body"]["command"] == ["/bin/echo", "ok"]
        assert exec_req["body"]["env"] == {"A": "1"}
        assert exec_req["body"]["timeout"] == "2.5s"
    finally:
        server.stop()


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


class _FleetHTTPServer:
    def __init__(self) -> None:
        self.requests: list[dict[str, object]] = []
        self.httpd = HTTPServer(("127.0.0.1", 0), self._handler())
        host, port = self.httpd.server_address
        self.url = f"http://{host}:{port}"
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def start(self) -> None:
        self.thread.start()

    def stop(self) -> None:
        self.httpd.shutdown()
        self.thread.join(timeout=1)
        self.httpd.server_close()

    def _handler(self) -> type[BaseHTTPRequestHandler]:
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                owner.requests.append(
                    {
                        "method": "GET",
                        "path": self.path,
                        "authorization": self.headers.get("authorization", ""),
                        "body": {},
                    }
                )
                if self.path == "/v1/sandboxes/job-1":
                    self._write({"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "ready"})
                    return
                self.send_error(404)

            def do_POST(self) -> None:  # noqa: N802
                body = self._read_json()
                owner.requests.append(
                    {
                        "method": "POST",
                        "path": self.path,
                        "authorization": self.headers.get("authorization", ""),
                        "body": body,
                    }
                )
                if self.path == "/v1/sandboxes":
                    self._write({"id": "job-1", "vm_name": "cove-sandbox-job-1", "status": "pending"})
                    return
                if self.path == "/v1/sandboxes/job-1/exec":
                    self._write({"done": True, "exit_code": 7, "stdout": "out", "stderr": "err"})
                    return
                self.send_error(404)

            def do_DELETE(self) -> None:  # noqa: N802
                owner.requests.append(
                    {
                        "method": "DELETE",
                        "path": self.path,
                        "authorization": self.headers.get("authorization", ""),
                        "body": {},
                    }
                )
                if self.path == "/v1/sandboxes/job-1":
                    self._write({"id": "job-1", "status": "draining"})
                    return
                self.send_error(404)

            def log_message(self, format: str, *args: object) -> None:
                del format, args

            def _read_json(self) -> dict[str, object]:
                n = int(self.headers.get("content-length") or "0")
                if n == 0:
                    return {}
                return json.loads(self.rfile.read(n))

            def _write(self, payload: dict[str, object]) -> None:
                data = json.dumps(payload).encode()
                self.send_response(200)
                self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        return Handler
