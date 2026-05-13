# Runtime Listener Smoke

This smoke test is opt-in. It starts one VM with the runtime listeners that
have host-visible surfaces, then checks each listener from the host.

Build and sign the local binary first:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Start a disposable VM with HTTP, VNC, and debug-stub listeners:

```bash
./cove -vm <vm> run -http 127.0.0.1:7777 -vnc 127.0.0.1:5901 -gdb 127.0.0.1:1234
```

In another terminal, verify the HTTP listener:

```bash
curl -fsS http://127.0.0.1:7777/healthz
```

Verify the runtime listener state through the control socket:

```bash
./cove ctl -vm <vm> vnc status
./cove ctl -vm <vm> debug-stub status
```

Verify the TCP listeners are accepting connections:

```bash
nc -vz 127.0.0.1 5901
nc -vz 127.0.0.1 1234
```

Stop the VM when finished:

```bash
./cove ctl -vm <vm> request-stop
./cove ctl -vm <vm> stop
```
