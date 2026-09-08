import argparse,ctypes,json,pathlib,re,subprocess,time
parser=argparse.ArgumentParser(description="Compare 24-second file/NBD WinPE boots on macOS, sampling at 6 and 20 seconds")
parser.add_argument("image",type=pathlib.Path)
parser.add_argument("probe",type=pathlib.Path)
parser.add_argument("output",type=pathlib.Path)
parser.add_argument("--port",type=int,default=10899)
parser.add_argument("--qemu-nbd",default="/opt/homebrew/bin/qemu-nbd")
a=parser.parse_args()
if not 1<=a.port<=65535:parser.error("port must be between 1 and 65535")
source=a.image.resolve(strict=True)
probe_path=a.probe.resolve(strict=True)
base=a.output.resolve();base.mkdir(parents=True,exist_ok=True)
lib=ctypes.CDLL('/usr/lib/libproc.dylib',use_errno=True)
class Usage(ctypes.Structure):
    _fields_=[('uuid',ctypes.c_ubyte*16),('values',ctypes.c_uint64*18)]
lib.proc_pid_rusage.argtypes=[ctypes.c_int,ctypes.c_int,ctypes.c_void_p]
lib.proc_pid_rusage.restype=ctypes.c_int

def services():
    data=subprocess.check_output(['ps','-axo','pid,comm'],text=True)
    return {int(s.split(None,1)[0]) for s in data.splitlines() if s.endswith('/com.apple.Virtualization.VirtualMachine')}

def usage(pid):
    u=Usage();err=lib.proc_pid_rusage(pid,2,ctypes.byref(u))
    return dict(pid=pid,error=err,user_ns=u.values[0],system_ns=u.values[1],rss=u.values[6],read_bytes=u.values[16],write_bytes=u.values[17])

for mode in ('file','nbd'):
    out=base/('matched-'+mode);out.mkdir(exist_ok=False)
    image=out/'boot.img'
    subprocess.run(['cp','-c',str(source),str(image)],check=True)
    prior=services();server=None;probe=None
    with (out/'server.log').open('w') as slog,(out/'probe.log').open('w') as plog:
        try:
            cmd=[str(probe_path),'-efi',str(image),'-graphics','virtio','-nocap','-seconds','24']
            if mode=='nbd':
                servercmd=[a.qemu_nbd,'--persistent','--bind=127.0.0.1',f'--port={a.port}','--format=raw','--trace=enable=nbd*,file='+str(out/'nbd.trace'),str(image)]
                server=subprocess.Popen(servercmd,stdout=slog,stderr=slog)
                time.sleep(.3)
                if server.poll() is not None:raise RuntimeError('server exited')
                cmd+=['-nbd',f'nbd://127.0.0.1:{a.port}']
                (out/'server-command.json').write_text(json.dumps(servercmd)+'\n')
            (out/'command.json').write_text(json.dumps(cmd)+'\n')
            start=time.monotonic();probe=subprocess.Popen(cmd,stdout=plog,stderr=plog)
            samples=[]
            for at in (6,20):
                time.sleep(max(0,at-(time.monotonic()-start)))
                pids=services()-prior
                sample=dict(seconds=time.monotonic()-start,probe_status=probe.poll(),vm_services=[usage(pid) for pid in sorted(pids)])
                if mode=='nbd':
                    trace=(out/'nbd.trace').read_text()
                    (out/('trace-'+str(at)+'.txt')).write_text(trace)
                    requests=re.findall(r'\.type = 0x0, from = (\d+), len = (\d+)',trace)
                    replies=re.findall(r'nbd_co_send_simple_reply.*cookie = (\d+), error = (\d+) \(.*?\), len = (\d+)',trace)
                    sample.update(server=usage(server.pid),read_requests=len(requests),logical_read_bytes=sum(int(x[1]) for x in requests),reply_events=len(replies),reply_errors=sorted({int(x[1]) for x in replies}))
                samples.append(sample)
                (out/'samples.json').write_text(json.dumps(samples,indent=2)+'\n')
            print(mode,'probe exit',probe.wait(timeout=12),flush=True)
        finally:
            for proc in (probe,server):
                if proc is not None and proc.poll() is None:
                    proc.terminate()
                    try:proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:proc.kill();proc.wait()
