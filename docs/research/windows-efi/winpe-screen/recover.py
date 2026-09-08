from pathlib import Path
import subprocess,plistlib,sys,shutil
img=Path(sys.argv[1]);out=Path(sys.argv[2]);out.mkdir(exist_ok=True)
d=plistlib.loads(subprocess.check_output(['hdiutil','attach','-nobrowse','-plist',str(img)]))
m=next(Path(e['mount-point']) for e in d['system-entities'] if 'mount-point'in e)
try:
 for pattern in ['SCREEN*','WINPE-RECEIPT.TXT','SHIMLOG.TXT']:
  for p in m.glob(pattern):
   shutil.copyfile(p,out/p.name)
   if p.suffix.upper()=='.TXT':print(p.name,p.read_text())
finally:subprocess.run(['hdiutil','detach',d['system-entities'][0]['dev-entry']],check=True)
