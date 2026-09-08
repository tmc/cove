from pathlib import Path
import subprocess,shutil,plistlib,json,hashlib
s=Path('/tmp/cove-winpe-network-20260907');w=s/'boot.wim';img=s/'network-base.img'
assert not w.exists() and not img.exists()
subprocess.run(['cp','-c','/tmp/cove-winpe-screen-20260907/boot.wim',str(w)],check=True)
script=Path('/tmp/cove-winpe-screen-20260907/cove-receipt.cmd').read_text()
script=script.replace('set phase=after-wpeinit\ncall :receipt','set phase=after-wpeinit\ncall :receipt\nfor %%D in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do if exist %%D:\\COVEWINPE.TAG call :network %%D:')
script+=r'''
:network
drvload %1\netkvm\netkvm.inf >%1\NETLOG.TXT 2>&1
echo drvload=%errorlevel%>>%1\NETLOG.TXT
wpeutil InitializeNetwork >>%1\NETLOG.TXT 2>&1
echo InitializeNetwork=%errorlevel%>>%1\NETLOG.TXT
ipconfig /all >>%1\NETLOG.TXT 2>&1
pnputil /enum-devices /class Net >>%1\NETLOG.TXT 2>&1
exit /b
'''
(s/'cove-receipt.cmd').write_bytes(script.replace('\n','\r\n').encode())
subprocess.run(['/Users/tmc2/.local/homebrew/bin/wimlib-imagex','update',str(w),'2'],input=f'add "{s}/cove-receipt.cmd" "/Windows/System32/cove-receipt.cmd"\n'.encode(),check=True)
subprocess.run(['cp','-c','/tmp/cove-winpe-screen-20260907/screen-base.img',str(img)],check=True)
d=plistlib.loads(subprocess.check_output(['hdiutil','attach','-nobrowse','-plist',str(img)]));m=next(Path(e['mount-point']) for e in d['system-entities'] if 'mount-point'in e)
try:
 assert not (m/'WINPE-RECEIPT.TXT').exists()
 shutil.copyfile(w,m/'sources/boot.wim');shutil.copytree(s/'netkvm',m/'netkvm')
finally:subprocess.run(['hdiutil','detach',d['system-entities'][0]['dev-entry']],check=True)
(s/'manifest.json').write_text(json.dumps({str(p):{'size':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in [w,s/'cove-receipt.cmd',*sorted((s/'netkvm').iterdir())]},indent=2)+'\n')
