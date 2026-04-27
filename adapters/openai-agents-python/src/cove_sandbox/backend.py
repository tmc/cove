from __future__ import annotations

import asyncio
import base64
import io
import shlex
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal

from .client import CoveClient, CoveError

try:  # pragma: no cover - covered when openai-agents is installed.
    from agents.sandbox.manifest import Manifest
    from agents.sandbox.session import SandboxSession, SandboxSessionState
    from agents.sandbox.session.base_sandbox_session import BaseSandboxSession
    from agents.sandbox.session.sandbox_client import BaseSandboxClient, BaseSandboxClientOptions
    from agents.sandbox.snapshot import SnapshotBase, SnapshotSpec, resolve_snapshot
    from agents.sandbox.types import ExecResult as AgentExecResult
    from agents.sandbox.types import ExposedPortEndpoint, User
except Exception:  # noqa: BLE001
    Manifest = None  # type: ignore[assignment]
    SnapshotBase = object  # type: ignore[assignment,misc]
    SnapshotSpec = object  # type: ignore[assignment,misc]
    User = object  # type: ignore[assignment,misc]
    ExposedPortEndpoint = None  # type: ignore[assignment]
    AgentExecResult = None  # type: ignore[assignment]
    _AGENTS_AVAILABLE = False

    class BaseSandboxClientOptions:  # type: ignore[no-redef]
        pass

    class SandboxSessionState:  # type: ignore[no-redef]
        pass

    class BaseSandboxSession:  # type: ignore[no-redef]
        pass

    class BaseSandboxClient:  # type: ignore[no-redef]
        def _wrap_session(self, inner: Any, *, instrumentation: Any | None = None) -> Any:
            del instrumentation
            return inner

    class SandboxSession:  # type: ignore[no-redef]
        pass

    def resolve_snapshot(snapshot: object, snapshot_id: str) -> object:  # type: ignore[no-redef]
        del snapshot_id
        return snapshot

else:
    _AGENTS_AVAILABLE = True


_DEFAULT_WORKSPACE_PREFIX = "/tmp/cove-sandbox-"


if _AGENTS_AVAILABLE:

    class CoveSandboxClientOptions(BaseSandboxClientOptions):
        type: Literal["cove"] = "cove"
        vm: str | None = None
        parent: str | None = None
        name: str | None = None
        cove: str = "cove"
        token: str | None = None
        socket_path: str | None = None
        workspace_root: str | None = None
        start: bool = True
        gui: bool = False
        stop_on_close: bool = True
        delete_on_close: bool = False
        wait_ready_timeout: float = 120.0
        extra_run_args: tuple[str, ...] = ()

        def __init__(
            self,
            vm: str | None = None,
            *,
            parent: str | None = None,
            name: str | None = None,
            cove: str = "cove",
            token: str | None = None,
            socket_path: str | None = None,
            workspace_root: str | None = None,
            start: bool = True,
            gui: bool = False,
            stop_on_close: bool = True,
            delete_on_close: bool = False,
            wait_ready_timeout: float = 120.0,
            extra_run_args: tuple[str, ...] = (),
            type: Literal["cove"] = "cove",
        ) -> None:
            super().__init__(
                type=type,
                vm=vm,
                parent=parent,
                name=name,
                cove=cove,
                token=token,
                socket_path=socket_path,
                workspace_root=workspace_root,
                start=start,
                gui=gui,
                stop_on_close=stop_on_close,
                delete_on_close=delete_on_close,
                wait_ready_timeout=wait_ready_timeout,
                extra_run_args=extra_run_args,
            )

else:

    @dataclass(frozen=True)
    class CoveSandboxClientOptions(BaseSandboxClientOptions):  # type: ignore[no-redef]
        vm: str | None = None
        parent: str | None = None
        name: str | None = None
        cove: str = "cove"
        token: str | None = None
        socket_path: str | None = None
        workspace_root: str | None = None
        start: bool = True
        gui: bool = False
        stop_on_close: bool = True
        delete_on_close: bool = False
        wait_ready_timeout: float = 120.0
        extra_run_args: tuple[str, ...] = ()
        type: str = "cove"

        def model_dump(self, mode: str = "python") -> dict[str, object]:
            del mode
            return dict(self.__dict__)


if _AGENTS_AVAILABLE:

    class CoveSandboxSessionState(SandboxSessionState):
        type: Literal["cove"] = "cove"
        vm: str
        cove: str = "cove"
        token: str | None = None
        socket_path: str | None = None
        workspace_root: str
        stop_on_close: bool = True
        delete_on_close: bool = False
        owned: bool = False

