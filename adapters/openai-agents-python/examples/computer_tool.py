import os

from agents import Agent, ComputerTool, Runner

from cove_sandbox import CoveSandbox


def main() -> None:
    parent = os.environ.get("COVE_PARENT_VM", "macos-base")
    name = os.environ.get("COVE_CHILD_VM", "openai-agents-eval-001")
    cove = os.environ.get("COVE_BIN", "cove")
    with CoveSandbox.from_fork(parent=parent, name=name, cove=cove) as sandbox:
        sandbox.start(gui=True)
        sandbox.wait_ready(timeout=120)
        agent = Agent(
            name="macOS operator",
            instructions="Use the macOS VM and report concise observations.",
            tools=[ComputerTool(sandbox.computer())],
        )
        result = Runner.run_sync(agent, "What is visible on the VM desktop?")
        print(result.final_output)


if __name__ == "__main__":
    main()
