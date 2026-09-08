from pathlib import Path
import http.server,threading,secrets,time,json,runpy,hashlib,urllib.request
root=Path('/tmp/cove-winpe-input-20260907')

token='/'+secrets.token_hex(24);events=[];start=time.monotonic()
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  self.send_response(200);self.send_header("Content-Length","2");self.end_headers();self.wfile.write(b"ok")
 def do_POST(self):
  print("request headers",self.client_address,self.headers.get("Content-Length"),flush=True)
  if self.path!=token:self.send_error(404);return
  n=int(self.headers.get('Content-Length','0'))
  if not 0<n<2*1024*1024:self.send_error(413);return
  self.connection.settimeout(5)
  data=self.rfile.read(n)
  if len(data)!=n or not data.startswith(b'\x89PNG\r\n\x1a\n'):self.send_error(400);return
  index=len(events);(root/f'frame-{index}.png').write_bytes(data)
  reply=b'alt-space' if index==0 else b''
  events.append(dict(seconds=time.monotonic()-start,source=self.client_address[0],bytes=n,sha256=hashlib.sha256(data).hexdigest(),input=reply.decode()))
  (root/'events.json').write_text(json.dumps(events,indent=2)+'\n')
  self.send_response(200);self.send_header('Content-Length',str(len(reply)));self.end_headers();self.wfile.write(reply)
  print('received frame',index,n,'bytes; reply',reply,flush=True)
server=http.server.ThreadingHTTPServer(('0.0.0.0',0),Handler)
(root/'PUSH.URL').write_text(f'http://192.168.64.1:{server.server_port}{token}\n')
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
with urllib.request.urlopen(f'http://127.0.0.1:{server.server_port}/health',timeout=3) as check:
 assert check.read()==b'ok'
print('host receiver self-check passed',flush=True)
try:runpy.run_path('/tmp/cove-winpe-input-20260907/apple.py',run_name='__main__')
finally:server.shutdown();server.server_close();thread.join()
print('received frames:',len(events),flush=True)
