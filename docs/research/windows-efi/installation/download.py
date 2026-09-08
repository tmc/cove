"""Fetch catalog media in resumable ranges, then verify size and SHA-1."""

import concurrent.futures
import argparse
import hashlib
import json
from pathlib import Path
import socket
import time
import urllib.request
import urllib.parse


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("--address", help="use this currently DNS-listed HTTP server address")
    args = parser.parse_args()
    root = args.root.resolve()
    entry = json.loads((root / "catalog-entry.json").read_text())
    url = entry["FilePath"]
    headers = {}
    if args.address:
        parsed = urllib.parse.urlsplit(url)
        addresses = {a[4][0] for a in socket.getaddrinfo(parsed.hostname, 80, type=socket.SOCK_STREAM)}
        if parsed.scheme != "http" or args.address not in addresses:
            parser.error("address must be listed in the published HTTP host's current DNS")
        headers["Host"] = parsed.netloc
        url = urllib.parse.urlunsplit(parsed._replace(netloc=args.address))
    size = int(entry["Size"])
    parts = root / "parts"
    parts.mkdir(exist_ok=True)
    chunk = 128 * 1024 * 1024
    deadline = time.monotonic() + 1800

    def fetch(index):
        start = index * chunk
        end = min(size, start + chunk)
        path = parts / f"{index:03d}"
        failures = 0
        while True:
            offset = path.stat().st_size if path.exists() else 0
            if offset == end - start:
                print(f"range {index} complete", flush=True)
                return path
            if offset > end - start:
                raise ValueError("oversized partial range")
            if time.monotonic() >= deadline:
                raise TimeoutError("download deadline")
            stop = min(end, start + offset + 4 * 1024 * 1024)
            request = urllib.request.Request(url, headers={**headers, "Range": f"bytes={start + offset}-{stop - 1}"})
            try:
                with urllib.request.urlopen(request, timeout=30) as response:
                    expected = f"bytes {start + offset}-{stop - 1}/{size}"
                    if response.status != 206 or response.headers.get("Content-Range") != expected:
                        raise ValueError(f"server did not honor range {expected}")
                    with path.open("ab") as output:
                        while data := response.read(1024 * 1024):
                            if time.monotonic() >= deadline:
                                raise TimeoutError("download deadline")
                            output.write(data)
                if path.stat().st_size != stop - start:
                    raise OSError("short range")
                failures = 0
            except (OSError, TimeoutError):
                failures += 1
                if failures == 3:
                    raise

    indices = range((size + chunk - 1) // chunk)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        files = list(pool.map(fetch, indices))
    output = root / "windows.verified.esd"
    sha1 = hashlib.sha1()
    sha256 = hashlib.sha256()
    with output.open("wb") as f:
        for part in files:
            with part.open("rb") as source:
                while data := source.read(8 * 1024 * 1024):
                    f.write(data)
                    sha1.update(data)
                    sha256.update(data)
    if output.stat().st_size != size or sha1.hexdigest() != entry["Sha1"]:
        raise ValueError("assembled ESD differs from catalog")
    output.replace(root / "windows.esd")
    receipt = {"size": size, "sha1": sha1.hexdigest(), "sha256": sha256.hexdigest(), "address": args.address}
    (root / "verified.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps(receipt), flush=True)


if __name__ == "__main__":
    main()
