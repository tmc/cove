# Guest-side WinPE screen capture

Both QEMU and Apple VZ produced three successful GDI captures. The recovered
Apple PNGs show Windows Setup at 1920x1200, including the expected missing
install.wim dialog. The same guest program and modified WIM passed QEMU first.
Apple used native PMU plus the existing GOP shim, ordinary virtualization
entitlements, and a fresh scratch disk clone. Each run ended after 30 seconds.

This establishes guest rendering and capture. PNGs were recovered from the FAT
disk only after the VM stopped; no live transport or input channel is proven.
It does not establish a full Windows installation or host VZ framebuffer access.

screen_windows.go calls GetDC, CreateCompatibleBitmap, BitBlt and GetDIBits,
then writes three PNGs after startup. Runtime is pinned to one OS thread for
GDI ownership. The bitmap is deselected before GetDIBits, and the 32-bit BGR
pixels are converted to RGBA with opaque alpha. API failures are logged.
Reference: [Microsoft screen capture](https://learn.microsoft.com/en-us/windows/win32/gdi/capturing-an-image).

Build outside the repository:

```
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/screen.exe screen_windows.go
```

This builds a Windows PE executable; macOS virtualization codesigning applies
to the host probe, not this guest executable. prepare.py, apple.py and recover.py
preserve the tested scratch procedure and fixed paths. Do not rerun over old
media. The original WIM and receipt master remain unchanged. Image and binary
hashes are in manifest.json and images.json; binary artifacts remain in scratch.

Next: establish guest/host transport and live input using this capture path.
The probe currently uses no guest NIC in these runs. Network/agent attachment
and a Windows network driver must be qualified separately.