else:

    @dataclass
    class CoveSandboxSessionState(SandboxSessionState):  # type: ignore[no-redef]
        vm: str
        workspace_root: str
        cove: str = "cove"
        token: str | None = None
        socket_path: str | None = None
        stop_on_close: bool = True
        delete_on_close: bool = False
        owned: bool = False
        type: str = "cove"
        session_id: uuid.UUID = field(default_factory=uuid.uuid4)
        snapshot: object | None = None
        manifest: object | None = None
        exposed_ports: tuple[int, ...] = ()
        workspace_root_ready: bool = False

        @classmethod
        def model_validate(cls, payload: dict[str, object]) -> "CoveSandboxSessionState":
            data = dict(payload)
            if isinstance(data.get("session_id"), str):
                data["session_id"] = uuid.UUID(str(data["session_id"]))
            return cls(**data)  # type: ignore[arg-type]

        def model_dump(self, mode: str = "python") -> dict[str, object]:
            del mode
            data = dict(self.__dict__)
            data["session_id"] = str(self.session_id)
            return data


class CoveSandboxSession(BaseSandboxSession):
    state: CoveSandboxSessionState

    def __init__(
        self,
        *,
        state: CoveSandboxSessionState,
        start: bool = True,
        gui: bool = False,
        wait_ready_timeout: float = 120.0,
        extra_run_args: tuple[str, ...] = (),
    ) -> None:
        self.state = state
        self._client = CoveClient(
            vm=state.vm,
            socket_path=state.socket_path,
            cove=state.cove,
            token=state.token,
        )
        self._start = start
        self._gui = gui
        self._wait_ready_timeout = wait_ready_timeout
        self._extra_run_args = extra_run_args
        self._running = False
        if _AGENTS_AVAILABLE:
            self._set_start_state_preserved(False, system=True)

    @classmethod
    def from_state(
        cls,
        state: CoveSandboxSessionState,
        *,
        start: bool = True,
        gui: bool = False,
        wait_ready_timeout: float = 120.0,
        extra_run_args: tuple[str, ...] = (),
    ) -> "CoveSandboxSession":
        return cls(
            state=state,
            start=start,
            gui=gui,
            wait_ready_timeout=wait_ready_timeout,
            extra_run_args=extra_run_args,
        )

    async def _ensure_backend_started(self) -> None:
        if self._start:
            await asyncio.to_thread(
                self._client.start,
                gui=self._gui,
                extra_args=self._extra_run_args,
            )
        await asyncio.to_thread(self._client.wait_ready, timeout=self._wait_ready_timeout)
        self._running = True

    async def _prepare_backend_workspace(self) -> None:
        await self.mkdir(self.state.workspace_root, parents=True)

    async def _after_start(self) -> None:
        self._running = True

    async def _after_start_failed(self) -> None:
        self._running = False

    async def _shutdown_backend(self) -> None:
        if not self.state.stop_on_close:
            return
        try:
            await asyncio.to_thread(self._client.stop)
        finally:
            self._running = False

    async def _exec_internal(self, *command: str | Path, timeout: float | None = None) -> Any:
        result = await asyncio.to_thread(
            self._client.exec,
            [str(part) for part in command],
            timeout=timeout,
        )
        stdout = result.stdout.encode()
        stderr = result.stderr.encode()
        if AgentExecResult is None:
            return result
        return AgentExecResult(stdout=stdout, stderr=stderr, exit_code=result.exit_code)

    async def read(self, path: Path, *, user: str | User | None = None) -> io.IOBase:
        del user
        data = await asyncio.to_thread(self._client.read_file, self._sandbox_path(path))
        return io.BytesIO(data)

    async def write(
        self,
        path: Path,
        data: io.IOBase,
        *,
        user: str | User | None = None,
    ) -> None:
        del user
        payload = data.read()
        if isinstance(payload, str):
            payload = payload.encode()
        elif not isinstance(payload, bytes):
            payload = bytes(payload)
        await asyncio.to_thread(self._client.write_file, self._sandbox_path(path), payload)

    async def running(self) -> bool:
        try:
            await asyncio.to_thread(self._client.control, {"type": "agent-ping"})
        except Exception:  # noqa: BLE001
            return False
        return True

    async def mkdir(
        self,
        path: Path | str,
        *,
        parents: bool = False,
        user: str | User | None = None,
    ) -> None:
        del user
        args = "-p " if parents else ""
        await self._exec_checked(f"mkdir {args}{shlex.quote(self._sandbox_path(path))}")

    async def rm(
        self,
        path: Path | str,
        *,
        recursive: bool = False,
        user: str | User | None = None,
    ) -> None:
        del user
        args = "-rf" if recursive else "-f"
        await self._exec_checked(f"rm {args} {shlex.quote(self._sandbox_path(path))}")

    async def persist_workspace(self) -> io.IOBase:
        root = self.state.workspace_root
        result = await asyncio.to_thread(
            self._client.exec,
            f"/usr/bin/tar -C {shlex.quote(root)} -cf - . | /usr/bin/base64",
            timeout=600,
        )
        result.check_returncode()
        return io.BytesIO(base64.b64decode(result.stdout))

    async def hydrate_workspace(self, data: io.IOBase) -> None:
        tmp = f"/tmp/cove-sandbox-hydrate-{uuid.uuid4().hex}.tar"
        payload = data.read()
        if isinstance(payload, str):
            payload = payload.encode()
        await asyncio.to_thread(self._client.write_file, tmp, payload)
        try:
            await self._exec_checked(
                "mkdir -p {root} && /usr/bin/tar -C {root} -xf {tmp}".format(
                    root=shlex.quote(self.state.workspace_root),
                    tmp=shlex.quote(tmp),
                ),
                timeout=600,
            )
        finally:
            try:
                await self._exec_checked(f"rm -f {shlex.quote(tmp)}")
            except Exception:  # noqa: BLE001
                pass

    async def _resolve_exposed_port(self, port: int) -> Any:
        if ExposedPortEndpoint is None:
            return ("127.0.0.1", port)
        return ExposedPortEndpoint(host="127.0.0.1", port=port, tls=False)

    def normalize_path(self, path: Path | str, *, for_write: bool = False) -> Path:
        del for_write
        return Path(self._sandbox_path(path))

    async def _exec_checked(self, command: str, *, timeout: float | None = None) -> None:
        result = await asyncio.to_thread(self._client.exec, command, timeout=timeout)
        result.check_returncode()

    def _sandbox_path(self, path: Path | str) -> str:
        p = Path(str(path))
        if p.is_absolute():
            return p.as_posix()
        return (Path(self.state.workspace_root) / p).as_posix()


