import hashlib
import http.server
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request


SOURCE = Path(__file__).resolve().parent


class TransportTest(unittest.TestCase):
    def test_viewer_round_trip_and_queue_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            process = subprocess.Popen([sys.executable, str(SOURCE / "viewer.py"), directory,
                                        "--port", "0", "--seconds", "4"], stdout=subprocess.DEVNULL)
            try:
                for _ in range(50):
                    if (root / "viewer.url").exists():
                        break
                    time.sleep(.02)
                url = (root / "viewer.url").read_text().strip()

                def post(path, data):
                    with urllib.request.urlopen(urllib.request.Request(url + path, data=data, headers={"X-Session": ""}), timeout=1) as response:
                        return response.read()

                with self.assertRaises(urllib.error.HTTPError) as error:
                    post("/input", b"key:13")
                self.assertEqual(error.exception.code, 409)
                error.exception.close()
                self.assertEqual(post("", b"\x89PNG\r\n\x1a\nfixture"), b"")

                with self.assertRaises(urllib.error.HTTPError) as error:
                    post("/input", b"unsupported:command")
                self.assertEqual(error.exception.code, 400)
                error.exception.close()
                for i in range(16):
                    self.assertEqual(post("/input", f"text:{i}".encode()), b"queued")
                with self.assertRaises(urllib.error.HTTPError) as error:
                    post("/input", b"text:overflow")
                self.assertEqual(error.exception.code, 429)
                error.exception.close()
                with self.assertRaises(urllib.error.HTTPError) as error:
                    post("", b"not a PNG")
                self.assertEqual(error.exception.code, 400)
                error.exception.close()
                # Transport fixture only: the receiver checks the signature, not image decoding.
                self.assertEqual(post("", b"\x89PNG\r\n\x1a\nfixture"), b"text:0")
                with urllib.request.urlopen(url + "/status") as response:
                    self.assertEqual(json.load(response), {"frames": 2, "queued": 15, "session": ""})
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": ""})) as response:
                    self.assertEqual(json.load(response), [f"text:{i}" for i in range(1, 16)])
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": ""})) as response:
                    self.assertEqual(json.load(response), [])
                self.assertEqual(post("/input", b"kbd:17,1"), b"queued")
                request = urllib.request.Request(url, data=b"\x89PNG\r\n\x1a\nfixture",
                                                 headers={"X-Control-Poll": "1"})
                with urllib.request.urlopen(request) as response:
                    self.assertEqual(response.read(), b"")
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": ""})) as response:
                    self.assertEqual(json.load(response), ["kbd:17,1"])
                self.assertEqual(post("/input", b"text:old session input"), b"queued")
                request = urllib.request.Request(url, data=b"\x89PNG\r\n\x1a\nnew guest",
                                                 headers={"X-Control-Poll": "1", "X-Session": "new"})
                with urllib.request.urlopen(request) as response:
                    self.assertEqual(response.read(), b"")
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": ""})) as response:
                    self.assertEqual(json.load(response), [])
                request = urllib.request.Request(url + "/input", data=b"key:13",
                                                 headers={"X-Session": "old"})
                with self.assertRaises(urllib.error.HTTPError) as error:
                    urllib.request.urlopen(request)
                self.assertEqual(error.exception.code, 409)
                error.exception.close()
                request = urllib.request.Request(url + "/input", data=b"key:13",
                                                 headers={"X-Session": "new"})
                with urllib.request.urlopen(request) as response:
                    self.assertEqual(response.read(), b"queued")
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": "old"})) as response:
                    self.assertEqual(json.load(response), [])
                with urllib.request.urlopen(urllib.request.Request(url + "/control", headers={"X-Session": "new"})) as response:
                    self.assertEqual(json.load(response), ["key:13"])
                self.assertEqual(process.wait(timeout=6), 0)
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=3)

    def test_download_resume_and_hash_gate(self):
        payload = b"catalog verified range fixture"
        ranges = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                span = self.headers["Range"]
                ranges.append(span)
                start, end = map(int, span.removeprefix("bytes=").split("-"))
                data = payload[start:end + 1]
                self.send_response(206)
                self.send_header("Content-Range", f"bytes {start}-{end}/{len(payload)}")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "parts").mkdir()
                (root / "parts/000").write_bytes(payload[:4])
                entry = {"Size": len(payload), "Sha1": hashlib.sha1(payload).hexdigest(),
                         "FilePath": f"http://127.0.0.1:{server.server_port}/image"}
                (root / "catalog-entry.json").write_text(json.dumps(entry))
                command = [sys.executable, str(SOURCE / "download.py"), directory]
                subprocess.run(command, check=True, capture_output=True, timeout=5)
                self.assertEqual((root / "windows.esd").read_bytes(), payload)
                self.assertEqual(ranges, [f"bytes=4-{len(payload) - 1}"])
                (root / "parts/000").write_bytes(b"!" + payload[1:])
                result = subprocess.run(command, capture_output=True, timeout=5)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual((root / "windows.esd").read_bytes(), payload)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == "__main__":
    unittest.main()
