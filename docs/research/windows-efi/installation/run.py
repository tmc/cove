"""Run one bounded VZ inventory, installation, or installed-disk boot."""

import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import secrets
import shutil
import subprocess
import time


def call(*args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def attach(image):
    result = plistlib.loads(subprocess.check_output(["hdiutil", "attach", "-nobrowse", "-plist", str(image)]))
    mount = next(Path(e["mount-point"]) for e in result["system-entities"] if "mount-point" in e)
    return result["system-entities"][0]["dev-entry"], mount


def answer_file():
    password = secrets.token_hex(16)
    return f'''<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
 <settings pass="oobeSystem">
  <component name="Microsoft-Windows-International-Core" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
   <InputLocale>0409:00000409</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
  </component>
  <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
   <OOBE><HideEULAPage>true</HideEULAPage><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><ProtectYourPC>3</ProtectYourPC></OOBE>
   <UserAccounts><LocalAccounts><LocalAccount wcm:action="add"><Name>cove</Name><DisplayName>Cove Research</DisplayName><Group>Administrators</Group><Password><Value>{password}</Value><PlainText>true</PlainText></Password></LocalAccount></LocalAccounts></UserAccounts>
   <AutoLogon><Enabled>true</Enabled><Username>cove</Username><LogonCount>10</LogonCount><Password><Value>{password}</Value><PlainText>true</PlainText></Password></AutoLogon>
  </component>
 </settings>
</unattend>
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("name")
    parser.add_argument("--mode", choices=("inventory", "install", "boot"), default="inventory")
    parser.add_argument("--seconds", type=int, default=60)
    parser.add_argument("--target", type=Path, help="existing dedicated installed disk, required for boot")
    parser.add_argument("--state", type=Path, help="existing dedicated EFI state, required for boot")
    args = parser.parse_args()
    if not 1 <= args.seconds <= 1800:
        parser.error("seconds must be between 1 and 1800")
    if args.mode == "boot" and (not args.target or not args.state):
        parser.error("boot requires target and state")
    if args.mode != "boot" and (args.target or args.state):
        parser.error("inventory and install create fresh dedicated targets")
    root = args.root.resolve()
    if Path(args.name).name != args.name or args.name in (".", ".."):
        parser.error("name must be a single directory name")
    run = root / args.name
    run.mkdir()
    source = Path(__file__).resolve().parent
    installer = None
    if args.mode == "boot":
        target, state = args.target.resolve(), args.state.resolve()
        if not target.is_relative_to(root) or not state.is_relative_to(root):
            parser.error("target and state must be inside this experiment root")
    else:
        target, state = run / "target.img", run / "state"
        with target.open("xb") as f:
            f.truncate(64 << 30)
        installer = run / "installer.dmg"
        call("cp", "-c", root / "prepared/installer-master.dmg", installer)
        device, mount = attach(installer)
        try:
            shutil.copyfile(root / "viewer/PUSH.URL", mount / "PUSH.URL")
            if args.mode == "install":
                if not (root / "inventory-passed.json").exists():
                    raise ValueError("installation requires reviewed inventory-passed.json receipt")
                shutil.copyfile(root / "diskcheck.exe", mount / "diskcheck.exe")
                shutil.copyfile(root / "diskcheck-test.exe", mount / "diskcheck-test.exe")
                for name in ("install.cmd", "partition.txt", "desktop.cmd"):
                    (mount / name).write_bytes((source / name).read_text().replace("\n", "\r\n").encode())
                (mount / "unattend.xml").write_text(answer_file())
                (mount / "INSTALL.TAG").write_text("install onto the new dedicated 64 GiB target\n")
        finally:
            call("hdiutil", "detach", device)
    probe = run / "winbootprobe"
    shutil.copy2(root / "winbootprobe", probe)
    command = [str(probe), "-pmu", "-nat", "-graphics", "virtio", "-nocap",
               "-disk", str(target), "-state", str(state), "-seconds", str(args.seconds)]
    if installer:
        command += ["-efi", str(installer)]
    metadata = {"command": command, "mode": args.mode, "started": time.time(),
                "probe_sha256": hashlib.sha256(probe.read_bytes()).hexdigest()}
    (run / "command.json").write_text(json.dumps(metadata, indent=2) + "\n")
    try:
        with (run / "probe.log").open("w") as log:
            result = subprocess.run(command, stdout=log, stderr=log, timeout=args.seconds + 45)
        metadata["exit"] = result.returncode
    finally:
        metadata["ended"] = time.time()
        (run / "command.json").write_text(json.dumps(metadata, indent=2) + "\n")
    if installer:
        time.sleep(1)
        device, mount = attach(installer)
        try:
            for pattern in ("*.TXT", "*.LOG", "*.log", "APPLIED.TAG", "latest.png"):
                for path in mount.glob(pattern):
                    if path.is_file():
                        shutil.copyfile(path, run / path.name)
        finally:
            call("hdiutil", "detach", device)
    print(json.dumps(metadata, indent=2))


if __name__ == "__main__":
    main()
