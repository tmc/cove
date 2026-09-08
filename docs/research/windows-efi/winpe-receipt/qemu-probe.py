import argparse,json,pathlib,socket,subprocess,time
p=argparse.ArgumentParser()
p.add_argument('disk')
p.add_argument('output')
p.add_argument('--seconds',type=int,default=20)
a=p.parse_args()
if a.seconds<=0:p.error('seconds must be positive')
disk=pathlib.Path(a.disk).resolve(strict=True)
out=pathlib.Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True)
monitor=out/'qmp.sock'
if monitor.exists():raise SystemExit('monitor already exists; use a fresh output directory')
args=['/opt/homebrew/bin/qemu-system-aarch64','-machine','virt,accel=hvf','-cpu','host','-smp','2','-m','4096','-bios','/opt/homebrew/share/qemu/edk2-aarch64-code.fd','-device','ramfb','-device','qemu-xhci','-device','usb-kbd','-device','usb-tablet','-drive',f'if=none,id=boot,format=raw,file={disk}','-device','usb-storage,drive=boot,bootindex=0','-display','none','-serial',f'file:{out}/serial.log','-qmp',f'unix:{monitor},server=on,wait=off','-no-reboot','-nic','none']
(out/'command.json').write_text(json.dumps(args,indent=2)+'\n')
with (out/'qemu.log').open('w') as log:
 vm=subprocess.Popen(args,stdout=log,stderr=log)
 try:
  deadline=time.monotonic()+10
  while not monitor.exists():
   if vm.poll() is not None:raise RuntimeError(f'qemu exited {vm.returncode}')
   if time.monotonic()>deadline:raise TimeoutError('qmp socket')
   time.sleep(.1)
  with socket.socket(socket.AF_UNIX) as sock:
   sock.settimeout(5);sock.connect(str(monitor));stream=sock.makefile('rwb',buffering=0)
   print(stream.readline().decode().strip())
   def command(name,**args):
    stream.write((json.dumps({'execute':name,'arguments':args})+'\n').encode())
    while True:
     r=json.loads(stream.readline())
     if 'return'in r or 'error'in r:return r
   print(command('qmp_capabilities'))
   first=min(6,a.seconds)
   time.sleep(first)
   (out/'blockstats-first.json').write_text(json.dumps(command('query-blockstats'),indent=2)+'\n')
   print(command('screendump',filename=str(out/'screen-first.ppm')))
   time.sleep(a.seconds-first)
   result=command('query-status');print(result)
   (out/'status.json').write_text(json.dumps(result)+'\n')
   print(command('screendump',filename=str(out/'screen.ppm')))
   (out/'blockstats-final.json').write_text(json.dumps(command('query-blockstats'),indent=2)+'\n')
   print(command('quit'))
   vm.wait(timeout=5)
 finally:
  if vm.poll() is None:
   vm.terminate()
   try:vm.wait(timeout=5)
   except subprocess.TimeoutExpired:vm.kill();vm.wait()
