from __future__ import annotations

import base64
import json
import os
import shlex
import socket
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Mapping, Sequence


class CoveError(RuntimeError):
    pass


@dataclass(frozen=True)
class ExecResult:
    exit_code: int
    stdout: str
    stderr: str
    duration_seconds: float = 0.0

    def check_returncode(self) -> "ExecResult":
        if self.exit_code != 0:
            raise CoveError(f"guest command exited {self.exit_code}: {self.stderr.strip()}")
        return self


class CoveClient:
    def __init__(
        self,
        *,
        vm: str | None = None,
        socket_path: str | os.PathLike[str] | None = None,
        cove: str = "cove",
        token: str | None = None,
        timeout: float = 30.0,
    ) -> None:
        if vm is None and socket_path is None:
            raise ValueError("vm or socket_path is required")
        self.vm = vm
        self.socket_path = Path(socket_path) if socket_path is not None else self._vm_socket_path(vm)
        self.cove = cove
        self.token = token if token is not None else self._load_token()
        self.timeout = timeout

    @classmethod
    def from_env(cls) -> "CoveClient":
        vm = os.environ.get("COVE_VM")
        socket_path = os.environ.get("COVE_CONTROL_SOCKET")
        cove = os.environ.get("COVE_BIN", "cove")
        token = os.environ.get("VZ_MACOS_CTL_TOKEN")
        return cls(vm=vm, socket_path=socket_path, cove=cove, token=token)

    def fork(self, parent: str, child: str) -> None:
        self.run_cove(["fork", parent, child])
        self.refresh_token()

    def refresh_token(self) -> None:
        self.token = self._load_token()

    def start(self, *, gui: bool = False, extra_args: Sequence[str] = ()) -> subprocess.Popen[bytes]:
        if not self.vm:
            raise CoveError("start requires a VM name")
        args = ["-vm", self.vm, "run", "-gui" if gui else "-headless", *extra_args]
        return subprocess.Popen([self.cove, *args])

    def stop(self, *, force: bool = False) -> None:
        if force:
            self.control({"type": "stop"})
            return
        self.control({"type": "agent-shutdown", "agent_shutdown": {}})

    def wait_ready(self, timeout: float = 120.0) -> None:
        deadline = time.monotonic() + timeout
        last: Exception | None = None
        while time.monotonic() < deadline:
            try:
                self.control({"type": "agent-ping"})
                return
            except Exception as exc:  # noqa: BLE001
                last = exc
                time.sleep(1)
        if last is None:
            raise CoveError("timed out waiting for guest agent")
        raise CoveError(f"timed out waiting for guest agent: {last}")

    def exec(
        self,
        command: str | Sequence[str],
        *,
        env: Mapping[str, str] | None = None,
        cwd: str = "",
        timeout: float | None = None,
    ) -> ExecResult:
        args = ["/bin/zsh", "-lc", command] if isinstance(command, str) else list(command)
        resp = self.control(
            {
                "type": "agent-exec",
                "agent_exec": {
                    "args": args,
                    "env": dict(env or {}),
                    "working_dir": cwd,
                },
            },
            timeout=timeout or max(self.timeout, 600),
        )
        data = resp.get("agent_exec_result") or {}
        if not data and isinstance(resp.get("data"), str):
            try:
                data = json.loads(resp["data"])
            except json.JSONDecodeError:
                data = {}
        return ExecResult(
            exit_code=int(data.get("exit_code", 0)),
            stdout=str(data.get("stdout", "")),
            stderr=str(data.get("stderr", "")),
            duration_seconds=float(data.get("duration_seconds", 0.0)),
        )

    def read_file(self, path: str) -> bytes:
        resp = self.control({"type": "agent-read", "agent_read": {"path": path}})
        data = (resp.get("agent_file") or {}).get("data")
        if data is None:
            raise CoveError(f"guest read returned no data for {path}")
        return base64.b64decode(data)

    def write_file(self, path: str, data: bytes | str, *, mode: int = 0o644) -> None:
        raw = data.encode() if isinstance(data, str) else data
        self.control(
            {
                "type": "agent-write",
                "agent_write": {
                    "path": path,
                    "data": base64.b64encode(raw).decode("ascii"),
                    "mode": mode,
                },
            }
        )

    def screenshot(self, *, scale: float = 1.0, fmt: str = "png", quality: int = 90) -> bytes:
        resp = self.control(
            {
                "type": "screenshot",
                "screenshot": {
                    "scale": scale,
                    "quality": quality,
                    "format": fmt,
                },
            },
            timeout=max(self.timeout, 30),
        )
        result = resp.get("screenshot_result") or {}
        data = result.get("image_data") or resp.get("data")
        if not isinstance(data, str):
            raise CoveError("screenshot returned no image data")
        return base64.b64decode(data)

    def key(self, key_code: int, *, down: bool | None = None, modifiers: int = 0) -> None:
        if down is None:
            self.key(key_code, down=True, modifiers=modifiers)
            time.sleep(0.05)
            self.key(key_code, down=False, modifiers=modifiers)
            return
        self.control(
            {
                "type": "key",
                "key": {
                    "key_code": key_code,
                    "key_down": down,
                    "modifiers": modifiers,
                    "use_cg_event": modifiers != 0,
                },
            }
        )

    def text(self, text: str) -> None:
        self.control({"type": "text", "text": {"text": text}}, timeout=max(self.timeout, 10 + len(text) / 10))

    def mouse(self, x: int, y: int, action: str, *, button: int = 0) -> None:
        self.control(
            {
                "type": "mouse",
                "mouse": {
                    "x": x,
                    "y": y,
                    "button": button,
                    "action": action,
                    "absolute": True,
                },
            }
        )

    def control(self, request: dict[str, Any], *, timeout: float | None = None) -> dict[str, Any]:
        req = dict(request)
        if self.token and "auth_token" not in req:
            req["auth_token"] = self.token
        data = (json.dumps(req, separators=(",", ":")) + "\n").encode()
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(timeout or self.timeout)
            sock.connect(str(self.socket_path))
            sock.sendall(data)
            line = self._read_line(sock)
        resp = json.loads(line)
        if not resp.get("success"):
            raise CoveError(str(resp.get("error") or "control request failed"))
        return resp

    def run_cove(self, args: Sequence[str], *, timeout: float | None = None) -> str:
        proc = subprocess.run(
            [self.cove, *args],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout or self.timeout,
            check=False,
        )
        if proc.returncode != 0:
            cmd = " ".join(shlex.quote(x) for x in [self.cove, *args])
            raise CoveError(f"{cmd}: {proc.stderr.strip()}")
        return proc.stdout

    def delete_vm(self, vm: str | None = None) -> None:
        name = vm or self.vm
        if not name:
            raise CoveError("delete_vm requires a VM name")
        self.run_cove(["vm", "delete", name], timeout=max(self.timeout, 120))

    def _load_token(self) -> str:
        token = os.environ.get("VZ_MACOS_CTL_TOKEN")
        if token:
            return token.strip()
        path = self.socket_path.with_name("control.token")
        try:
            return path.read_text().strip()
        except FileNotFoundError:
            return ""

    @staticmethod
    def _vm_socket_path(vm: str | None) -> Path:
        if not vm:
            raise ValueError("vm is required")
        return Path.home() / ".vz" / "vms" / vm / "control.sock"

    @staticmethod
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
            raise CoveError("control socket returned no response")
        return b"".join(chunks).split(b"\n", 1)[0].decode()
