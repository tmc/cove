from pathlib import Path
import subprocess,shutil,plistlib,json,hashlib
s=Path('/tmp/cove-winpe-receipt-20260907')
w=s/'boot.wim'; base=Path('/tmp/cove-windows-20260906/winpe-tree/sources/boot.wim')
subprocess.run(['cp','-c',str(base),str(w)],check=True)
(s/'winpeshl.ini').write_bytes(b'[LaunchApps]\r\n%SYSTEMROOT%\\System32\\cmd.exe, /c %SYSTEMROOT%\\System32\\cove-receipt.cmd\r\n')
script=r'''@echo off
set phase=before-wpeinit
call :receipt
wpeinit
set phase=after-wpeinit
call :receipt
X:\sources\setup.exe
exit /b
:receipt
for %%D in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do if exist %%D:\COVEWINPE.TAG (
  echo COVE_WINPE_USERMODE_STARTED_20260907>>%%D:\WINPE-RECEIPT.TXT
  echo phase=%phase%>>%%D:\WINPE-RECEIPT.TXT
  ver>>%%D:\WINPE-RECEIPT.TXT
  echo systemroot=%SYSTEMROOT% architecture=%PROCESSOR_ARCHITECTURE%>>%%D:\WINPE-RECEIPT.TXT
  echo time=%DATE% %TIME%>>%%D:\WINPE-RECEIPT.TXT
  reg query HKLM\SYSTEM\CurrentControlSet\Control\MiniNT>>%%D:\WINPE-RECEIPT.TXT 2>&1
)
exit /b
'''
(s/'cove-receipt.cmd').write_bytes(script.replace('\n','\r\n').encode())
commands=''.join(f'add "{s/n}" "/Windows/System32/{n}"\n' for n in ['winpeshl.ini','cove-receipt.cmd'])
subprocess.run(['wimlib-imagex','update',str(w),'2'],input=commands.encode(),check=True)
img=s/'receipt-base.img'; subprocess.run(['cp','-c','/tmp/cove-windows-20260906/winpe-chainload-base.dmg',str(img)],check=True)
r=subprocess.run(['hdiutil','attach','-nomount',str(img)],capture_output=True,check=True)
# detach nomount before standard mount
subprocess.run(['hdiutil','detach',r.stdout.decode().split()[0]],check=True)
r=subprocess.run(['hdiutil','attach','-nobrowse','-plist',str(img)],capture_output=True,check=True)
a=plistlib.loads(r.stdout);e=next(e for e in a['system-entities'] if 'mount-point' in e);mnt=Path(e['mount-point'])
try:
 shutil.copyfile(w,mnt/'sources/boot.wim');(mnt/'COVEWINPE.TAG').write_text('cove WinPE startup receipt experiment 20260907\n')
finally:subprocess.run(['hdiutil','detach',e['dev-entry']],check=True)
(s/'manifest.json').write_text(json.dumps({str(p):{'size':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in [w,s/'winpeshl.ini',s/'cove-receipt.cmd']},indent=2))
