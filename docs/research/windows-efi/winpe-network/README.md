# WinPE VirtIO networking on VZ

The ARM64 Windows 11 NetKVM driver loaded from the cached VirtIO ISO. Windows
reports the Red Hat VirtIO Ethernet Adapter Started, with DHCP address
192.168.64.53/24 and gateway/DHCP server 192.168.64.1. The MAC matches the
probe's randomly generated address. Native PMU and the existing GOP shim were
retained; the added device was a public VZ NAT NIC. Bounded run: 30 seconds.

This establishes driver binding and DHCP, not application transport or live
screen streaming. No inference depends on earlier KDNET capability behavior.
Next is a guest-to-host frame upload and an input response on this NAT path.

The probe's -nat flag is opt-in and mutually exclusive with -pcap. Driver files
stay outside git under /tmp/cove-winpe-network-20260907/netkvm. Their hashes and
ISO provenance are recorded in manifest.json and driver-iso.json. Driver loading
used drvload after wpeinit, then wpeutil InitializeNetwork, ipconfig and pnputil.
The command output establishes success; intended numeric exit-status echo lines
were not recovered and are not evidence. prepare.py and apple.py preserve the
exact tested scratch procedure, fixed paths and fresh-clone requirements.

apple-network.txt is a whitespace-normalized copy of raw NETLOG.TXT in scratch.
All experiment VMs are stopped. Host probe was built and signed with ordinary
virtualization entitlements; targeted probe/filehandle tests passed.