class CoveSandboxClient(BaseSandboxClient):
    backend_id = "cove"
    supports_default_options = False

    async def create(
        self,
        *,
        snapshot: SnapshotSpec | SnapshotBase | None = None,
        manifest: Any | None = None,
        options: CoveSandboxClientOptions | None = None,
    ) -> Any:
        opts = options or CoveSandboxClientOptions()
        session_id = uuid.uuid4()
        vm, owned = await self._resolve_vm(opts, session_id=session_id)
        workspace_root = opts.workspace_root or f"{_DEFAULT_WORKSPACE_PREFIX}{session_id.hex[:12]}"
        manifest = _manifest_with_root(manifest, workspace_root)
        state = CoveSandboxSessionState(
            session_id=session_id,
            snapshot=resolve_snapshot(snapshot, str(session_id)),
            manifest=manifest,
            vm=vm,
            cove=opts.cove,
            token=opts.token,
            socket_path=opts.socket_path,
            workspace_root=workspace_root,
            stop_on_close=opts.stop_on_close,
            delete_on_close=opts.delete_on_close,
            owned=owned,
        )
        inner = CoveSandboxSession.from_state(
            state,
            start=opts.start,
            gui=opts.gui,
            wait_ready_timeout=opts.wait_ready_timeout,
            extra_run_args=opts.extra_run_args,
        )
        return self._wrap_session(inner)

    async def delete(self, session: Any) -> Any:
        inner = getattr(session, "_inner", session)
        if not isinstance(inner, CoveSandboxSession):
            raise TypeError("CoveSandboxClient.delete expects a CoveSandboxSession")
        if inner.state.delete_on_close and inner.state.owned:
            await asyncio.to_thread(inner._client.delete_vm, inner.state.vm)
        return session

    async def resume(self, state: SandboxSessionState) -> Any:
        if not isinstance(state, CoveSandboxSessionState):
            raise TypeError("CoveSandboxClient.resume expects a CoveSandboxSessionState")
        inner = CoveSandboxSession.from_state(state, start=False)
        if _AGENTS_AVAILABLE:
            inner._set_start_state_preserved(True, system=True)
        return self._wrap_session(inner)

    def deserialize_session_state(self, payload: dict[str, object]) -> SandboxSessionState:
        return CoveSandboxSessionState.model_validate(payload)  # type: ignore[return-value]

    async def _resolve_vm(
        self,
        opts: CoveSandboxClientOptions,
        *,
        session_id: uuid.UUID,
    ) -> tuple[str, bool]:
        if opts.parent:
            name = opts.name or f"{opts.parent}-{session_id.hex[:8]}"
            client = CoveClient(vm=name, cove=opts.cove, token=opts.token)
            await asyncio.to_thread(client.fork, opts.parent, name)
            return name, True
        if opts.vm:
            return opts.vm, False
        raise CoveError("CoveSandboxClientOptions requires vm or parent")


def sandbox_run_config(
    *,
    vm: str | None = None,
    parent: str | None = None,
    name: str | None = None,
    **kwargs: object,
) -> Any:
    if not _AGENTS_AVAILABLE:
        raise CoveError("sandbox_run_config requires the openai-agents package")
    from agents import RunConfig
    from agents.sandbox import SandboxRunConfig

    return RunConfig(
        sandbox=SandboxRunConfig(
            client=CoveSandboxClient(),
            options=CoveSandboxClientOptions(vm=vm, parent=parent, name=name, **kwargs),
        )
    )


def _manifest_with_root(manifest: Any | None, root: str) -> Any:
    if Manifest is None:
        return manifest
    default_root = str(Manifest.model_fields["root"].default)
    if manifest is None:
        return Manifest(root=root)
    if getattr(manifest, "root", None) == default_root:
        return manifest.model_copy(update={"root": root}, deep=True)
    return manifest
