"""Decode virtio NIC capabilities from pci.c's saved text report."""
import argparse
import json
import pathlib
import re
import struct


def decode(text):
    devices = []
    for block in text.split("handle "):
        if "vendor=0x1af4 device=0x1041" not in block:
            continue
        words = {}
        for line in block.splitlines():
            match = re.fullmatch(r" (0x[0-9a-f]+):((?: 0x[0-9a-f]+){4})", line)
            if match:
                offset = int(match[1], 16)
                if offset in words:
                    raise ValueError("duplicate config row")
                words[offset] = [int(x, 16) for x in match[2].split()]
        if set(words) != set(range(0, 256, 16)):
            raise ValueError("incomplete config snapshot")
        data = b"".join(struct.pack("<4I", *words[i]) for i in range(0, 256, 16))
        offset = data[0x34]
        seen = set()
        caps = []
        while offset:
            if offset in seen or offset < 0x40 or offset > 0xfc or offset % 4:
                raise ValueError("invalid capability chain")
            seen.add(offset)
            if data[offset] == 9:
                length = data[offset + 2]
                if length < 16 or offset + length > len(data):
                    raise ValueError("invalid virtio capability length")
                caps.append({"offset": hex(offset), "type": data[offset + 3],
                             "bar": data[offset + 4]})
            offset = data[offset + 1]
        devices.append({"vendor": "0x1af4", "device": "0x1041", "capabilities": caps,
                        "missing_types_1_to_5": sorted(set(range(1, 6)) - {c["type"] for c in caps})})
    if not devices:
        raise ValueError("no modern virtio NIC in report")
    return devices


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report", type=pathlib.Path)
    args = parser.parse_args()
    print(json.dumps(decode(args.report.read_text()), indent=2))
