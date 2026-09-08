"""Patch both installed Windows EFI entry paths on an already mounted scratch ESP."""

import argparse
import hashlib
import json
from pathlib import Path
import os
import tempfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def replace(path, data):
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix="cove-", delete=False) as f:
        temporary = Path(f.name)
        try:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        except BaseException:
            temporary.unlink()
            raise
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def patch(mount, shim, loader_hash):
    vendor = mount / "EFI/Microsoft/Boot/bootmgfw.efi"
    backup = vendor.with_name("bootmgfw-real.efi")
    fallback = mount / "EFI/BOOT/BOOTAA64.EFI"
    current = vendor.read_bytes()
    fallback.read_bytes()  # Refuse a mount without both expected entry paths.
    original = backup.read_bytes() if backup.exists() else current
    if digest(original) != loader_hash:
        raise ValueError("original Microsoft loader hash mismatch")
    if current not in (original, shim):
        raise ValueError("vendor loader differs from the original and requested shim")
    if shim == original:
        raise ValueError("shim and original loader must differ")
    if not backup.exists():
        with backup.open("xb") as f:
            f.write(original)
            f.flush()
            os.fsync(f.fileno())
    replace(fallback, shim)
    replace(vendor, shim)
    return {str(p.relative_to(mount)): digest(p.read_bytes())
            for p in (backup, fallback, vendor)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mount", type=Path)
    parser.add_argument("shim", type=Path, help="INSTALLED.EFI, which chainloads bootmgfw-real.efi")
    parser.add_argument("--loader-sha256", required=True)
    parser.add_argument("--shim-sha256", required=True)
    args = parser.parse_args()
    shim = args.shim.read_bytes()
    if digest(shim) != args.shim_sha256:
        parser.error("shim hash mismatch")
    print(json.dumps(patch(args.mount, shim, args.loader_sha256), indent=2))


if __name__ == "__main__":
    main()
