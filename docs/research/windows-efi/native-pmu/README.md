# Native PMU and WinPE on Apple VZ

The 30-second Apple run reached WinPE user mode. A pristine clone of the
QEMU-validated receipt media produced WINPE-RECEIPT.TXT both before and after
wpeinit, reporting ARM64 Windows 10.0.26100.4349. See receipt-shim-winpe.txt.
The master had no receipt before boot. This establishes execution of Windows
userspace, not visible Setup, a usable desktop, or an installed Windows system.

The normally signed winbootprobe used -pmu with public virtio graphics and the
existing guest GOP shim. No AMFI or host security changes were needed. The
private generic-platform PMU setter changes guest PMUVer from 0 to 1; without
it, the unchanged Microsoft loader faults on PMCCNTR_EL0. Native PMU with
unmodified BltOnly GOP instead crashes the VM service. The PMU/shim combination
passes the startup receipt. The host crash's memory mapping owner remains
unresolved; do not infer a particular device from the SIGBUS alone.

Command and EFI hashes are in receipt-shim-command.json. The modified WIM hash
is 6444aab958243ca33eef0c3982943652b248b831cc721deebcc20bb74dfaf7ec;
its preparation and QEMU control are in ../winpe-receipt/. Raw media stay in
/tmp/cove-winpe-receipt-20260907 and /tmp/cove-windows-native-pmu-20260907.
receipt-run.py records the exact run procedure and relies on scratch assets;
it is a historical harness, not a standalone media builder.

The shim's reserved linear framebuffer does not forward direct Windows writes
to the host display after ExitBootServices. Rendering remains unresolved.
Private host linear-framebuffer configuration is rejected as a restricted
device. The next task is a supported guest display/output path and integration
of the tested PMU plus shim combination; neither PMU alone nor the untested
full-install path should become the default based on this receipt.

Repository text copies use LF line endings and omit trailing blank lines.
Raw scratch receipts and Windows scripts remain byte-preserved at the recorded
paths; manifest hashes refer to those raw originals, not normalized copies.

The post-wpeinit marker proves the command returned. Its exit status was not
recorded, so the receipt does not establish successful device initialization.
