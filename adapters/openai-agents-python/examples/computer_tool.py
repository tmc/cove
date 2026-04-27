from agents import Agent, ComputerTool, Runner

from cove_sandbox import CoveSandbox


def main() -> None:
    sandbox = CoveSandbox(vm="macos-eval")
    agent = Agent(
        name="macOS operator",
        instructions="Use the macOS VM and report concise observations.",
        tools=[ComputerTool(sandbox.computer())],
    )
    result = Runner.run_sync(agent, "What is visible on the VM desktop?")
    print(result.final_output)


if __name__ == "__main__":
    main()
