from __future__ import annotations

import sys
import types

from cove_sandbox import sandbox_run_config
import cove_sandbox.backend as backend


def test_sandbox_run_config_factory(monkeypatch) -> None:
    fake_agents = types.ModuleType("agents")
    fake_sandbox = types.ModuleType("agents.sandbox")

    class FakeRunConfig:
        def __init__(self, *, sandbox):
            self.sandbox = sandbox

    class FakeSandboxRunConfig:
        def __init__(self, *, client, options):
            self.client = client
            self.options = options

    fake_agents.RunConfig = FakeRunConfig
    fake_sandbox.SandboxRunConfig = FakeSandboxRunConfig
    monkeypatch.setitem(sys.modules, "agents", fake_agents)
    monkeypatch.setitem(sys.modules, "agents.sandbox", fake_sandbox)
    monkeypatch.setattr(backend, "_AGENTS_AVAILABLE", True)

    cfg = sandbox_run_config(parent="macos-base", name="eval-001", delete_on_close=True)
    assert isinstance(cfg, FakeRunConfig)
    assert isinstance(cfg.sandbox, FakeSandboxRunConfig)
    assert cfg.sandbox.options.parent == "macos-base"
    assert cfg.sandbox.options.name == "eval-001"
    assert cfg.sandbox.options.delete_on_close is True
    assert cfg.sandbox.options.vm is None
