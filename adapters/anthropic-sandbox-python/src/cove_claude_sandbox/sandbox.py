from __future__ import annotations

import os
import time
from collections.abc import Sequence
from typing import Any

from .actions import AnthropicToolDispatcher
from .client import CoveClient, CoveError, ExecResult
from .loop import AnthropicAgentLoop, AnthropicSandboxResult


class AnthropicSandbox:
    def __init__(
        self,
        *,
        vm: str | None = None,
        socket_path: str | None = None,
        cove: str = "cove",
        cove_bin: str | None = None,
        token: str | None = None,
        width: int = 1024,
        height: int = 768,
    ) -> None:
        self.client = CoveClient(vm=vm, socket_path=socket_path, cove=cove_bin or cove, token=token)
        self.width = width
        self.height = height
        self._process: Any = None

    @classmethod
    def from_env(cls) -> "AnthropicSandbox":
        sandbox = cls.__new__(cls)
        sandbox.client = CoveClient.from_env()
        sandbox.width = int(os.environ.get("COVE_DISPLAY_WIDTH", "1024"))
        sandbox.height = int(os.environ.get("COVE_DISPLAY_HEIGHT", "768"))
        sandbox._process = None
        return sandbox

    @classmethod
    def from_fork(
        cls,
        *,
        parent: str,
        child: str | None = None,
        name: str | None = None,
        cove: str = "cove",
        cove_bin: str | None = None,
        token: str | None = None,
        width: int = 1024,
        height: int = 768,
    ) -> "AnthropicSandbox":
        vm = child or name
        if not vm:
            raise ValueError("child or name is required")
        client = CoveClient(vm=vm, cove=cove_bin or cove, token=token)
        client.fork(parent, vm)
        sandbox = cls.__new__(cls)
        sandbox.client = client
        sandbox.width = width
        sandbox.height = height
        sandbox._process = None
        return sandbox

    @property
    def vm(self) -> str | None:
        return self.client.vm

    def start(self, *, gui: bool = True, extra_args: Sequence[str] = ()) -> None:
        self._process = self.client.start(gui=gui, extra_args=extra_args)

    def wait_ready(self, timeout: float = 120.0) -> None:
        self.client.wait_ready(timeout=timeout)

    def first_frame(self, *, gui: bool = True, timeout: float = 120.0) -> bytes:
        if self._process is None:
            self.start(gui=gui)
        deadline = time.monotonic() + timeout
        last: Exception | None = None
        while time.monotonic() < deadline:
            try:
                return self.client.screenshot(fmt="png")
            except Exception as exc:  # noqa: BLE001
                last = exc
                time.sleep(1)
        if last is None:
            raise CoveError("timed out waiting for first frame")
        raise CoveError(f"timed out waiting for first frame: {last}")

    def run(
        self,
        prompt: str,
        *,
        model: str | None = None,
        max_iterations: int = 20,
        max_tokens: int = 4096,
        anthropic_client: Any = None,
        system: str | Sequence[dict[str, Any]] | None = None,
        thinking: dict[str, Any] | None = None,
    ) -> AnthropicSandboxResult:
        if anthropic_client is None:
            import anthropic

            anthropic_client = anthropic.Anthropic()
        dispatcher = AnthropicToolDispatcher(self.client, width=self.width, height=self.height)
        loop = AnthropicAgentLoop(
            anthropic_client=anthropic_client,
            dispatcher=dispatcher,
            model=model or os.environ.get("ANTHROPIC_MODEL", "claude-opus-4-7"),
            max_tokens=max_tokens,
            thinking=thinking,
        )
        return loop.run(prompt, max_iterations=max_iterations, system=system)

    def stop(self, *, force: bool = False) -> None:
        self.client.stop(force=force)

    def exec(self, command: str | Sequence[str], **kwargs: object) -> ExecResult:
        return self.client.exec(command, **kwargs)

    def __enter__(self) -> "AnthropicSandbox":
        return self

    def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
        del exc_type, exc, tb
        try:
            self.stop()
        except Exception:  # noqa: BLE001
            pass
