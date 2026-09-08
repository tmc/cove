"""Build new unbooted media from a catalog-verified ESD; no VM is launched."""

import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile


def run(*args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def digest(path, algorithm="sha256"):
    h = hashlib.new(algorithm)
    with path.open("rb") as f:
        while data := f.read(8 * 1024 * 1024):
            h.update(data)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("--wimlib", default="/Users/tmc2/.local/homebrew/bin/wimlib-imagex")
    args = parser.parse_args()
    root = args.root.resolve()
    source = Path(__file__).resolve().parent
    esd = root / "media/windows.esd"
    entry = json.loads((root / "media/catalog-entry.json").read_text())
    if esd.stat().st_size != int(entry["Size"]) or digest(esd, "sha1") != entry["Sha1"]:
        raise ValueError("ESD differs from catalog")
    shim = root / "shim/SHIM.EFI"
    if digest(shim) != "bab5bd9e8822c51c0cc03713f63912886d51a510d5cd7d4ad6c3cc2ba876ef40":
        raise ValueError("shim differs from verified receipt")
    iso = Path.home() / ".vz/windows-drivers/virtio-win-0.1.285.iso"
    if digest(iso) != "bff25ed15cc6af578547e63b7c4afa8b5be4660f94f5c6b41d1bea34979ede02":
        raise ValueError("driver ISO differs from verified receipt")
    build = root / "prepared"
    build.mkdir()  # Refuse to overwrite any prior build or master.
    tree = build / "tree"
    tree.mkdir()
    wim = args.wimlib
    with (build / "esd-info.txt").open("w") as f:
        run(wim, "info", esd, stdout=f)
    run(wim, "apply", esd, "1", tree, "--no-acls")
    boot = tree / "sources/boot.wim"
    run(wim, "export", esd, "2", boot, "--compress=LZX")
    run(wim, "export", esd, "3", boot, "--compress=LZX", "--boot")
    install = build / "install.wim"
    run(wim, "export", esd, "Windows 11 Pro", install, "--compress=LZX")
    run(wim, "split", install, tree / "sources/install.swm", "3000")
    ini = build / "winpeshl.ini"
    ini.write_bytes(b"[LaunchApps]\r\n%SYSTEMROOT%\\System32\\cmd.exe, /c %SYSTEMROOT%\\System32\\inventory.cmd\r\n")
    script = build / "inventory.cmd"
    script.write_bytes((source / "inventory.cmd").read_text().replace("\n", "\r\n").encode())
    commands = "".join(f'add "{p}" "/Windows/System32/{p.name}"\n' for p in (ini, script))
    run(wim, "update", boot, "2", input=commands.encode())
    finish(root)


def finish(root):
    source = Path(__file__).resolve().parent
    build = root / "prepared"
    tree = build / "tree"
    esd = root / "media/windows.esd"
    shim = root / "shim/SHIM.EFI"
    iso = Path.home() / ".vz/windows-drivers/virtio-win-0.1.285.iso"
    boot = tree / "sources/boot.wim"
    install = build / "install.wim"
    if (build / "installer-master.dmg").exists():
        raise ValueError("installer master already exists")
    drivers = Path(tempfile.mkdtemp(prefix="drivers-", dir=build))
    run("tar", "-xf", iso, "-C", drivers, "NetKVM")
    shutil.copytree(drivers / "NetKVM/w11/ARM64", tree / "netkvm")
    shutil.copyfile(root / "screen.exe", tree / "screen.exe")
    (tree / "disks.txt").write_bytes((source / "disks.txt").read_text().replace("\n", "\r\n").encode())
    (tree / "COVEWINPE.TAG").write_text("cove Windows VZ installation experiment 20260908\n")
    microsoft = tree / "EFI/Microsoft/Boot"
    microsoft.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(tree / "EFI/BOOT/BOOTAA64.EFI", microsoft / "bootmgfw.efi")
    shutil.copyfile(shim, tree / "EFI/BOOT/BOOTAA64.EFI")
    image = build / "installer-master.dmg"
    run("hdiutil", "create", "-size", "8g", "-fs", "MS-DOS FAT32", "-volname", "COVEINSTALL", "-layout", "GPTSPUD", "-o", image)
    attached = plistlib.loads(subprocess.check_output(["hdiutil", "attach", "-nobrowse", "-plist", str(image)]))
    mount = next(Path(e["mount-point"]) for e in attached["system-entities"] if "mount-point" in e)
    try:
        run("rsync", "-r", str(tree) + "/", str(mount) + "/")
    finally:
        run("hdiutil", "detach", attached["system-entities"][0]["dev-entry"])
    files = [esd, shim, iso, boot, install, image, root / "screen.exe"]
    manifest = {str(p): {"size": p.stat().st_size, "sha256": digest(p)} for p in files}
    (build / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")


if __name__ == "__main__":
    main()
