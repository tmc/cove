"""Send a bounded command sequence to the currently displayed guest session."""

import argparse
from pathlib import Path
import time
import urllib.request


def send(root, commands, delay=.2):
    url = (root / "viewer.url").read_text().strip()
    with urllib.request.urlopen(url + "/frame", timeout=3) as response:
        session = response.headers.get("X-Agent-Session")
        stamp = float(response.headers.get("X-Frame-Time", "0"))
        response.read()
    if not session or time.time() - stamp > 5:
        raise ValueError("guest display is unavailable or stale")
    for command in commands:
        request = urllib.request.Request(url + "/input", data=command.encode(),
                                         headers={"X-Session": session})
        with urllib.request.urlopen(request, timeout=3) as response:
            response.read()
        time.sleep(delay)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path, help="receiver state directory")
    parser.add_argument("commands", nargs="+")
    args = parser.parse_args()
    send(args.root, args.commands)


if __name__ == "__main__":
    main()
