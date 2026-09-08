import ctypes,json,pathlib,plistlib,shutil,subprocess,time,hashlib
root=pathlib.Path('/tmp/cove-winpe-input-20260907')
lib=ctypes.CDLL('/usr/lib/libproc.dylib',use_errno=True)
class Usage(ctypes.Structure):
 _fields_=[('uuid',ctypes.c_ubyte*16),('values',ctypes.c_uint64*18)]
lib.proc_pid_rusage.argtypes=[ctypes.c_int,ctypes.c_int,ctypes.c_void_p]
def services():
 return {int(s.split(None,1)[0]) for s in subprocess.check_output(['ps','-axo','pid,comm'],text=True).splitlines() if s.endswith('/com.apple.Virtualization.VirtualMachine')}
def usage(pid):
 u=Usage();e=lib.proc_pid_rusage(pid,2,ctypes.byref(u));return dict(pid=pid,error=e,user_ns=u.values[0],system_ns=u.values[1],rss=u.values[6],read_bytes=u.values[16])
def attach(img):
 d=plistlib.loads(subprocess.check_output(['hdiutil','attach','-plist',str(img)]));return d,next(pathlib.Path(e['mount-point']) for e in d['system-entities'] if 'mount-point'in e)
def detach(d):subprocess.run(['hdiutil','detach',d['system-entities'][0]['dev-entry']],check=True,stdout=subprocess.DEVNULL)
for name in ('live-shim',):
 img=root/(name+'.img');assert not img.exists()
 source='/tmp/cove-winpe-network-20260907/network-base.img'
 subprocess.run(['cp','-c',source,str(img)],check=True)
 metadata={'source':source}
 if name.endswith('shim'):
  d,m=attach(img)
  try:
   artifact=pathlib.Path('/tmp/cove-windows-shim-20260907/SHIM.EFI');shutil.copyfile(artifact,m/'EFI/BOOT/BOOTAA64.EFI')
   (m/'SHIMLOG.TXT').unlink(missing_ok=True)
   assert not (m/'WINPE-RECEIPT.TXT').exists()
   shutil.copyfile(pathlib.Path('/tmp/cove-winpe-input-20260907/screen.exe'),m/'screen.exe')
   shutil.copyfile(root/'PUSH.URL',m/'PUSH.URL')
   metadata.update(shim_sha256=hashlib.sha256(artifact.read_bytes()).hexdigest(),child_sha256=hashlib.sha256((m/'EFI/Microsoft/Boot/bootmgfw.efi').read_bytes()).hexdigest())
  finally:detach(d)
 cmd=['/tmp/cove-winpe-network-20260907/probe','-pmu','-nat','-efi',str(img),'-graphics','virtio','-nocap','-seconds','30']
 metadata['command']=cmd;(root/(name+'-command.json')).write_text(json.dumps(metadata,indent=2)+'\n')
 prior=services();samples=[];start=time.monotonic()
 with (root/(name+'.log')).open('w') as log:
  p=subprocess.Popen(cmd,stdout=log,stderr=log)
  try:
   for at in (1,3,6,12,29):
    while time.monotonic()-start<at and p.poll() is None:time.sleep(.1)
    samples.append(dict(seconds=time.monotonic()-start,probe_status=p.poll(),vm_services=[usage(pid) for pid in sorted(services()-prior)]))
    (root/(name+'-samples.json')).write_text(json.dumps(samples,indent=2)+'\n')
    if p.poll() is not None:break
   print(name,'exit',p.wait(timeout=12),flush=True)
  finally:
   if p.poll() is None:p.terminate();p.wait(timeout=5)
 print((root/(name+'.log')).read_text(),flush=True)
 if name.endswith('shim'):
  d,m=attach(img)
  try:
   for capture in [*m.glob('SCREEN*'),*m.glob('NETLOG.TXT')]:
    shutil.copyfile(capture,root/capture.name)
   f=m/'SHIMLOG.TXT'
   if f.exists():(root/(name+'-report.txt')).write_bytes(f.read_bytes())
   receipt=m/'WINPE-RECEIPT.TXT'
   if receipt.exists():
    (root/(name+'-winpe.txt')).write_bytes(receipt.read_bytes())
    print(receipt.read_text(),flush=True)
   else:print('NO WINPE RECEIPT',flush=True)
  finally:detach(d)
