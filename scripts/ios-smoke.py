#!/usr/bin/env python3
"""Record native iOS prerequisites; --run boots only an independent bundle copy."""

import argparse
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import signal
import subprocess
import time


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def capture(argv, timeout=30):
    try:
        r = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
        return {"argv": list(map(str, argv)), "exitCode": r.returncode,
                "stdout": r.stdout, "stderr": r.stderr}
    except (OSError, subprocess.TimeoutExpired) as e:
        return {"argv": list(map(str, argv)), "exitCode": None, "error": str(e)}


def artifact_paths(bundle):
    config = json.loads((bundle / "config.json").read_text())
    ios = config["ios"]
    names = ["config.json", "hw.model", "machine.id", "aux.img", "sep.img", "disk.img", ios["rom"]]
    if ios.get("sepROM"):
        names.append(ios["sepROM"])
    result = {}
    for name in names:
        rel = Path(name)
        if rel.is_absolute() or ".." in rel.parts:
            raise ValueError("artifact path must remain inside bundle: " + name)
        path = (bundle / rel).resolve(strict=True)
        if not path.is_relative_to(bundle) or not path.is_file():
            raise ValueError("artifact must be a contained regular file: " + name)
        result[name] = path
    if len(set(result.values())) != len(names):
        raise ValueError("bundle artifacts must be distinct files")
    return result


def copy_bundle(bundle, destination):
    paths = artifact_paths(bundle)
    before = {name: digest(path) for name, path in paths.items()}
    destination.mkdir()
    for name, path in paths.items():
        target = destination / name
        target.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["/bin/cp", "-c", str(path), str(target)], check=True)
        if target.stat().st_ino == path.stat().st_ino and target.stat().st_dev == path.stat().st_dev:
            raise ValueError("copy unexpectedly shares a source inode: " + name)
        if digest(target) != before[name]:
            raise ValueError("artifact changed while copying: " + name)
    after = {name: digest(path) for name, path in paths.items()}
    if after != before:
        raise ValueError("source bundle changed during copy")
    return paths, before


