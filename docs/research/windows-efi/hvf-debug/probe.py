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
args += ['-chardev',f'socket,path={out}/gdb.sock,server=on,wait=off,id=gdb0','-gdb','chardev:gdb0']
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
   print(command('stop'))
   debug={}
   with socket.socket(socket.AF_UNIX) as gdb:
    gdb.settimeout(5);gdb.connect(str(out/'gdb.sock'))
    def recvbyte():
     b=gdb.recv(1)
     if not b:raise EOFError('gdb closed')
     return b
    def packet(request):
     data=request.encode();gdb.sendall(b'$'+data+b'#'+f'{sum(data)%256:02x}'.encode())
     while recvbyte()!=b'$':pass
     payload=bytearray()
     while True:
      b=recvbyte()
      if b==b'#':break
      payload.extend(b)
     check=recvbyte()+recvbyte()
     if int(check,16)!=sum(payload)%256:raise ValueError('gdb checksum')
     gdb.sendall(b'+')
     result=bytearray();i=0
     while i<len(payload):
      b=payload[i];i+=1
      if b==125:
       result.append(payload[i]^32);i+=1
      elif b==42:
       result.extend([result[-1]]*(payload[i]-29));i+=1
      else:result.append(b)
     response=result.decode();debug[request]=response;return response
    packet('qSupported:multiprocess+;qXfer:features:read+')
    packet('?')
    registers=packet('g')
    assert len(registers)>=536 and not registers.startswith('E'),registers
    debug['pc']=hex(int.from_bytes(bytes.fromhex(registers[512:528]),'little'))
    assert packet('Qqemu.PhyMemMode:1')=='OK'
    firmware=bytes.fromhex(packet('m0,40'))
    expected=pathlib.Path('/opt/homebrew/share/qemu/edk2-aarch64-code.fd').read_bytes()[:64]
    debug['firmware_matches']=firmware==expected
    assert firmware==expected,'firmware memory mismatch'
    assert packet('Qqemu.PhyMemMode:0')=='OK'
    assert packet('D;1')=='OK'
   (out/'debug.json').write_text(json.dumps(debug,indent=2)+'\n')
   print(command('cont'))
   time.sleep(1)
   print(command('send-key',keys=[{'type':'qcode','data':'shift'},{'type':'qcode','data':'f10'}]))
   time.sleep(4)
   (out/'resumed-status.json').write_text(json.dumps(command('query-status'))+'\n')
   print(command('screendump',filename=str(out/'resumed.ppm')))
   print(command('quit'))
   vm.wait(timeout=5)
 finally:
  if vm.poll() is None:
   vm.terminate()
   try:vm.wait(timeout=5)
   except subprocess.TimeoutExpired:vm.kill();vm.wait()
