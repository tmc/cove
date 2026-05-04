from __future__ import annotations

import argparse
import os
from pathlib import Path

from cove_claude_sandbox import AnthropicSandbox


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("prompt", nargs="?", default="Take a screenshot and describe what is visible.")
    parser.add_argument("--parent", default=os.environ.get("COVE_PARENT_VM"))
    parser.add_argument("--child", default=os.environ.get("COVE_CHILD_VM", "claude-sandbox-smoke"))
    parser.add_argument("--cove-bin", default=os.environ.get("COVE_BIN", "cove"))
    parser.add_argument("--model", default=os.environ.get("ANTHROPIC_MODEL", "claude-opus-4-7"))
    parser.add_argument("--first-frame-only", action="store_true")
    parser.add_argument("--frame-out", default=os.environ.get("COVE_FIRST_FRAME_OUT", "/tmp/cove-claude-first-frame.png"))
    args = parser.parse_args()

    if not args.parent:
        raise SystemExit("set COVE_PARENT_VM or pass --parent")

    sandbox = AnthropicSandbox.from_fork(parent=args.parent, child=args.child, cove_bin=args.cove_bin)
    try:
        if args.first_frame_only:
            data = sandbox.first_frame(gui=True)
            Path(args.frame_out).write_bytes(data)
            print(args.frame_out)
            return
        result = sandbox.run(args.prompt, model=args.model)
        print(result.final_text)
    finally:
        sandbox.stop(force=args.first_frame_only)


if __name__ == "__main__":
    main()
