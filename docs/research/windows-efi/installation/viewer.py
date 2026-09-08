"""Bounded research viewer. Prints a loopback URL; secrets stay in scratch."""

import argparse
import collections
import hashlib
import http.server
import json
from pathlib import Path
import secrets
import threading
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    parser.add_argument("--seconds", type=int, default=1800)
    parser.add_argument("--port", type=int, default=49191)
    parser.add_argument("--agent", type=Path, help="optional compiled guest agent to serve for a controlled update")
    args = parser.parse_args()
    if not 1 <= args.seconds <= 7200:
        parser.error("seconds must be between 1 and 7200")
    root = args.root.resolve()
    root.mkdir(parents=True, exist_ok=True)
    secret = root / "token"
    if not secret.exists():
        secret.write_text(secrets.token_hex(24))
        secret.chmod(0o600)
    token = "/" + secret.read_text().strip()
    queue = collections.deque()
    lock = threading.Lock()
    latest = b""
    count = 0
    frame_time = 0
    frame_session = ""
    run = root / time.strftime("%Y%m%dT%H%M%S", time.gmtime())
    run.mkdir()
    events = (run / "events.jsonl").open("a", buffering=1)

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, data, kind="text/plain", headers=None):
            self.send_response(200)
            self.send_header("Content-Type", kind)
            self.send_header("Content-Length", str(len(data)))
            self.send_header("Cache-Control", "no-store")
            for key, value in (headers or {}).items():
                self.send_header(key, str(value))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path == token + "/agent.exe" and args.agent:
                self.reply(args.agent.read_bytes(), "application/octet-stream")
                return
            if self.path == token + "/control":
                with lock:
                    if time.time() - frame_time > 5 or self.headers.get("X-Session") != frame_session:
                        self.reply(b"[]", "application/json")
                        return
                    commands = [queue.popleft().decode() for _ in range(len(queue))]
                    if commands:
                        events.write(json.dumps({"time": time.time(), "control": commands,
                                                 "source": self.client_address[0]}) + "\n")
                self.reply(json.dumps(commands).encode(), "application/json")
                return
            if self.client_address[0] != "127.0.0.1":
                self.send_error(403)
                return
            path = self.path.split("?", 1)[0]
            if path == token:
                page = """<!doctype html><meta charset=utf-8><title>Windows VZ research</title>
<style>body{font:16px system-ui;background:#222;color:white}img{max-width:95vw}input{width:30em}</style>
<form><input placeholder="Text to send"><button>Send text</button></form>
<button id=enter>Enter</button><p id=status></p><img>
<script>
const base=location.pathname,img=document.querySelector('img');
let session='';
async function send(command){await fetch(base+'/input',{method:'POST',headers:{'X-Session':session},body:command});}
document.querySelector('form').onsubmit=e=>{e.preventDefault();send('text:'+document.querySelector('input').value);};
document.querySelector('#enter').onclick=()=>send('key:13');
img.onclick=e=>{const r=img.getBoundingClientRect();send('click:'+Math.floor((e.clientX-r.left)*img.naturalWidth/r.width)+','+Math.floor((e.clientY-r.top)*img.naturalHeight/r.height));};
setInterval(()=>{img.src=base+'/frame?t='+Date.now();fetch(base+'/status').then(r=>r.json()).then(s=>{session=s.session;document.querySelector('#status').textContent='Frames: '+s.frames+'; queued: '+s.queued;});},1500);
</script>"""
                self.reply(page.encode(), "text/html")
            elif path == token + "/frame":
                with lock:
                    data = latest
                    headers = {"X-Frame-Time": frame_time, "X-Agent-Session": frame_session}
                self.reply(data, "image/png", headers)
            elif path == token + "/status":
                with lock:
                    data = json.dumps({"frames": count, "queued": len(queue), "session": frame_session}).encode()
                self.reply(data, "application/json")
            else:
                self.send_error(404)

        def do_POST(self):
            nonlocal latest, count, frame_time, frame_session
            if self.path not in (token, token + "/input"):
                self.send_error(404)
                return
            try:
                n = int(self.headers.get("Content-Length", "0"))
                limit = 2 * 1024 * 1024 if self.path == token else 1024
                if not 0 < n <= limit:
                    raise ValueError("invalid length")
                self.connection.settimeout(5)
                data = self.rfile.read(n)
                if len(data) != n:
                    raise ValueError("short body")
            except (ValueError, OSError):
                self.send_error(400)
                return
            if self.path == token + "/input":
                if self.client_address[0] != "127.0.0.1":
                    self.send_error(403)
                    return
                try:
                    command = data.decode("utf-8")
                    if not command.startswith(("text:", "click:", "key:", "kbd:", "pointer:", "wheel:")):
                        raise ValueError("unknown command")
                except (UnicodeError, ValueError):
                    self.send_error(400)
                    return
                with lock:
                    if time.time() - frame_time > 5 or self.headers.get("X-Session") != frame_session:
                        self.send_error(409, "guest session unavailable or changed")
                        return
                    if len(queue) >= 16:
                        self.send_error(429)
                        return
                    queue.append(data)
                self.reply(b"queued")
                return
            if not data.startswith(b"\x89PNG\r\n\x1a\n"):
                self.send_error(400)
                return
            with lock:
                session = self.headers.get("X-Session", "")
                if session != frame_session:
                    if queue:
                        events.write(json.dumps({"time": time.time(), "discarded": len(queue),
                                                 "old_session": frame_session, "new_session": session}) + "\n")
                    queue.clear()
                count += 1
                latest = data
                frame_time = time.time()
                frame_session = session
                (run / f"frame-{count:04d}.png").write_bytes(data)
                command = queue.popleft() if queue and self.headers.get("X-Control-Poll") != "1" else b""
                event = {"time": time.time(), "frame": count, "source": self.client_address[0],
                         "sha256": hashlib.sha256(data).hexdigest(), "input": command.decode(),
                         "systemroot": self.headers.get("X-Systemroot"),
                         "session": self.headers.get("X-Session"), "uptime": self.headers.get("X-Uptime")}
                events.write(json.dumps(event) + "\n")
            self.reply(command)

    server = http.server.ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    url = f"http://127.0.0.1:{server.server_port}{token}"
    (root / "viewer.url").write_text(url + "\n")
    (root / "PUSH.URL").write_text(f"http://192.168.64.1:{server.server_port}{token}\n")
    print(url, flush=True)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        thread.join(args.seconds)
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
        events.close()


if __name__ == "__main__":
    main()
