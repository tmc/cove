from __future__ import annotations

from collections.abc import Sequence

from .client import CoveClient, ExecResult
from .computer import CoveComputer


class CoveSandbox:
    def __init__(
        self,
        *,
        vm: str | None = None,
        socket_path: str | None = None,
        cove: str = "cove",
        token: str | None = None,
    ) -> None:
        self.client = CoveClient(vm=vm, socket_path=socket_path, cove=cove, token=token)

    @classmethod
    def from_env(cls) -> "CoveSandbox":
        sandbox = cls.__new__(cls)
        sandbox.client = CoveClient.from_env()
        return sandbox

    @classmethod
    def from_fork(
        cls,
        *,
        parent: str,
        name: str,
        cove: str = "cove",
        token: str | None = None,
    ) -> "CoveSandbox":
        client = CoveClient(vm=name, cove=cove, token=token)
        client.fork(parent, name)
        sandbox = cls.__new__(cls)
        sandbox.client = client
        return sandbox

    @property
    def vm(self) -> str | None:
        return self.client.vm

    def start(self, *, gui: bool = False, extra_args: Sequence[str] = ()) -> None:
        self.client.start(gui=gui, extra_args=extra_args)

    def wait_ready(self, timeout: float = 120.0) -> None:
        self.client.wait_ready(timeout=timeout)

    def stop(self, *, force: bool = False) -> None:
        self.client.stop(force=force)

    def exec(self, command: str | Sequence[str], **kwargs: object) -> ExecResult:
        return self.client.exec(command, **kwargs)

    def computer(self, *, width: int = 1024, height: int = 768) -> CoveComputer:
        return CoveComputer(self.client, width=width, height=height)

    def __enter__(self) -> "CoveSandbox":
        return self

    def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
        del exc_type, exc, tb
        try:
            self.stop()
        except Exception:  # noqa: BLE001
            pass
