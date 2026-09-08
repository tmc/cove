from pathlib import Path
import tempfile
import unittest

from patch_installed import digest, patch


class PatchTest(unittest.TestCase):
    def test_backup_repeat_and_unknown_loader(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            vendor = root / "EFI/Microsoft/Boot/bootmgfw.efi"
            fallback = root / "EFI/BOOT/BOOTAA64.EFI"
            vendor.parent.mkdir(parents=True)
            fallback.parent.mkdir(parents=True)
            vendor.write_bytes(b"original")
            fallback.write_bytes(b"fallback")
            with self.assertRaisesRegex(ValueError, "hash mismatch"):
                patch(root, b"shim", digest(b"other"))
            self.assertEqual(vendor.read_bytes(), b"original")
            self.assertEqual(fallback.read_bytes(), b"fallback")
            expected = patch(root, b"shim", digest(b"original"))
            self.assertEqual(patch(root, b"shim", digest(b"original")), expected)
            self.assertEqual(vendor.with_name("bootmgfw-real.efi").read_bytes(), b"original")
            vendor.write_bytes(b"unexpected servicing update")
            with self.assertRaisesRegex(ValueError, "differs"):
                patch(root, b"shim", digest(b"original"))
            self.assertEqual(vendor.read_bytes(), b"unexpected servicing update")


if __name__ == "__main__":
    unittest.main()
