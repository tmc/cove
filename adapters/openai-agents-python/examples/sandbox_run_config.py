from __future__ import annotations

import asyncio

from agents import RunConfig, Runner
from agents.sandbox import SandboxAgent, SandboxRunConfig

from cove_sandbox import CoveSandboxClient, CoveSandboxClientOptions


async def main() -> None:
    agent = SandboxAgent(
        name="macOS sandbox",
        instructions="Use the cove-backed macOS VM for shell and file work.",
    )
    run_config = RunConfig(
        sandbox=SandboxRunConfig(
            client=CoveSandboxClient(),
            options=CoveSandboxClientOptions(
                parent="macos-base",
                name="openai-agents-eval-001",
                gui=False,
                delete_on_close=True,
            ),
        )
    )
    result = await Runner.run(agent, "Run sw_vers and summarize the OS version.", run_config=run_config)
    print(result.final_output)


if __name__ == "__main__":
    asyncio.run(main())
