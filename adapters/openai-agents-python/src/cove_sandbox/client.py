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
        self._lease_holder = ""

    @classmethod
    def create_sandbox(
        cls,
        *,
        fleet_url: str | None = None,
        image_ref: str,
        manifest_bundle: str | None = None,
        image_manifest_digest: str | None = None,
        image_digest_ref: str | None = None,
        image_platform: str | None = None,
        required_labels: Mapping[str, str] | None = None,
        required_capabilities: Sequence[str] | str | None = None,
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
        if manifest_bundle and manifest_bundle.strip():
            body["manifest_bundle"] = manifest_bundle.strip()
        if image_manifest_digest and image_manifest_digest.strip():
            body["image_manifest_digest"] = image_manifest_digest.strip()
        if image_digest_ref and image_digest_ref.strip():
            body["image_digest_ref"] = image_digest_ref.strip()
        if image_platform and image_platform.strip():
            body["image_platform"] = image_platform.strip()
        labels = _clean_string_map(required_labels)
        if labels:
            body["required_labels"] = labels
        capabilities = _clean_string_list(required_capabilities)
        if capabilities:
            body["required_capabilities"] = capabilities
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
    def plan_sandbox(
        cls,
        *,
        fleet_url: str | None = None,
        image_ref: str,
        manifest_bundle: str | None = None,
        image_manifest_digest: str | None = None,
        image_digest_ref: str | None = None,
        image_platform: str | None = None,
        required_labels: Mapping[str, str] | None = None,
        required_capabilities: Sequence[str] | str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        limit: int | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        image_ref = image_ref.strip()
        if not image_ref:
            raise ValueError("image_ref is required")
        if limit is not None and limit < 0:
            raise ValueError("limit must be non-negative")
        body: dict[str, object] = {"image_ref": image_ref}
        if namespace:
            body["namespace"] = namespace
        if manifest_bundle and manifest_bundle.strip():
            body["manifest_bundle"] = manifest_bundle.strip()
        if image_manifest_digest and image_manifest_digest.strip():
            body["image_manifest_digest"] = image_manifest_digest.strip()
        if image_digest_ref and image_digest_ref.strip():
            body["image_digest_ref"] = image_digest_ref.strip()
        if image_platform and image_platform.strip():
            body["image_platform"] = image_platform.strip()
        labels = _clean_string_map(required_labels)
        if labels:
            body["required_labels"] = labels
        capabilities = _clean_string_list(required_capabilities)
        if capabilities:
            body["required_capabilities"] = capabilities
        if limit:
            body["limit"] = limit
        seed = cls(
            sandbox_id="placement-plan",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        data = seed._request("POST", "/v1/placements/plan", body, timeout=timeout)
        plan = dict(data)
        for key in ("candidates", "skipped"):
            items = plan.get(key) or []
            if not isinstance(items, list):
                raise CoveError(f"POST /v1/placements/plan: expected {key} list")
            plan[key] = [dict(item) for item in items if isinstance(item, dict)]
        return plan

    @classmethod
    def prepare_image(
        cls,
        *,
        fleet_url: str | None = None,
        image_ref: str,
        source_ref: str | None = None,
        manifest_bundle: str | None = None,
        image_manifest_digest: str | None = None,
        image_digest_ref: str | None = None,
        image_platform: str | None = None,
        required_labels: Mapping[str, str] | None = None,
        required_capabilities: Sequence[str] | str | None = None,
        force: bool = False,
        dry_run: bool = False,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        image_ref = image_ref.strip()
        source_ref = (source_ref or "").strip()
        manifest_bundle = (manifest_bundle or "").strip()
        if not image_ref:
            raise ValueError("image_ref is required")
        if not source_ref and not manifest_bundle:
            raise ValueError("source_ref or manifest_bundle is required")
        body: dict[str, object] = {"image_ref": image_ref}
        if source_ref:
            body["source_ref"] = source_ref
        if namespace:
            body["namespace"] = namespace
        if manifest_bundle:
            body["manifest_bundle"] = manifest_bundle
        if image_manifest_digest and image_manifest_digest.strip():
            body["image_manifest_digest"] = image_manifest_digest.strip()
        if image_digest_ref and image_digest_ref.strip():
            body["image_digest_ref"] = image_digest_ref.strip()
        if image_platform and image_platform.strip():
            body["image_platform"] = image_platform.strip()
        labels = _clean_string_map(required_labels)
        if labels:
            body["required_labels"] = labels
        capabilities = _clean_string_list(required_capabilities)
        if capabilities:
            body["required_capabilities"] = capabilities
        if force:
            body["force"] = True
        if dry_run:
            body["dry_run"] = True
        seed = cls(
            sandbox_id="image-prepare",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        result = seed._request("POST", "/v1/images/prepare", body, timeout=timeout)
        _normalize_dict_list(result, "assignments", "POST /v1/images/prepare")
        _normalize_dict_list(result, "skipped", "POST /v1/images/prepare")
        return result

    @classmethod
    def list_image_preparations(
        cls,
        *,
        fleet_url: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        source_ref: str = "",
        image_ref: str = "",
        image_manifest_digest: str = "",
        offset: int = 0,
        limit: int = 0,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        if offset < 0:
            raise ValueError("image preparation offset must be non-negative")
        if limit < 0:
            raise ValueError("image preparation limit must be non-negative")
        seed = cls(
            sandbox_id="image-preparations",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        query = {
            "namespace": namespace or "",
            "source_ref": source_ref,
            "image_ref": image_ref,
            "image_manifest_digest": image_manifest_digest,
            "offset": str(offset) if offset else "",
            "limit": str(limit) if limit else "",
        }
        data = seed._request("GET", _query_path("/v1/images/preparations", query), timeout=timeout)
        preparations = data.get("preparations") or []
        if not isinstance(preparations, list):
            raise CoveError("GET /v1/images/preparations: expected preparations list")
        page = dict(data)
        page["preparations"] = [dict(item) for item in preparations if isinstance(item, dict)]
        page["count"] = int(page.get("count") or len(page["preparations"]))
        return page

    @classmethod
    def get_image_preparation(
        cls,
        *,
        fleet_url: str | None = None,
        preparation_id: str,
        api_key: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        preparation_id = preparation_id.strip()
        if not preparation_id:
            raise ValueError("image preparation id is required")
        seed = cls(
            sandbox_id="image-preparation",
            fleet_url=fleet_url,
            api_key=api_key,
            timeout=timeout,
        )
        result = seed._request("GET", _image_preparation_path(preparation_id), timeout=timeout)
        _normalize_dict_list(result, "assignments", "GET /v1/images/preparations")
        _normalize_dict_list(result, "skipped", "GET /v1/images/preparations")
        return result

    @classmethod
    def ensure_warm_pool(
        cls,
        *,
        fleet_url: str | None = None,
        image_ref: str,
        size: int,
        name: str = "",
        manifest_bundle: str | None = None,
        image_manifest_digest: str | None = None,
        image_digest_ref: str | None = None,
        image_platform: str | None = None,
        policy: str | None = None,
        required_labels: Mapping[str, str] | None = None,
        required_capabilities: Sequence[str] | str | None = None,
        resources: Mapping[str, object] | None = None,
        args: Sequence[str] | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        name = name.strip()
        image_ref = image_ref.strip()
        if not image_ref:
            raise ValueError("image_ref is required")
        if size < 0:
            raise ValueError("size must be non-negative")
        body: dict[str, object] = {"image_ref": image_ref, "size": size}
        if name:
            body["name"] = name
        if namespace:
            body["namespace"] = namespace
        if manifest_bundle and manifest_bundle.strip():
            body["manifest_bundle"] = manifest_bundle.strip()
        if image_manifest_digest and image_manifest_digest.strip():
            body["image_manifest_digest"] = image_manifest_digest.strip()
        if image_digest_ref and image_digest_ref.strip():
            body["image_digest_ref"] = image_digest_ref.strip()
        if image_platform and image_platform.strip():
            body["image_platform"] = image_platform.strip()
        if policy and policy.strip():
            body["policy"] = policy.strip()
        labels = _clean_string_map(required_labels)
        if labels:
            body["required_labels"] = labels
        capabilities = _clean_string_list(required_capabilities)
        if capabilities:
            body["required_capabilities"] = capabilities
        if resources:
            body["resources"] = dict(resources)
        if args:
            body["args"] = list(args)
        seed = cls(
            sandbox_id="warm-pool",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        result = seed._request("POST", "/v1/warm-pools", body, timeout=timeout)
        return _normalize_warm_pool_result(result, "POST /v1/warm-pools")

    @classmethod
    def list_warm_pools(
        cls,
        *,
        fleet_url: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> list[dict[str, Any]]:
        seed = cls(
            sandbox_id="warm-pools",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        data = seed._request("GET", _query_path("/v1/warm-pools", {"namespace": namespace or ""}), timeout=timeout)
        pools = data.get("warm_pools") or []
        if not isinstance(pools, list):
            raise CoveError("GET /v1/warm-pools: expected warm_pools list")
        return [dict(item) for item in pools if isinstance(item, dict)]

    @classmethod
    def get_warm_pool(
        cls,
        *,
        fleet_url: str | None = None,
        name: str,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        name = name.strip()
        if not name:
            raise ValueError("warm pool name is required")
        seed = cls(
            sandbox_id="warm-pool",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        return seed._request("GET", _warm_pool_path(name), timeout=timeout)

    @classmethod
    def delete_warm_pool(
        cls,
        *,
        fleet_url: str | None = None,
        name: str,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        name = name.strip()
        if not name:
            raise ValueError("warm pool name is required")
        seed = cls(
            sandbox_id="warm-pool",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        result = seed._request("DELETE", _warm_pool_path(name), timeout=timeout)
        _normalize_dict_list(result, "cleanup", "DELETE /v1/warm-pools")
        _normalize_string_list(result, "canceled", "DELETE /v1/warm-pools")
        _normalize_string_list(result, "deferred", "DELETE /v1/warm-pools")
        return result

    @classmethod
    def claim_warm_pool(
        cls,
        *,
        fleet_url: str | None = None,
        name: str,
        command: str | Sequence[str],
        env: Mapping[str, str] | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        name = name.strip()
        if not name:
            raise ValueError("warm pool name is required")
        command_list = ["/bin/zsh", "-lc", command] if isinstance(command, str) else list(command)
        if not command_list or not str(command_list[0]).strip():
            raise ValueError("command is required")
        body: dict[str, object] = {"name": name, "command": command_list}
        if namespace:
            body["namespace"] = namespace
        if env:
            body["env"] = dict(env)
        seed = cls(
            sandbox_id="warm-pool",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        data = seed._request("POST", "/v1/warm-pools/claim", body, timeout=timeout)
        for key in ("slot", "assignment"):
            value = data.get(key) or {}
            if not isinstance(value, dict):
                raise CoveError(f"POST /v1/warm-pools/claim: expected {key} object")
            data[key] = dict(value)
        return data

    @classmethod
    def warm_pool_events(
        cls,
        *,
        fleet_url: str | None = None,
        name: str,
        api_key: str | None = None,
        namespace: str | None = None,
        actor: str = "",
        action: str = "",
        worker_id: str = "",
        assignment_id: str = "",
        offset: int = 0,
        limit: int = 0,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        name = name.strip()
        if not name:
            raise ValueError("warm pool name is required")
        if offset < 0:
            raise ValueError("warm pool events offset must be non-negative")
        if limit < 0:
            raise ValueError("warm pool events limit must be non-negative")
        seed = cls(
            sandbox_id="warm-pool",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        query = {
            "actor": actor,
            "action": action,
            "worker_id": worker_id,
            "assignment_id": assignment_id,
            "offset": str(offset) if offset else "",
            "limit": str(limit) if limit else "",
        }
        data = seed._request("GET", _query_path(_warm_pool_path(name, "events"), query), timeout=timeout)
        events = data.get("events") or []
        if not isinstance(events, list):
            raise CoveError("GET warm pool events: expected events list")
        page = dict(data)
        page["events"] = [dict(item) for item in events if isinstance(item, dict)]
        page["count"] = int(page.get("count") or len(page["events"]))
        return page

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
        offset: int | None = None,
        limit: int | None = None,
        timeout: float = 30.0,
    ) -> list[dict[str, Any]]:
        data = cls.list_sandboxes_page(
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            status=status,
            worker_id=worker_id,
            image_ref=image_ref,
            offset=offset,
            limit=limit,
            timeout=timeout,
        )
        return [dict(item) for item in data["sandboxes"] if isinstance(item, dict)]

    @classmethod
    def list_sandboxes_page(
        cls,
        *,
        fleet_url: str | None = None,
        api_key: str | None = None,
        namespace: str | None = None,
        status: str | None = None,
        worker_id: str | None = None,
        image_ref: str | None = None,
        offset: int | None = None,
        limit: int | None = None,
        timeout: float = 30.0,
    ) -> dict[str, Any]:
        seed = cls(
            sandbox_id="list",
            fleet_url=fleet_url,
            api_key=api_key,
            namespace=namespace,
            timeout=timeout,
        )
        return seed.list_page(
            status=status,
            worker_id=worker_id,
            image_ref=image_ref,
            offset=offset,
            limit=limit,
        )

    def list(
        self,
        *,
        status: str | None = None,
        worker_id: str | None = None,
        image_ref: str | None = None,
        offset: int | None = None,
        limit: int | None = None,
    ) -> list[dict[str, Any]]:
        data = self.list_page(status=status, worker_id=worker_id, image_ref=image_ref, offset=offset, limit=limit)
        return [dict(item) for item in data["sandboxes"] if isinstance(item, dict)]

    def list_page(
        self,
        *,
        status: str | None = None,
        worker_id: str | None = None,
        image_ref: str | None = None,
        offset: int | None = None,
        limit: int | None = None,
    ) -> dict[str, Any]:
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
        if offset is not None:
            if offset < 0:
                raise ValueError("offset must be non-negative")
            if offset > 0:
                query["offset"] = str(offset)
        path = _query_path("/v1/sandboxes", query)
        data = self._request("GET", path, timeout=self.timeout)
        sandboxes = data.get("sandboxes") or []
        if not isinstance(sandboxes, list):
            raise CoveError("GET /v1/sandboxes: expected sandboxes list")
        page = dict(data)
        page["sandboxes"] = [dict(item) for item in sandboxes if isinstance(item, dict)]
        page["count"] = int(page.get("count") or len(page["sandboxes"]))
        return page

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
        self._request("POST", self._sandbox_path("start"), self._lease_body(), timeout=self.timeout)

    def stop(self, *, force: bool = False) -> None:
        del force
        self._request("POST", self._sandbox_path("stop"), self._lease_body(), timeout=self.timeout)

    def restart(self) -> None:
        self._request("POST", self._sandbox_path("restart"), self._lease_body(), timeout=self.timeout)

    def lease(self, *, holder: str = "", ttl: float | None = None) -> dict[str, Any]:
        if ttl is not None and ttl < 0:
            raise ValueError("ttl must not be negative")
        body: dict[str, object] = {}
        if holder.strip():
            body["holder"] = holder.strip()
        if ttl is not None:
            body["ttl"] = _format_seconds(ttl)
        data = self._request("POST", self._sandbox_path("lease"), body, timeout=self.timeout)
        lease = data.get("lease") or {}
        if isinstance(lease, dict):
            self._lease_holder = str(lease.get("holder") or "").strip()
        return data

    def release_lease(self, *, holder: str = "") -> dict[str, Any]:
        path = self._sandbox_path("lease")
        holder = holder.strip() or self._lease_holder
        if holder:
            path = _query_path(path, {"holder": holder})
        data = self._request("DELETE", path, timeout=self.timeout)
        if holder == self._lease_holder:
            self._lease_holder = ""
        return data

    def metering(self) -> dict[str, Any]:
        return self._request("GET", self._sandbox_path("metering"), timeout=self.timeout)

    def list_metering(self, *, sandbox_id: str | None = None) -> dict[str, Any]:
        query = {"namespace": self.namespace, "sandbox_id": sandbox_id or ""}
        return self._request("GET", _query_path("/v1/metering/sandboxes", query), timeout=self.timeout)

    def events(
        self,
        *,
        actor: str = "",
        action: str = "",
        offset: int = 0,
        limit: int = 0,
    ) -> dict[str, Any]:
        if offset < 0:
            raise ValueError("sandbox events offset must be non-negative")
        if limit < 0:
            raise ValueError("sandbox events limit must be non-negative")
        query = {
            "actor": actor,
            "action": action,
            "offset": str(offset) if offset else "",
            "limit": str(limit) if limit else "",
        }
        data = self._request("GET", _query_path(self._sandbox_path("events"), query), timeout=self.timeout)
        events = data.get("events") or []
        if not isinstance(events, list):
            raise CoveError("GET sandbox events: expected events list")
        page = dict(data)
        page["events"] = [dict(item) for item in events if isinstance(item, dict)]
        page["count"] = int(page.get("count") or len(page["events"]))
        return page

    def reports(
        self,
        *,
        role: str = "",
        status: str = "",
        offset: int = 0,
        limit: int = 0,
    ) -> dict[str, Any]:
        if offset < 0:
            raise ValueError("sandbox reports offset must be non-negative")
        if limit < 0:
            raise ValueError("sandbox reports limit must be non-negative")
        query = {
            "role": role,
            "status": status,
            "offset": str(offset) if offset else "",
            "limit": str(limit) if limit else "",
        }
        data = self._request("GET", _query_path(self._sandbox_path("reports"), query), timeout=self.timeout)
        reports = data.get("reports") or []
        if not isinstance(reports, list):
            raise CoveError("GET sandbox reports: expected reports list")
        page = dict(data)
        page["reports"] = [dict(item) for item in reports if isinstance(item, dict)]
        page["count"] = int(page.get("count") or len(page["reports"]))
        return page

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
                **self._lease_body(),
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
        payload.update(self._lease_body())
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
        path = self._sandbox_path()
        if self._lease_holder:
            path = _query_path(path, {"holder": self._lease_holder})
        self._request("DELETE", path, timeout=max(self.timeout, 120))

    def _lease_body(self) -> dict[str, object]:
        if not self._lease_holder:
            return {}
        return {"holder": self._lease_holder}

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


def _clean_string_list(values: Sequence[str] | str | None) -> list[str]:
    if not values:
        return []
    if isinstance(values, str):
        values = (values,)
    seen: set[str] = set()
    out: list[str] = []
    for item in values:
        value = str(item).strip()
        if not value or value in seen:
            continue
        seen.add(value)
        out.append(value)
    return out


def _clean_string_map(values: Mapping[str, str] | None) -> dict[str, str]:
    if not values:
        return {}
    out: dict[str, str] = {}
    for key, value in values.items():
        key = str(key).strip()
        if not key:
            continue
        out[key] = str(value).strip()
    return out


def _normalize_warm_pool_result(data: dict[str, Any], endpoint: str) -> dict[str, Any]:
    result = dict(data)
    pool = result.get("pool") or {}
    if not isinstance(pool, dict):
        raise CoveError(f"{endpoint}: expected pool object")
    result["pool"] = dict(pool)
    _normalize_dict_list(result, "created", endpoint)
    _normalize_dict_list(result, "cleanup", endpoint)
    _normalize_string_list(result, "canceled", endpoint)
    return result


def _normalize_dict_list(data: dict[str, Any], key: str, endpoint: str) -> None:
    items = data.get(key) or []
    if not isinstance(items, list):
        raise CoveError(f"{endpoint}: expected {key} list")
    data[key] = [dict(item) for item in items if isinstance(item, dict)]


def _normalize_string_list(data: dict[str, Any], key: str, endpoint: str) -> None:
    items = data.get(key) or []
    if not isinstance(items, list):
        raise CoveError(f"{endpoint}: expected {key} list")
    data[key] = [str(item) for item in items]


def _warm_pool_path(name: str, action: str = "") -> str:
    path = "/v1/warm-pools/" + urllib.parse.quote(name, safe="")
    if action:
        path += "/" + urllib.parse.quote(action, safe="")
    return path


def _image_preparation_path(preparation_id: str) -> str:
    return "/v1/images/preparations/" + urllib.parse.quote(preparation_id, safe="")


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
