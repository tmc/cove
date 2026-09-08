# Native PMU host crash localization

Read-only inspection; no VM runs or privileged attach.

Commands:
```
nm -n /System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine
otool -tvV /System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine
```
Full outputs: service-nm.txt and service-disassembly.txt beside this report.
Crash JSON parsed with Python json.loads(text.split("\n",1)[1]) from ~/Library/Logs/DiagnosticReports/Retired/com.apple.Virtualization.VirtualMachine-2026-09-07-{203100,203213}.ips.

## Virtio run SIGBUS

203100 crash reports cpu-0 thread, _platform_memmove+424, ESR description `(Data Abort) byte write Permission fault`, FAR and x0 both 0x109258000. vmRegionInfo identifies that address as first byte of a 1024K `shared memory` region with `r--/rwx` protection. Its owner/device is not named.

Return addresses, image-relative: 0x3d1f88, 0x3cf804, 0x3b68b4, 0x28e12c, 0xb1394. nm identifies only redacted functions for these.

Literal disassembly evidence:
```
1000b1384 bl 0x1004125ec ; _hv_vcpu_run
1000b1390 bl 0x10028ce98
10028e114 add x1, x1, #0xd61 ; "Handle this, data abort iss is %08x"
10028e128 bl 0x1003b5de8
1003b68a8 add x2, sp, #0x70
1003b68b0 blraa x8, x16
1003cf7f8 mov x2, x21
1003cf7fc mov x3, x22
1003cf800 bl 0x1003d1ee8
1003d1f74 mov x1, x22
1003d1f78 mov x2, x21
1003d1f7c mov x3, x23
1003d1f84 blraa x8, x16
```
This places the crash on a vCPU exit/guest-memory-copy path, consistent with servicing a guest write to readonly host backing. It does not establish graphics versus storage. The 1MiB mapping size and shared-memory type do not identify ownership; do not infer a framebuffer, disk buffer, or ROM solely from them.

## Linear framebuffer run SIGTRAP

203213 stack includes image offsets 0x40f770, 0x40f858, 0x335158. Literal instructions:
```
10040f76c bl 0x100411e4c ; __os_crash
10040f770 brk #0x1
100335144 add x0, x0, #0x9c0 ; "Restricted devices require the com.apple.private.virtualization entitlement."
100335148 bl 0x10040f774
100335150 add x0, x0, #0xa0d ; "Unable to create a virtual machine with restricted devices."
100335154 bl 0x10040f774
100335158 tbnz w0, #0x4, 0x100335160
```
This is an explicit restricted-device rejection, distinct from the later virtio memmove fault.
