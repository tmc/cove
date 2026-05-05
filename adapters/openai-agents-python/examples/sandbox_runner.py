from __future__ import annotations

import asyncio
import os

from cove_sandbox import sandbox_run_config


async def main() -> None:
    parent = os.environ.get("COVE_PARENT_VM", "macos-base")
    child = os.environ.get("COVE_CHILD_VM", "openai-agents-eval-001")
    task = os.environ.get("COVE_TASK", "Run sw_vers and summarize the OS version.")

    if os.environ.get("COVE_DRY_RUN") == "1":
        print(
            "dry run: would call Runner.run("
            f"task={task!r}, parent={parent!r}, name={child!r}, gui=False, delete_on_close=True)"
        )
        return

    from agents import Runner
    from agents.sandbox import SandboxAgent

    run_config = sandbox_run_config(
        parent=parent,
        name=child,
        gui=False,
        delete_on_close=True,
    )

    agent = SandboxAgent(
        name="macOS workspace",
        instructions="Use the cove-backed VM for shell and file work.",
    )
    result = await Runner.run(agent, task, run_config=run_config)
    print(result.final_output)


if __name__ == "__main__":
    asyncio.run(main())
