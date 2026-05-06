from __future__ import annotations

import argparse
import sys

def main() -> None:
    from agents import Agent, ComputerTool, Runner
    from cove_sandbox import CoveClient, CoveComputer

    parser = argparse.ArgumentParser()
    parser.add_argument("--vm", required=True)
    parser.add_argument("--task", required=True)
    parser.add_argument("--max-steps", type=int, default=25)
    parser.add_argument("--screenshot-dir")
    parser.add_argument("--events-jsonl")
    args = parser.parse_args()

    del args.max_steps, args.screenshot_dir, args.events_jsonl

    client = CoveClient(vm=args.vm)
    computer = CoveComputer(client)
    agent = Agent(
        name="macOS operator",
        instructions="Use the cove-backed macOS VM and report concise observations.",
        tools=[ComputerTool(computer)],
    )
    result = Runner.run_sync(agent, args.task)
    print(result.final_output)


if __name__ == "__main__":
    try:
        main()
    except ImportError as exc:
        raise SystemExit("install OpenAI Agents SDK: pip install -e adapters/openai-agents-python[agents]") from exc
    except KeyboardInterrupt:
        sys.exit(130)
