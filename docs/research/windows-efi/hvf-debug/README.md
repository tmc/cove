# QEMU HVF debugger control

The installed QEMU 11.0.1 HVF backend paused the running WinPE guest and returned
core registers through its Unix-socket GDB stub. A physical read of 64 bytes at
GPA 0 matched the EDK2 firmware file exactly. Multiprocess detach D;1 returned
OK. QMP reported running after resume. The final screenshot was black despite
a visible Setup dialog before pause, so this corrected run does not establish
post-resume display/input health.

This qualifies register/physical-memory access and acknowledged detach.
It does not yet qualify repeatable post-detach display/input health. It does not provide access to VZ state,
validate every debug operation, or establish a usable VZ display.

Exact packets and command are in the adjacent JSON files. probe.py requires
a fresh disposable disk clone, output directory, and recorded local QEMU paths.
Raw screenshot: /tmp/cove-hvf-debug-20260907/run-v2/resumed.png (not staged).
All experiment processes stopped. The first run used D after negotiating
multiprocess and got E22; after socket closure and QMP resume, that run did
show a Shift+F10 command prompt. Its partial positive must not be combined
with the second run to claim one fully passing control. The corrected control
uses D;1 and requires OK. The display discrepancy remains unresolved.

Protocol reference: https://www.qemu.org/docs/master/system/gdb.html
