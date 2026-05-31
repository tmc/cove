from __future__ import annotations

import base64
import json
import os
import shlex
import socket
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
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

    def start(self, *, gui: bool = False, extra_args: Sequence[str] = ()) -> subprocess.Popen[bytes]:
        if not self.vm:
            raise CoveError("start requires a VM name")
        args = ["-vm", self.vm, "run"]
        args.append("-gui" if gui else "-headless")
        args.extend(extra_args)
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


class CoveFleetClient:
    def __init__(
        self,
        *,
        sandbox_id: str,
        fleet_url: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        vm: str | None = None,
        timeout: float = 30.0,
    ) -> None:
        sandbox_id = sandbox_id.strip()
        if not sandbox_id:
            raise ValueError("sandbox_id is required")
        fleet_url = (fleet_url or os.environ.get("COVE_FLEET_URL") or "").strip()
        if not fleet_url:
            raise ValueError("fleet_url is required")
        self.sandbox_id = sandbox_id
        self.fleet_url = fleet_url.rstrip("/")
        self.api_key = api_key if api_key is not None else _fleet_api_key_from_env()
        self.namespace = namespace.strip() if namespace else ""
        self.vm = vm
        self.timeout = timeout

    @classmethod
    def create_sandbox(
        cls,
        *,
        fleet_url: str | None = None,
        image_ref: str,
        sandbox_id: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        vm_name: str | None = None,
        timeout: float = 30.0,
    ) -> "CoveFleetClient":
        image_ref = image_ref.strip()
        if not image_ref:
            raise ValueError("image_ref is required")
        body: dict[str, object] = {"image_ref": image_ref}
        if sandbox_id:
            body["id"] = sandbox_id
        if namespace:
            body["namespace"] = namespace
        if vm_name:
            body["vm_name"] = vm_name
        seed = cls(
            sandbox_id=sandbox_id or "pending",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        data = seed._request("POST", "/v1/sandboxes", body, timeout=timeout)
        created_id = str(data.get("id") or sandbox_id or "").strip()
        if not created_id:
            raise CoveError("fleet sandbox create returned no id")
        return cls(
            sandbox_id=created_id,
            fleet_url=seed.fleet_url,
            api_key=seed.api_key,
            namespace=str(data.get("namespace") or namespace or ""),
            vm=str(data.get("vm_name") or vm_name or ""),
            timeout=timeout,
        )

    @classmethod
    def list_sandboxes(
        cls,
        *,
        fleet_url: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        status: str | None = None,
        worker_id: str | None = None,
        image_ref: str | None = None,
        limit: int | None = None,
        timeout: float = 30.0,
    ) -> list[dict[str, Any]]:
        seed = cls(
            sandbox_id="list",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        return seed.list(
            status=status,
            worker_id=worker_id,
            image_ref=image_ref,
            limit=limit,
        )

    def list(
        self,
        *,
        status: str | None = None,
        worker_id: str | None = None,
        image_ref: str | None = None,
        limit: int | None = None,
    ) -> list[dict[str, Any]]:
        query: dict[str, str] = {
            "namespace": self.namespace,
            "status": status or "",
            "worker_id": worker_id or "",
            "image_ref": image_ref or "",
        }
        if limit is not None:
            if limit < 0:
                raise ValueError("limit must be non-negative")
            if limit > 0:
                query["limit"] = str(limit)
        path = _query_path("/v1/sandboxes", query)
        data = self._request("GET", path, timeout=self.timeout)
        sandboxes = data.get("sandboxes") or []
        if not isinstance(sandboxes, list):
            raise CoveError("GET /v1/sandboxes: expected sandboxes list")
        return [dict(item) for item in sandboxes if isinstance(item, dict)]

    def status(self, *, timeout: float | None = None) -> dict[str, Any]:
        data = self._request("GET", self._sandbox_path(), timeout=timeout or self.timeout)
        self.vm = str(data.get("vm_name") or self.vm or "")
        return data

    def wait(self, timeout: float = 30.0) -> dict[str, Any]:
        if timeout < 0:
            raise ValueError("timeout must not be negative")
        data = self._request(
            "POST",
            _query_path(self._sandbox_path("wait"), {"timeout": _format_seconds(timeout)}),
            {},
            timeout=timeout + min(timeout, 30),
        )
        sandbox = data.get("sandbox") or {}
        if isinstance(sandbox, dict):
            self.vm = str(sandbox.get("vm_name") or self.vm or "")
        return data

    def start(self, *, gui: bool = False, extra_args: Sequence[str] = ()) -> None:
        del gui, extra_args
        self._request("POST", self._sandbox_path("start"), {}, timeout=self.timeout)

    def stop(self, *, force: bool = False) -> None:
        del force
        self._request("POST", self._sandbox_path("stop"), {}, timeout=self.timeout)

    def restart(self) -> None:
        self._request("POST", self._sandbox_path("restart"), {}, timeout=self.timeout)

    def lease(self, *, holder: str = "", ttl: float | None = None) -> dict[str, Any]:
        if ttl is not None and ttl < 0:
            raise ValueError("ttl must not be negative")
        body: dict[str, object] = {}
        if holder.strip():
            body["holder"] = holder.strip()
        if ttl is not None:
            body["ttl"] = _format_seconds(ttl)
        return self._request("POST", self._sandbox_path("lease"), body, timeout=self.timeout)

    def release_lease(self, *, holder: str = "") -> dict[str, Any]:
        path = self._sandbox_path("lease")
        if holder.strip():
            path = _query_path(path, {"holder": holder.strip()})
        return self._request("DELETE", path, timeout=self.timeout)

    def metering(self) -> dict[str, Any]:
        return self._request("GET", self._sandbox_path("metering"), timeout=self.timeout)

    def list_metering(self, *, sandbox_id: str | None = None) -> dict[str, Any]:
        query = {"namespace": self.namespace, "sandbox_id": sandbox_id or ""}
        return self._request("GET", _query_path("/v1/metering/sandboxes", query), timeout=self.timeout)

    def wait_ready(self, timeout: float = 120.0) -> None:
        deadline = time.monotonic() + timeout
        last_status = ""
        while True:
            data = self.status(timeout=min(max(timeout, 0.1), self.timeout))
            last_status = str(data.get("status") or "")
            if last_status == "ready":
                return
            if last_status in {"canceled", "complete", "failed", "stopped"}:
                raise CoveError(f"sandbox {self.sandbox_id} is {last_status}")
            if time.monotonic() >= deadline:
                raise CoveError(f"timed out waiting for sandbox {self.sandbox_id} to become ready: {last_status}")
            time.sleep(1)

    def exec(
        self,
        command: str | Sequence[str],
        *,
        env: Mapping[str, str] | None = None,
        cwd: str = "",
        timeout: float | None = None,
    ) -> ExecResult:
        args = ["/bin/zsh", "-lc", command] if isinstance(command, str) else list(command)
        if cwd:
            quoted = " ".join(shlex.quote(part) for part in args)
            args = ["/bin/zsh", "-lc", f"cd {shlex.quote(cwd)} && exec {quoted}"]
        wait = max(self.timeout, 600) if timeout is None else timeout
        data = self._request(
            "POST",
            self._sandbox_path("exec"),
            {
                "command": args,
                "env": dict(env or {}),
                "timeout": _format_seconds(wait),
            },
            timeout=wait + min(wait, 30),
        )
        if not data.get("done"):
            raise CoveError(f"sandbox exec timed out after {_format_seconds(wait)}")
        return ExecResult(
            exit_code=int(data.get("exit_code", 0)),
            stdout=str(data.get("stdout", "")),
            stderr=str(data.get("stderr", "")),
        )

    def read_file(self, path: str) -> bytes:
        result = self.exec(["/bin/sh", "-c", "/usr/bin/base64 < " + shlex.quote(path)])
        result.check_returncode()
        return base64.b64decode(result.stdout)

    def write_file(self, path: str, data: bytes | str, *, mode: int = 0o644) -> None:
        raw = data.encode() if isinstance(data, str) else data
        payload = base64.b64encode(raw).decode("ascii")
        script = (
            f"/usr/bin/base64 -d > {shlex.quote(path)} <<'COVE_EOF'\n"
            f"{payload}\n"
            "COVE_EOF\n"
            f"chmod {mode:o} {shlex.quote(path)}\n"
        )
        self.exec(["/bin/sh", "-c", script]).check_returncode()

    def screenshot(self, *, scale: float = 1.0, fmt: str = "png", quality: int = 90) -> bytes:
        data = self._control(
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
        image = data.get("data")
        response = data.get("response") or {}
        if not image and isinstance(response, dict):
            result = response.get("screenshot_result") or {}
            if isinstance(result, dict):
                image = result.get("image_data")
            if not image:
                image = response.get("data")
        if not isinstance(image, str):
            raise CoveError("screenshot returned no image data")
        return base64.b64decode(image)

    def key(self, key_code: int, *, down: bool | None = None, modifiers: int = 0) -> None:
        if down is None:
            self.key(key_code, down=True, modifiers=modifiers)
            time.sleep(0.05)
            self.key(key_code, down=False, modifiers=modifiers)
            return
        self._control(
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
        self._control({"type": "text", "text": {"text": text}}, timeout=max(self.timeout, 10 + len(text) / 10))

    def mouse(self, x: int, y: int, action: str, *, button: int = 0) -> None:
        self._control(
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
        if request.get("type") in {"screenshot", "key", "mouse", "text"}:
            return self._control(request, timeout=timeout)
        if request.get("type") != "agent-ping":
            raise CoveError("control socket request type is not available through the fleet sandbox provider")
        data = self._request("GET", self._sandbox_path(), timeout=timeout or self.timeout)
        if data.get("status") != "ready":
            raise CoveError(f"sandbox {self.sandbox_id} is {data.get('status')}")
        return {"success": True}

    def _control(self, request: dict[str, Any], *, timeout: float | None = None) -> dict[str, Any]:
        wait = timeout or self.timeout
        payload = dict(request)
        payload["timeout"] = _format_seconds(wait)
        data = self._request(
            "POST",
            self._sandbox_path("control"),
            payload,
            timeout=wait + min(wait, 30),
        )
        if not data.get("done"):
            raise CoveError(f"sandbox control timed out after {_format_seconds(wait)}")
        if data.get("error"):
            raise CoveError(str(data["error"]))
        response = data.get("response") or {}
        if isinstance(response, dict) and response.get("success") is False:
            raise CoveError(str(response.get("error") or "control request failed"))
        return data

    def delete_vm(self, vm: str | None = None) -> None:
        del vm
        self._request("DELETE", self._sandbox_path(), timeout=max(self.timeout, 120))

    def _sandbox_path(self, action: str = "") -> str:
        path = "/v1/sandboxes/" + urllib.parse.quote(self.sandbox_id, safe="")
        if action:
            path += "/" + urllib.parse.quote(action, safe="")
        return path

    def _request(
        self,
        method: str,
        path: str,
        payload: Mapping[str, object] | None = None,
        *,
        timeout: float | None = None,
    ) -> dict[str, Any]:
        url = self.fleet_url + path
        data = None
        headers: dict[str, str] = {}
        if payload is not None:
            data = json.dumps(payload, separators=(",", ":")).encode()
            headers["content-type"] = "application/json"
        if self.api_key:
            headers["authorization"] = "Bearer " + self.api_key
        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=timeout or self.timeout) as resp:
                body = resp.read()
        except urllib.error.HTTPError as exc:
            body = exc.read()
            msg = _json_error(body) or exc.reason or str(exc)
            raise CoveError(f"{method} {path}: {msg}") from exc
        except urllib.error.URLError as exc:
            raise CoveError(f"{method} {path}: {exc.reason}") from exc
        if not body:
            return {}
        try:
            loaded = json.loads(body)
        except json.JSONDecodeError as exc:
            raise CoveError(f"{method} {path}: invalid json response") from exc
        if not isinstance(loaded, dict):
            raise CoveError(f"{method} {path}: expected json object")
        return loaded


def _fleet_api_key_from_env() -> str:
    return (os.environ.get("COVE_API_KEY") or os.environ.get("COVE_FLEET_TOKEN") or "").strip()


def _query_path(path: str, values: Mapping[str, str]) -> str:
    query = {key: value.strip() for key, value in values.items() if value.strip()}
    if not query:
        return path
    return path + "?" + urllib.parse.urlencode(query)


def _format_seconds(seconds: float) -> str:
    if seconds == int(seconds):
        return f"{int(seconds)}s"
    return f"{seconds}s"


def _json_error(data: bytes) -> str:
    try:
        loaded = json.loads(data)
    except json.JSONDecodeError:
        return data.decode(errors="replace").strip()
    if isinstance(loaded, dict):
        return str(loaded.get("error") or "").strip()
    return ""
