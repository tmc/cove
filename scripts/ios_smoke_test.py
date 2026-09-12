import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("ios_smoke", Path(__file__).with_name("ios-smoke.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class SmokeTest(unittest.TestCase):
    def fixture(self, root):
        bundle = root / "source.covevm"
        bundle.mkdir()
        (bundle / "config.json").write_text(json.dumps({"ios": {"rom": "boot.rom"}}))
        for name in ["hw.model", "machine.id", "aux.img", "sep.img", "disk.img", "boot.rom"]:
            (bundle / name).write_bytes(("original " + name).encode())
        return bundle

    def test_copy_is_independent(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            bundle = self.fixture(root)
            copy = root / "copy.covevm"
            paths, hashes = smoke.copy_bundle(bundle, copy)
            (copy / "disk.img").write_bytes(b"guest writes")
            self.assertEqual({name: smoke.digest(path) for name, path in paths.items()}, hashes)

    def test_escape_rejected_before_copy(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            bundle = self.fixture(root)
            external = root / "external.rom"
            external.write_bytes(b"outside")
            (bundle / "boot.rom").unlink()
            (bundle / "boot.rom").symlink_to(external)
            copy = root / "copy.covevm"
            with self.assertRaisesRegex(ValueError, "contained regular"):
                smoke.copy_bundle(bundle, copy)
            self.assertFalse(copy.exists())

    def test_only_copy_passed_to_runtime(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            bundle = self.fixture(root)
            output = root / "evidence"
            output.mkdir()
            copy = output / "disposable.covevm"
            paths, hashes = smoke.copy_bundle(bundle, copy)
            binary = root / "fake-cove"
            binary.write_text("#!/usr/bin/env python3\nimport sys,pathlib\np=pathlib.Path(sys.argv[sys.argv.index('-vm-dir')+1])\n(p/'disk.img').write_bytes(b'guest write')\nprint('iOS research VM started; fake test only')\n")
            binary.chmod(0o755)
            result = smoke.run_copy(binary, copy, output, "dfu", 2, 1)
            self.assertEqual(result["exitCode"], 0)
            self.assertIn(str(copy), result["argv"])
            self.assertNotIn(str(bundle), result["argv"])
            self.assertIn("-force-dfu", result["argv"])
            self.assertIsNone(result["dfuObserved"])
            self.assertFalse(result["guestBootVerified"])
            self.assertEqual({name: smoke.digest(path) for name, path in paths.items()}, hashes)

    def test_unresponsive_process_reaped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            binary = root / "fake-cove"
            binary.write_text("#!/usr/bin/env python3\nimport signal,time\nsignal.signal(signal.SIGTERM,signal.SIG_IGN)\nwhile True:time.sleep(1)\n")
            binary.chmod(0o755)
            result = smoke.run_copy(binary, root / "copy", root, "normal", 0.5, 0.1)
            self.assertTrue(result["forcedKill"])
            self.assertLess(result["exitCode"], 0)
            self.assertFalse(result["nativeRunningObserved"])


if __name__ == "__main__":
    unittest.main()
