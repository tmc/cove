from pathlib import Path
import http.server,threading,secrets,time,json,runpy,hashlib,urllib.request,subprocess
root=Path('/tmp/cove-winpe-viewer-20260907')
token='/'+secrets.token_hex(24);ui_token='/'+secrets.token_hex(24)
events=[];pending=[];latest=b'';lock=threading.Lock();start=time.monotonic();first=threading.Event()
class Base(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def reply(self,data,kind='text/plain'):
  self.send_response(200);self.send_header('Content-Type',kind);self.send_header('Content-Length',str(len(data)));self.send_header('Cache-Control','no-store');self.end_headers();self.wfile.write(data)
class Guest(Base):
 def do_POST(self):
  global latest
  if self.path!=token:self.send_error(404);return
  n=int(self.headers.get('Content-Length','0'))
  if not 0<n<2*1024*1024:self.send_error(413);return
  self.connection.settimeout(5);data=self.rfile.read(n)
  if len(data)!=n or not data.startswith(b'\x89PNG\r\n\x1a\n'):self.send_error(400);return
  with lock:
   index=len(events);latest=data;reply=pending.pop(0) if pending else b''
   events.append(dict(seconds=time.monotonic()-start,source=self.client_address[0],bytes=n,sha256=hashlib.sha256(data).hexdigest(),input=reply.decode()))
   (root/f'frame-{index}.png').write_bytes(data);(root/'events.json').write_text(json.dumps(events,indent=2)+'\n')
  self.reply(reply);first.set();print('frame',index,'input',reply,flush=True)
class UI(Base):
 def do_GET(self):
  path=self.path.split('?',1)[0]
  if path==ui_token:self.reply((root/'viewer.html').read_bytes(),'text/html');return
  with lock:
   if path==ui_token+'/status':self.reply(json.dumps({'frames':len(events)}).encode(),'application/json');return
   if path==ui_token+'/frame':self.reply(latest,'image/png');return
  self.send_error(404)
 def do_POST(self):
  if self.path!=ui_token+'/input':self.send_error(404);return
  n=int(self.headers.get('Content-Length','0'))
  if not 0<=n<=256:self.send_error(413);return
  text=self.rfile.read(n).decode('utf-8')
  with lock:
   if len(pending)>=16:self.send_error(429);return
   pending.append(('text:'+text).encode())
  self.reply(b'queued')
guest=http.server.ThreadingHTTPServer(('0.0.0.0',0),Guest)
ui=http.server.ThreadingHTTPServer(('127.0.0.1',0),UI)
url=f'http://127.0.0.1:{ui.server_port}{ui_token}'
(root/'PUSH.URL').write_text(f'http://192.168.64.1:{guest.server_port}{token}\n');(root/'viewer.url').write_text(url+'\n')
threads=[threading.Thread(target=x.serve_forever,daemon=True) for x in (guest,ui)]
for t in threads:t.start()
errors=[]
def boot():
 try:runpy.run_path(str(root/'apple.py'),run_name='__main__')
 except BaseException as e:errors.append(repr(e))
vm=threading.Thread(target=boot);vm.start()
try:
 if not first.wait(20):raise TimeoutError('no live frame')
 request=urllib.request.Request(url+'/input',data=b'VIEWER',method='POST')
 with urllib.request.urlopen(request,timeout=3) as response:assert response.read()==b'queued'
 time.sleep(4)
 with urllib.request.urlopen(url+'/status',timeout=3) as response:(root/'ui-status.json').write_bytes(response.read())
 browser='/Applications/Brave Browser.app/Contents/MacOS/Brave Browser'
 with (root/'browser.log').open('w') as log:
  result=subprocess.run([browser,'--headless','--no-first-run',f'--user-data-dir={root}/browser-profile','--window-size=1280,1000',f'--screenshot={root}/viewer.png','--timeout=3000',url],stdout=log,stderr=log,timeout=12)
  print('browser exit',result.returncode,flush=True)
finally:
 vm.join(timeout=45)
 for server in (guest,ui):server.shutdown();server.server_close()
 for t in threads:t.join()
print('VM errors:',errors,flush=True)