def run_copy(binary, copy, output, mode, duration, grace):
    argv = [str(binary), "-headless", "-vm-dir", str(copy), "-serial", "stdout",
            "-start-timeout", str(duration) + "s"]
    if mode == "dfu":
        argv.append("-force-dfu")
    argv.append("run")
    env = dict(os.environ, COVE_STATE_DIR=str(output / "state"))
    forced = False
    with (output / "runtime.stdout").open("w") as stdout, (output / "runtime.stderr").open("w") as stderr:
        child = subprocess.Popen(argv, stdout=stdout, stderr=stderr, env=env, start_new_session=True)
        try:
            child.wait(timeout=duration)
        except subprocess.TimeoutExpired:
            os.killpg(child.pid, signal.SIGTERM)
            try:
                child.wait(timeout=grace)
            except subprocess.TimeoutExpired:
                forced = True
        finally:
            if child.poll() is None:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                child.wait()
    text = (output / "runtime.stdout").read_text()
    return {"argv": argv, "exitCode": child.returncode, "forcedKill": forced,
            "nativeRunningObserved": "iOS research VM started;" in text,
            "dfuObserved": None, "guestBootVerified": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path, help="new evidence directory, outside the source bundle")
    parser.add_argument("--bundle", type=Path, help="stopped, prepared source bundle; never launched directly")
    parser.add_argument("--run", action="store_true", help="opt in to launching a fresh copy")
    parser.add_argument("--mode", choices=["normal", "dfu"], default="dfu")
    parser.add_argument("--duration", type=float, default=20)
    parser.add_argument("--stop-grace", type=float, default=45)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    bundle = args.bundle.resolve(strict=True) if args.bundle else None
    output = args.output.resolve()
    if bundle and output.is_relative_to(bundle):
        parser.error("output must be outside source bundle")
    if not (math.isfinite(args.duration) and math.isfinite(args.stop_grace)) or args.duration <= 0 or args.stop_grace <= 0:
        parser.error("duration and stop-grace must be positive")
    output.mkdir(parents=True, exist_ok=False)
    report = {"schemaVersion": 1, "startedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "mode": args.mode, "host": {"machine": platform.machine()},
              "binary": str(binary), "binarySHA256": digest(binary),
              "nativeRunningObserved": False, "dfuObserved": None,
              "guestBootVerified": False, "blockers": []}
    lock = None
    paths = None
    before = None
    try:
        report["host"]["os"] = capture(["/usr/bin/sw_vers"])
        report["host"]["hardware"] = capture(["/usr/sbin/sysctl", "-n", "hw.model", "hw.memsize"])
        report["signature"] = capture(["/usr/bin/codesign", "--verify", "--strict", str(binary)])
        report["entitlements"] = capture(["/usr/bin/codesign", "-d", "--entitlements", ":-", str(binary)])
        report["probe"] = capture([str(binary), "ios", "probe"])
        if report["probe"]["exitCode"] == -signal.SIGKILL:
            predicate = 'process == "amfid" AND eventMessage CONTAINS ' + json.dumps(binary.name)
            report["signingLog"] = capture(["/usr/bin/log", "show", "--last", "1m", "--style", "compact", "--predicate", predicate])
        report["usbBefore"] = capture(["/usr/sbin/ioreg", "-p", "IOUSB", "-l", "-w", "0"])
        if report["signature"]["exitCode"] != 0:
            report["blockers"].append("binary signature verification failed")
        if report["probe"]["exitCode"] != 0:
            report["blockers"].append("native research probe failed; inspect probe JSON and stderr")
        if bundle is None:
            report["blockers"].append("no prepared iOS bundle supplied; need config, model, identity, aux/SEP/disk state and boot ROM")
        else:
            report["sourceBundle"] = str(bundle)
            lock_path = bundle / "run.lock"
            if lock_path.exists():
                lock = lock_path.open("rb")
                fcntl.flock(lock.fileno(), fcntl.LOCK_SH | fcntl.LOCK_NB)
            report["validation"] = capture([str(binary), "ios", "validate", "-vm-dir", str(bundle)])
            if report["validation"]["exitCode"] != 0:
                report["blockers"].append("prepared-bundle validation failed")
        if args.run and not report["blockers"]:
            paths, before = copy_bundle(bundle, output / "disposable.covevm")
            report["sourceSHA256"] = before
            report["runtime"] = run_copy(binary, output / "disposable.covevm", output, args.mode, args.duration, args.stop_grace)
            report["nativeRunningObserved"] = report["runtime"]["nativeRunningObserved"]
            report["usbAfter"] = capture(["/usr/sbin/ioreg", "-p", "IOUSB", "-l", "-w", "0"])
            if report["runtime"]["exitCode"] != 0 or report["runtime"]["forcedKill"] or not report["nativeRunningObserved"]:
                report["blockers"].append("native running state not reached or clean shutdown not completed; inspect runtime logs")
        elif not args.run:
            report["runSkipped"] = "pass --run to opt in to starting a disposable copy"
    except Exception as e:
        report["blockers"].append(str(e))
    finally:
        if paths and before:
            try:
                report["sourceUnchanged"] = all(digest(path) == before[name] for name, path in paths.items())
            except OSError as e:
                report["sourceUnchanged"] = False
                report["blockers"].append("source verification failed: " + str(e))
            if not report["sourceUnchanged"]:
                report["blockers"].append("source changed during smoke run; inspect external writers")
        if lock:
            lock.close()
        try:
            report["binaryUnchanged"] = digest(binary) == report["binarySHA256"]
        except OSError:
            report["binaryUnchanged"] = False
        if not report["binaryUnchanged"]:
            report["blockers"].append("binary changed or disappeared during smoke run")
        report["status"] = "blocked" if report["blockers"] else "native-running-observed" if report["nativeRunningObserved"] else "inspection-only"
        report["dfuEvidence"] = "USB snapshots alone do not establish target identity or DFU mode; mode-aware ECID-targeted discovery remains required"
        (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(output / "report.json")
    return 1 if report["blockers"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
