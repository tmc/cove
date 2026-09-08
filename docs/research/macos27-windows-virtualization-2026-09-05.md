# Does macOS 27 enable Windows guests without QEMU?

Follow-up (2026-09-06): [guest-side experiment plan](windows-boot-experiments-2026-09-06.md).
The entitlement rejection below is established. Whether a linear GOP alone permits
Windows to boot remains untested; the conclusions about its sufficiency should be
read as hypotheses. The follow-up measures GOP inside the guest and validates media
before attempting a shim or changing a spare host's security policy.

Researched 2026-09-05 on macOS 27.0 (build 26A5425a), Apple M4 Pro, arm64.

**Short answer: no. macOS 27 adds UEFI Secure Boot, but Apple scoped it to Linux — it
enrolls the shim CA, not the CA that signs Windows Boot Manager. There is still no TPM of
any kind, and the GOP blocker cove already documented is unchanged. The private APIs that
would each solve a piece of this all exist, are fully implemented in the VMM, and are all
refused at VM start: the VM service checks the calling process for
`com.apple.private.virtualization` and rejects any configuration containing a "restricted
device". That gate is located and read out of the service binary below — it is proven, not
inferred. QEMU remains the only Windows path.**

Everything below was verified against this machine's SDK headers, the shared-cache
framework binary, and runtime probes — not from documentation alone.

## What macOS 27 adds to Virtualization.framework

Headers carrying `API_AVAILABLE(macos(27.0))`:

| Area | API |
| --- | --- |
| UEFI Secure Boot | `VZEFIVariableStore` gains enable/disable/reset plus signature enrollment; new `VZEFISignature`, `VZEFISignatureList`, `VZEFISignatureX509Certificate`, `VZEFISignatureSHA256Hash`, `VZEFISignatureDatabaseConfiguration` |
| Custom devices | `VZCustomVirtioDevice` + configuration, provider, and delegate types |
| USB | `VZUSBPassthroughDeviceConfiguration` (wraps `AAUSBAccessory`) |
| Storage | `VZDiskBlockDeviceStorageDeviceAttachment` |
| Virtio internals | `VZVirtioSharedMemoryRegion`, `VZVirtioFeatureSet`, `VZNegotiatedVirtioFeatureSet`, `VZVirtioQueue`, `VZVirtioQueueElement` |
| Misc | `VZGuestProvisioningOptions`, `VZGuestMemoryMapping`, `VZVirtualMachineConfiguration.label` |

`VZCustomVirtioDevice` is the notable promotion: implementing a virtio device in the
host process was previously private SPI (`_VZCustomVirtioDevice`), and is now public.
You supply `deviceID`, `PCIClassID`, `PCISubclassID`, and `virtioQueueCount`, and the
framework instantiates the device when the VM is created.

## Secure Boot ships the Linux CA, not the Windows CA

This is the decisive detail. `enrollDefaultSecureBootSignaturesWithError:` was run
against a fresh variable store and the resulting databases read back:

```
===== KEK =====   Microsoft Corporation KEK CA 2011
                  Microsoft Corporation KEK 2K CA 2023
===== db  =====   Microsoft Corporation UEFI CA 2011
                  Microsoft UEFI CA 2023
===== dbx =====   26 SHA-256 revocation hashes
```

The `db` set contains only the *third-party* UEFI CA — the one that signs `shim`.
**`Microsoft Windows Production PCA 2011` (and its `Windows UEFI CA 2023` successor),
which sign `bootmgfw.efi`, are absent.** Apple's own documentation for the method says
so plainly:

> This allows Microsoft-signed Linux distributions to boot with Secure Boot enabled.

So Windows Boot Manager fails signature verification under the default enrollment.

That is recoverable, though: custom enrollment works. Building a
`VZEFISignatureX509Certificate` from an arbitrary DER cert, wrapping it in a
`VZEFISignatureList`, and passing it through `enrollSecureBootSignatures:` succeeded
and read back correctly. Adding the Windows Production CA by hand — or simply leaving
Secure Boot off — is available. Secure Boot is therefore neither the blocker nor the
enabler for Windows; it is orthogonal.

## There is still no TPM

Searched the macOS 27 SDK headers, the exported symbol table of the shared-cache
framework image, and the ObjC runtime. There is no `VZTPMDeviceConfiguration`, no
private `_VZTPM*` class, and no TPM string anywhere in the binary. Zero hits.

Consequences for a Windows 11 guest: the setup TPM check needs the usual bypass, and
BitLocker, Windows Hello, measured boot, and device attestation are unavailable.
Windows 11 will run without a TPM; it just runs degraded.

## The real blocker is the GOP pixel format, and it predates macOS 27

`internal/windows/windows.go` (currently `//go:build ignore`, note dated 2026-02-08)
records the actual failure:

> The Windows Boot Manager (bootmgfw.efi) requires a linear framebuffer via EFI
> Graphics Output Protocol (GOP) with PixelBlueGreenRedReserved8BitPerColor format.
> Apple's built-in EFI firmware (VZEFIBootLoader) only provides PixelBltOnly GOP
> through VirtioGpuDxe, which Windows refuses.

macOS 27 does **not** change this in public API. `VZEFIBootLoader` is unchanged apart
from the variable-store Secure Boot methods, and the framework's firmware volumes
(`Resources/VZG11.fd` — an EDK2 volume, `_FVH` signature at 0x28 — and
`Resources/VZG21.fd`) yield no readable strings, consistent with compressed sections.
`VZEFIBootLoader` exposes no property for substituting firmware.

What does exist — and still exists in macOS 27 — is the private class
`_VZLinearFramebufferGraphicsDeviceConfiguration`, confirmed loadable at runtime with
`-initWithBackingStoreSize:`, `-setBackingStoreSize:`, and
`-makeGraphicsDeviceForVirtualMachine:graphicsDeviceIndex:`. cove already wires this up:
`cmd/cove/windows.go` exposes it as `-windows-graphics linear-framebuffer` via
`vzkit/framebuffer.SetLinearFramebufferGraphicsDevice`. The default remains `virtio`.

Other private classes relevant to a Windows effort, all present in macOS 27 — but see
[Do the private APIs actually work?](#do-the-private-apis-actually-work) below, because
presence is not usability:

- `_VZCustomMMIODeviceConfiguration` — host-implemented MMIO devices, with an
  in-process delegate provider (`_VZCustomMMIODeviceDelegateProvider`). A TPM 2.0 CRB
  interface is MMIO-based, so this is the shape a userspace vTPM would take.
- `_VZBinaryBootLoader` — loads an arbitrary binary at a given entry point.
- `VZEFIBootLoader._setROMImageURL:` — substitutes the EFI ROM image, i.e. OVMF instead
  of Apple's firmware, which would sidestep the GOP problem outright.
- `_VZUSBOpticalDriveDeviceConfiguration` — a real CD-ROM for install media.
- `_VZPCIPassthroughDeviceConfiguration`, `_VZVNCServer`, `_VZGDBDebugStub`.
- `VZGenericPlatformConfiguration._setGuestType:` — a dead end. Only two guest types are
  exported, `_VZVirtualMachineGuestTypeLinux` (the default) and
  `_VZVirtualMachineGuestTypeCoprocessor`. There is no Windows guest type.

## Do the private APIs actually work?

**No — and the reason is now proven rather than inferred.** The VM XPC service refuses any
configuration containing a "restricted device" unless the calling process holds
`com.apple.private.virtualization`, which AMFI will not grant a third-party binary while
SIP is on.

Tested with minimal ObjC harnesses ad-hoc signed against cove's own
`internal/autosign/vz.entitlements` (`com.apple.security.virtualization`):

| Configuration | validate | start |
| --- | --- | --- |
| Plain EFI VM (control) | OK | **STARTED** |
| `VZVirtioGraphicsDeviceConfiguration` (public) | OK | **STARTED** |
| `VZCustomVirtioDevice` + delegate provider (public, new in 27) | OK | **STARTED** |
| `_VZLinearFramebufferGraphicsDeviceConfiguration` | OK | **FAILS** — `VZErrorDomain` code 1 |
| `VZEFIBootLoader._setROMImageURL:` → OVMF | OK | **FAILS** |
| `_setROMImageURL:` → Apple's own `VZG11.fd`/`VZG21.fd` | OK | **FAILS** |
| `_VZCustomMMIODeviceConfiguration` + `_VZCustomMMIODeviceDelegateProvider` | OK | **FAILS** |

Every failure is the same opaque error, with no `NSUnderlyingError`:

```
domain=VZErrorDomain code=1
desc=Internal Virtualization error. The virtual machine failed to start.
```

### Finding the gate

The failing starts share one observable: the VM service logs a single `E`-level line whose
payload is `<private>`, then exits cleanly — no crash report. Unredacting os_log is not
available on macOS 27 (`log config --mode private_data:on` returns `Invalid Modes`; the
`private_data` key was removed and now requires a configuration profile), so the message
was recovered statically instead.

`com.apple.Virtualization.VirtualMachine.xpc` builds a bitmask of the *client's*
entitlements at connection time (one helper, seven checks):

| bit | entitlement |
| --- | --- |
| 0 | `com.apple.security.virtualization` — also set when bit 1 is present |
| 1 | `com.apple.private.virtualization` |
| 2 | `com.apple.vm.networking` |
| 3 | `com.apple.private.ggdsw.GPUProcessProtectedContent` |
| 4 | `com.apple.private.virtualization.security-research` |
| 5 | `com.apple.private.virtualization.private-vsock` |
| 6 | `com.apple.private.usb.capture` |

The mask is stored on the VM configuration object at `+0xa18`. A single gate function
tests bit 1:

```
0x100335118:  tbz  w0, #0x1, 0x100335130   ; bit 1 set -> return, allowed
0x10033512c:  retab
0x100335130:  adrp/add "com.apple.virtualization"
0x100335138:  bl   _os_variant_has_internal_content
0x10033513c:  cbz  w0, 0x10033514c          ; customer build -> skip detailed message
0x100335144:  adrp/add "Restricted devices require the com.apple.private.virtualization entitlement."
0x100335150:  adrp/add "Unable to create a virtual machine with restricted devices."
```

That `_os_variant_has_internal_content` check is why the log line is redacted on a customer
build: the specific message is emitted only on Apple-internal builds.

Roughly sixty call sites load `[config+0xa18]` and branch into that gate. Custom MMIO is
one of them, and the guard is exactly "is the list non-empty":

```
0x1003215ac:  ldr  x21, [x1, #0x3c0]   ; custom_mmio_devices.begin
0x1003215b0:  ldr  x25, [x1, #0x3c8]   ; .end
0x1003215b4:  cmp  x21, x25
0x1003215b8:  b.eq 0x1003215cc         ; empty -> skip the check entirely
0x1003215bc:  ldr  w0, [x20, #0xa18]   ; entitlement mask
0x1003215c0:  bl   0x100310238         ; -> the gate above
```

The linear framebuffer is gated by the same function, called on entry to the routine that
constructs `com.apple.virtualization.graphics.linear-framebuffer-device`. That is why the
LFB and MMIO failures produce an identical breadcrumb — they share the code path.

The ROM override (`rom_binary`) reaches the gate through a second entry point at
`0x100335158`, which first allows bit 4 and otherwise falls into the bit-1 test:

```
0x100335158:  tbnz w0, #0x4, 0x100335160   ; security-research entitlement -> allowed
0x10033515c:  b    0x100335118             ; else the com.apple.private.virtualization gate
```

So substituting the EFI ROM is permitted by either
`com.apple.private.virtualization.security-research` or `com.apple.private.virtualization`.
Both are `com.apple.private.*`, so both are AMFI-restricted; the practical result is the
same.

An independent check that this reading of the bitmask is right: bit 2
(`com.apple.vm.networking`) gates the `vmnet` network backends, which matches Apple's
documented, publicly known requirement that bridged networking needs that entitlement.

### Why the error is opaque: the framework only pre-checks *public* entitlements

A falsifiable prediction of the model above, tested and confirmed with a twist worth
recording. Bit 2 is `com.apple.vm.networking`, which Apple *documents* as the requirement
for `VZBridgedNetworkDeviceAttachment`. Configuring bridged networking without it does not
produce the opaque internal error — it is caught client-side, at validation, by name:

```
$ ./bridge nat        # control
mode=nat  validate OK
=== RESULT(nat) === STARTED

$ ./bridge bridged
bridged interfaces visible: 1
  en0 (Wi-Fi)
VALIDATE-FAIL: domain=VZErrorDomain code=2
  Invalid virtual machine configuration. Using VZBridgedNetworkDeviceAttachment in a
  process that lacks the "com.apple.vm.networking" entitlement.
```

That is the whole explanation for the asymmetry in failure quality:

| entitlement | pre-checked in-process by the framework? | failure |
| --- | --- | --- |
| `com.apple.vm.networking` (public, requestable) | **yes** | named `VZErrorDomain` code 2 at *validate* |
| `com.apple.private.virtualization` (restricted) | **no** | opaque `VZErrorDomain` code 1 at *start* |

`validateWithError:` runs entirely client-side and knows nothing about restricted devices —
Apple never wrote a friendly pre-check for an entitlement no third party is expected to
hold. The bit-2 test inside the VM service is a defense-in-depth backstop behind the
client-side check; the bit-1 test is the *only* check there is. Hence a silent internal
error rather than a diagnosable one.

### What is *not* the problem

Several plausible explanations were tested and ruled out:

1. **Not a device-model limitation.** The custom MMIO device model is real and complete.
   Setting `irqs` produces a genuine validation error — *"MMIO device interrupt number
   must be between 96 and 127 or between 512 and 639"* — and the service contains the
   full implementation (`VzCore::Hardware::CustomMmioDevice`, `mmio_read`/`mmio_write`
   messengers, save/restore, IRQ pulse). The device is even successfully constructed:
   the delegate's `customMMIODeviceConfiguration:didCreateDevice:` fires and hands back a
   live `_VZCustomMMIODevice`. Only the subsequent start is refused.
2. **Not plugin-only.** An earlier reading of this — that `_VZCustomMMIODeviceProvider` is
   purely an XPC plugin handle requiring `com.apple.private.virtualization.plugin` — was
   wrong. `_VZCustomMMIODeviceDelegateProvider` is an in-process delegate provider
   (`-initWithDeviceQueue:delegate:`), the direct analogue of the public
   `VZCustomVirtioDeviceDelegateProvider`. The plugin variant
   (`_VZCustomMMIODevicePluginProvider`) is a separate, additional option.
3. **Not the provider endpoint.** The provider's `_endpoint` ivar is nil after init — but
   it is *also* nil on the public `VZCustomVirtioDeviceDelegateProvider`, which starts
   fine. It is populated at VM start, so nil-at-init means nothing.
4. **Not address or IRQ placement.** Custom MMIO fails at every base address tried
   (`0x09000000`, `0x10000000`, `0x20000000`, `0x30000000`, `0xFED40000`, `0x100000000`,
   `0x200000000`), every legal IRQ (96, 127, 512, 639), with and without
   `writeSynchronously` and `supportsSaveRestore`, and under all guest types. Consistent
   with a gate that runs before placement.
5. **Not guest type.** `_setGuestType:` to `_VZVirtualMachineGuestTypeCoprocessor` fails
   even with no MMIO device present, so guest type is not the discriminator.
6. **`validateWithError:` is not the gate.** It accepted a ROM URL pointing at a text file
   containing `not firmware`. Validation never reads the image; rejection happens at start.
7. **The ROM override fails even when handed Apple's own firmware**, in place, unmodified.
   So the failure is not "OVMF is the wrong shape for Apple's virtual platform" (it is,
   separately — it is a 64 MB image built for the QEMU `virt` machine). The override path
   itself is closed.

Adding `com.apple.private.virtualization` to the entitlements plist does not help: the
binary is **SIGKILLed at launch** (exit 137). `com.apple.private.*` entitlements are
restricted, and AMFI honors them only for Apple-provisioned binaries. SIP is enabled on
this machine, which is the supported configuration.

### This breaks cove's existing `-windows-graphics linear-framebuffer`

The finding reproduces in cove itself, not just the harness. Against a scratch VM with a
signed build of `./cmd/cove`:

```
$ cove -windows -headless -windows-graphics virtio -vm lfbprobe run
VM started successfully

$ cove -windows -headless -windows-graphics linear-framebuffer -vm lfbprobe run
  Configuration valid
  Starting virtual machine...
error: vm start: vm entered error state during startup
```

So `-windows-graphics linear-framebuffer` cannot work on any normally-signed build. The
flag should either be removed, or fail fast with an explanation, rather than surfacing
as a generic startup error. `vzkit/framebuffer.SetLinearFramebufferGraphicsDevice` only
checks that the *class exists* — which it does — so the failure is deferred to VM start
and reads as a mysterious internal error.

## Independent corroboration of the gate

The disassembly above was done blind; published third-party work confirms it from the
outside.

**The gate is real and the entitlement is the key.** The `Code-Hex/vz` Go bindings
document enabling the private GDB debug stub — `_setDebugStub:`, one of the restricted
devices in the same list as custom MMIO — and the recipe is exactly the bit-1 entitlement
plus disabling AMFI:

> The entitlement needed is `com.apple.private.virtualization` set to `true`.
> 1. Boot into recovery mode and run `csrutil disable`
> 2. `sudo nvram boot-args="amfi_get_out_of_my_way=1 ipc_control_port_options=0"`
> 3. Include the private virtualization entitlement in your app's entitlements file
> 4. `codesign` and build

— [Code-Hex/vz wiki, "How to debug guest with virtualization framework"](https://github.com/Code-Hex/vz/wiki/How-to-debug-guest-with-virtualization-framework)

That is a working, reproducible case of a restricted device starting *with* the
entitlement and not without it. It resolves the one path this audit could not test
directly: **the gate can be defeated, but only by disabling AMFI and SIP.**

The same recipe appears independently for a different private VZ surface
(`_VZMacSerialNumber`, `_machineIdentifierWithSerialNumber:`), using the other spelling of
the boot-arg:

> `nvram 40A0DDD2-77F8-4392-B4A3-1E7304206516:boot-args='amfi=0x80'` — "disables AMFI's
> security restrictions, including allowing us to sign arbitrary entitlements on our
> applications" (also known as `amfi_get_out_of_my_way=0x1`)

— [khronokernel, "Apple Silicon and Virtual Machines"](https://khronokernel.com/macos/2023/08/18/AS-VM-SERIAL.html)

**Apple's own answer on obtaining it:** the entitlement "is restricted to developers of
virtualization software" and requires contacting an Apple representative — i.e. it is not
something a project like cove can request through the normal capability flow.

**Bit 4 (`com.apple.private.virtualization.security-research`) is not a second key to the
restricted devices.** Counting call sites settles it. The gate has two entry points:
`0x100335118` tests bit 1 only, and `0x100335158` is a two-instruction prologue that
accepts bit 4 and otherwise falls through to the bit-1 test:

```
0x100335158:  tbnz w0, #0x4, 0x100335160   ; security-research -> allowed
0x10033515c:  b    0x100335118             ; else require bit 1
0x100335160:  ret
```

Sixty-four call sites reach the bit-1-only entry (59 through the thunk at `0x100310238`,
5 directly). **Exactly one** reaches `0x100335158` — and it is not firmware or graphics,
it is a network backend:

```
0x10034bbf0:  ldr  w0, [x8]
0x10034bbf4:  bl   0x100335158
0x10034bc24:  "com.apple.virtualization.network-backend.vmnet"
0x10034bc48:  "host_only"
```

So `security-research` buys a host-only vmnet backend and nothing else. Both devices cove
would want go to the bit-1-only entry:

| device | gate call | entry | accepts |
| --- | --- | --- | --- |
| custom MMIO (`0x1003215bc`) | `bl 0x100310238` | `0x100335118` | bit 1 only |
| linear framebuffer (`0x100348ed4`) | `bl 0x100310238` | `0x100335118` | bit 1 only |

The shipped boot ROMs also enumerate every guest platform the
VMM knows how to boot. The VM service references four by name; three are on disk:

```
$ ls /System/Library/Frameworks/Virtualization.framework/Versions/A/Resources/AVP*
AVPBooter.vmapple2.bin      AVPBooter.vresearch1.bin      AVPSEPBooter.vresearch1.bin

$ strings -a vmxpc | grep -E 'AVP(SEP)?Booter' | sort -u
AVPBooter.vmapple1
AVPBooter.vmapple2
AVPBooter.vresearch1
AVPSEPBooter.vresearch1
```

`vmapple1`/`vmapple2` are the two generations of the Apple Virtual Platform used for macOS
guests. `vresearch1` — which uniquely also ships a *SEP* booter, i.e. a virtualized Secure
Enclave — is the research platform, and it is the only thing bit 4 unlocks. SIP exposes the
matching host-side switch, `csrutil allow-research-guests` (Recovery OS only), which is
absent from every published Windows-virtualization discussion because it has nothing to do
with Windows. There is no fourth ROM: the restricted path leads to Apple's own research
guest, not to a general-purpose firmware you could point at `bootmgfw.efi`. Linux guests do
not use these ROMs at all — they go through `VZEFIBootLoader` and Apple's own EFI, which is
where the GOP blocker lives.

**Nobody has done Windows this way.** Searching for prior art on Windows-on-ARM under
Virtualization.framework turns up only QEMU-based work — Alexander Graf's hypervisor
patches and [UTM](https://mac.getutm.app/). There is no published instance of Windows
booting on Virtualization.framework, with or without private APIs.

## `csrutil allow-research-guests` is the wrong axis; AMFI relaxation is the right one

These are two different switches, and only one of them is on the path to Windows.

**`csrutil allow-research-guests` gates the AVP guest type, not restricted devices.** It is
a host SIP setting (Recovery OS only) that lets the VMM accept `guestType == 1` — the Apple
Virtual Platform research guest that boots the `vresearch1` Image4 chain (LLB/iBSS/iBEC/iBoot,
SPTM/TXM, a virtualized SEP, and a kernelcache). That is an Apple-silicon boot, not UEFI. The
host-side refusal on that path is a *hardware* check, not the SIP bit:

```
0x100307630:  ldr  w8, [x26, #0x150]     ; guest type
0x10030763c:  cbz  w8, 0x100307750       ; 0 -> ordinary guest
0x100307644:  b.ne 0x100307898           ; != 1 -> elsewhere
0x100307654:  bl   _MGGetSInt32Answer    ; @"SoCSKU"
0x10030768c:  "AVP guests are not supported on this host."
```

Windows would boot through `VZEFIBootLoader` on the generic platform, which never reaches
this code. Turning on research guests changes nothing about the GOP pixel format, the
missing TPM, or the restricted-device gate. It is orthogonal.

**What actually opens the gate is being able to sign `com.apple.private.virtualization`
(bit 1), and that is an AMFI question.** `com.apple.private.*` entitlements are restricted:
AMFI only honors them on Apple-signed binaries. Relax AMFI and you can self-grant them, at
which point all 64 restricted-device call sites — custom MMIO and the linear framebuffer
included — open at once.

**There is working prior art.** [`Lakr233/vphone-cli`](https://github.com/Lakr233/vphone-cli)
runs iOS guests on Virtualization.framework and ships this entitlement set:

```xml
<key>com.apple.security.virtualization</key>            <true/>
<key>com.apple.private.virtualization</key>             <true/>
<key>com.apple.private.virtualization.security-research</key> <true/>
<key>com.apple.vm.networking</key>                      <true/>
```

It self-signs bit 1 and it works — its tested-hosts table includes macOS 27.0b2 on
Mac16,6/8/11/12. Its documented host setup is either full SIP disable plus
`amfi_get_out_of_my_way=1`, or `csrutil enable --without debug` plus
[`amfidont`](https://github.com/zqxwce/amfidont) (a `pip`-installable daemon that attaches
to `amfid` and spoofs the signature verdict). Note that both options pair
`allow-research-guests` with an AMFI relaxation — the SIP bit alone grants no entitlement.
vphone needs the research-guest bit because it boots AVP guests. cove would not.

**What this changes, and what it does not.** The gate is passable on a relaxed host, so the
verdict below is about the shipping configuration, not about physics. On such a host the
linear framebuffer becomes testable, and it is the one device that plausibly addresses the
GOP blocker — `_VZLinearFramebufferGraphicsDeviceConfiguration` is by name a linear
framebuffer, which is what `PixelBlueGreenRedReserved8BitPerColor` describes. That
experiment has not been run here, because it costs a Recovery reboot and a permanent
downgrade of the host's code-signing enforcement. It is worth being clear about the cost:
`amfi_get_out_of_my_way=1` disables entitlement validation machine-wide, for every process,
not just cove. Nothing that depends on it can ship as a supported path for cove's users —
at best it is a research configuration for answering the GOP question one way or the other.

There is no reason for cove to implement its own `amfidont`. It exists, it is a host
security tool rather than a VM feature, and writing another one would not change any of the
findings above.

### The SIP option space, narrowest first

`csrutil enable --without <feature>` takes six features, read out of the table at
`__DATA:0x24a88` in `/usr/bin/csrutil` (six 24-byte entries of `{name, label, handler}`):

| feature | label |
| --- | --- |
| `kext` | Kext Signing |
| `fs` | Filesystem Protections |
| `debug` | Debugging Restrictions |
| `dtrace` | DTrace Restrictions |
| `nvram` | NVRAM Protections |
| `basesystem` | BaseSystem Verification |

Only `debug` is on the path to a restricted entitlement, and only indirectly: clearing
Debugging Restrictions permits `task_for_pid` against Apple platform binaries, which is what
lets `amfidont` attach to `amfid` and spoof the signature verdict for an allowlisted path.
The other five are irrelevant here — note in particular that `--without nvram` only lets you
*write* `boot-args`; AMFI still declines to honor `amfi_get_out_of_my_way` unless the local
policy is Permissive. `bputil` confirms the tiering: `-a/--disable-boot-args-restriction`
"automatically downgrades to Permissive Security", whereas a custom SIP configuration needs
only Reduced Security.

That gives a ladder:

| option | mechanism | security mode | blast radius |
| --- | --- | --- | --- |
| none | — | Full | cannot self-sign a restricted entitlement at all |
| `--without debug` + `amfidont` | patch `amfid`'s verdict for one path | Reduced | AMFI stays on; only allowlisted binaries pass |
| `csrutil disable` + `amfi_get_out_of_my_way=1` | disable entitlement validation | Permissive | machine-wide, every process |

**The important part is that none of this has to happen on the working system.** On Apple
silicon the security policy is per-OS-install LocalPolicy, not a global setting: `csrutil` in
Recovery prompts "Pick a macOS installation", and `bputil -e` displays every policy while
`-v <vuid>` targets one. This machine already carries three installs on two containers —
`Macintosh HD` (disk3, macOS 27) plus `Backup Partition` and `MacOS 26` (disk4) — so the
experiment can be run against a secondary install, or an external boot volume, with the
primary left at Full Security. That is the configuration to use: relax one spare install to
Reduced Security with `--without debug`, run `amfidont` there, leave the daily system alone.

On this machine `sudo bputil -d -e` reports three volume groups, all identical and none
pre-relaxed — `Security Mode: Full`, `SIP Status: Enabled`, `Boot Args Filtering: Enabled`:

| vuid | volume | `love` | macOS |
| --- | --- | --- | --- |
| `9853EB2B-…` | Macintosh HD (booted) | 26.1.425.5.1 | 27 |
| `F3461E75-…` | MacOS 26 | 25.6.71.0.0 | 26 |
| `540D93C3-…` | Backup Partition | 24.2.91.2.0 | 15.2 |

Neither spare runs macOS 27, which raises the obvious objection: does testing on the macOS 26
install answer a macOS 27 question? For this experiment, yes — the relevant surfaces are
identical. Both VM service binaries carry the same gate and the same two devices:

```
$ strings -a <26 or 27 VM service> | grep -iE 'restricted device|linear.?framebuffer|custom-mmio'
Restricted devices require the com.apple.private.virtualization entitlement.
Unable to create a virtual machine with restricted devices.
com.apple.virtualization.graphics.linear-framebuffer-device
com.apple.virtualization.custom-mmio-device
N6VzCore8Hardware23LinearFramebufferDeviceE
```

So the experiment runs against the `MacOS 26` install (`F3461E75`) with no change to
`Macintosh HD` and no OS upgrade of the spare. That is also consistent with the GOP blocker
predating macOS 27 — a macOS 26 result is directly informative either way, and a positive
result would only need confirming on 27 afterwards.

**A VM is not an option for this.** `VZGenericPlatformConfiguration.nestedVirtualizationSupported`
reports YES on this M4 Pro, but nested virtualization exists only on the *generic* platform —
`VZMacPlatformConfiguration` has no such property — so a macOS guest cannot itself run
Virtualization.framework. (A Linux guest with nested virtualization could run QEMU/KVM, but
that is the QEMU path again, one level down.)

## Removing the graphics device does not help either

The one remaining public-API avenue: does `bootmgfw.efi` *require* a GOP, or does it only
reject `PixelBltOnly`? If the former, dropping the graphics device changes nothing; if the
latter, a headless boot might get further.

Tested by booting the existing Windows install media (`efi-boot.img`, attached read-only)
with a copy of the VM's real EFI variable store, so the Windows boot entry is present.
Progress was measured as bytes read from disk by the VM service process
(`proc_pid_rusage` → `ri_diskio_bytesread`), which is unambiguous: actually loading
Windows reads hundreds of megabytes.

| graphics | read by t=6s | read t=6s→26s | CPU over that window |
| --- | --- | --- | --- |
| none | 3.2 MB | **0 bytes** | 480 ms |
| virtio | 1.7 MB | **0 bytes** | 480 ms |

Both configurations read a few megabytes — the firmware scanning the ESP and loading the
boot manager — and then stop entirely, idling at ~2% CPU for the remainder of a 45-second
run while the VM stays in `Running`. Neither progresses. Removing the graphics device does
not unblock Windows, so that avenue is closed too.

(Note that "VM is still Running" on its own proves nothing here — a guest parked in a
firmware menu is also Running. The disk-read figure is what carries the result.)

## Apple's position

An Apple DTS engineer, May 2025 (macOS Sequoia), on
[Developer Forums thread 784051](https://developer.apple.com/forums/thread/784051):

> Windows 11 isn't supported by Virtualization Framework in macOS Sequoia. Other
> virtual machine software based on Hypervisor Framework do support Windows 11.
>
> Regarding future versions of macOS, I would recommend filing a new feature request
> through the Feedback assistant.

The same thread was revisited in **May 2026**, asking specifically about macOS 27. DTS
Engineer Quinn "The Eskimo!" replied:

> We can't talk about The Future™. See tip 3 in Quinn's Top Ten DevForums Tips.
>
> If and when this changes, you should be informed via `FB17582343` in Feedback Assistant.

So as of four months before this audit, Apple had neither announced nor denied Windows
support for macOS 27 — and FB17582343 remains the tracking bug. Nothing in the shipped
macOS 27 SDK contradicts the 2025 stance: there is no Windows-specific symbol, boot
loader, or guest type anywhere in the framework.

Quinn's canonical, moderator-locked
[Virtualization Resources](https://developer.apple.com/forums/thread/796275) post, posted
**August 2025** and still the pinned entry point for the tag, opens with the same framing:

> Virtualization framework is a high-level API to create macOS and Linux virtual machines.
>
> Hypervisor is a low-level API to build virtualization solutions without the need for a
> kernel extension.

That is the whole of Apple's public answer: Virtualization.framework is macOS and Linux;
for Windows, use Hypervisor.framework — which is exactly what QEMU does.

The VM service binary is even more emphatic. Searched for `windows`, `bootmgfw`, and
`tpm` across all strings in
`com.apple.Virtualization.VirtualMachine.xpc`: **zero hits, case-insensitive.** The VMM
has no concept of a Windows guest or a TPM at any level.

### There is no prior art, and the forum index is why it looks that way

Web search turns up nothing on any of this, but that is an artifact of the forums being
unindexable, not evidence of absence. `https://developer.apple.com/forums/search?q=...`
302s every non-browser client to `/forums/verify-human/verify.html`, so crawlers never
reach the result pages and the exact-phrase searches that matter return nothing. Driving
a real browser over CDP gets past the gate and lets the forums' own index answer
directly:

| query | results in the forums index |
|---|---|
| `FB17582343` | 2 — both in thread 784051, nowhere else |
| `com.apple.private.virtualization` | **No results found** |
| `_VZCustomMMIODeviceConfiguration` | **No results found** |
| `VZLinearFramebufferGraphicsDeviceConfiguration` | **No results found** |
| `Virtualization TPM` | no thread about a guest TPM |
| Windows + Virtualization.framework | thread 784051 is the only one |

So nobody has publicly asked about the restricted-device entitlement or either private
class, and thread 784051 is the entire public record of Windows-on-Virtualization.framework.
The findings above are first-hand or nothing.

## Verdict for cove

1. **macOS 27 does not unblock Windows.** Secure Boot is real and useful, but ships the
   Linux shim CA rather than the Windows CA; the GOP blocker is untouched in public API;
   the TPM gap is unchanged.
2. **The private-API route is closed by an explicit entitlement check, not by accident.**
   The three private APIs that would each independently solve a piece of this —
   `_setROMImageURL:` (boot OVMF, get a conforming GOP),
   `_VZLinearFramebufferGraphicsDeviceConfiguration` (GOP without replacing firmware),
   `_VZCustomMMIODeviceConfiguration` (userspace vTPM) — are all classified as "restricted
   devices" by the VM service and refused unless the client holds
   `com.apple.private.virtualization`. The devices themselves are fully implemented and
   would work; the gate is deliberate and runs before any of them is placed. This is not a
   "ships but risky" situation; it does not run at all, and no amount of parameter tuning
   changes that.
3. **Fix or retire `-windows-graphics linear-framebuffer`.** It cannot work on a
   normally-signed build and currently fails with an opaque "vm entered error state
   during startup". At minimum, detect the failure and say why.
4. **Windows on Virtualization.framework requires Apple to move, and the ask is small.**
   The single change that would unblock it is a linear-framebuffer GOP in the firmware
   behind `VZEFIBootLoader` — public API, no new device types, no TPM. That is a far more
   tractable Feedback than "support Windows", and it is worth filing against FB17582343.
5. **Until then, QEMU stays the Windows path.** That matches what
   `~/.vz/vms/windows-swiftui-qemu` already does, and what UTM does. Nothing found in this
   audit changes that trade-off.
6. **Removing the graphics device does not help.** Booting the real install media with no
   graphics device at all stalls in exactly the same place as with virtio, so the blocker
   is not merely that Windows rejects `PixelBltOnly`.
7. **The gate can be defeated only by relaxing AMFI**, which is confirmed by published
   third-party work rather than left as speculation: `Code-Hex/vz` enables the private GDB
   debug stub — a restricted device on the same list — with
   `com.apple.private.virtualization` plus `csrutil disable` and
   `amfi_get_out_of_my_way=1`, and `Lakr233/vphone-cli` self-signs the same entitlement and
   runs iOS guests on macOS 27.0b2 today. The entitlement is nominally "restricted to
   developers of virtualization software" and obtainable only through an Apple
   representative, but on an AMFI-relaxed host it can simply be self-granted. That is not a
   configuration cove can ask users to adopt, so the shipping conclusion is unchanged.
8. **`csrutil allow-research-guests` does not help, and neither does
   `com.apple.private.virtualization.security-research`.** The SIP bit gates AVP research
   guests (the `vresearch1` Image4/iBoot/SEP chain), which Windows never touches; the
   research entitlement gates exactly one call site in the whole VM service, a host-only
   vmnet backend. Both are the wrong axis. Only bit 1 opens the restricted devices.
9. **One experiment is still open, and it is worth naming.** On an AMFI-relaxed host,
   `_VZLinearFramebufferGraphicsDeviceConfiguration` becomes testable, and it is the single
   device most likely to answer the GOP question — a linear framebuffer is what
   `PixelBlueGreenRedReserved8BitPerColor` describes. If it boots `bootmgfw.efi`, that
   turns item 4 from a guess into a demonstrated one-line ask against FB17582343. It has
   not been run here because it costs a Recovery reboot and a machine-wide code-signing
   downgrade, which is a decision for the operator, not for this audit.

## How to reproduce

```bash
# the restricted-device gate, from the VM service binary
X=/System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/\
com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine
lipo -thin arm64e "$X" -output vmxpc
strings -a vmxpc | grep -i "restricted"
#   Restricted devices require the com.apple.private.virtualization entitlement.
#   Unable to create a virtual machine with restricted devices.
otool -tV vmxpc > vmxpc.asm
grep -n "com.apple.private.virtualization\"" vmxpc.asm   # the entitlement bitmask builder
grep -n "custom-mmio-device" vmxpc.asm                   # the guarded MMIO construction
```

```bash
# macOS 27 additions
SDK=$(xcrun --show-sdk-path)/System/Library/Frameworks/Virtualization.framework/Headers
grep -rl "macos(27" "$SDK"

# private + public class presence (framework is in the dyld shared cache, so nm(1) fails)
dyld_info -exports /System/Library/Frameworks/Virtualization.framework/Versions/A/Virtualization \
  | grep -oE '_OBJC_CLASS_\$_+_?VZ[A-Za-z0-9]*'

# no TPM anywhere
dyld_info -exports /System/Library/Frameworks/Virtualization.framework/Versions/A/Virtualization | grep -ic tpm
```

The private-API probes were minimal ObjC programs ad-hoc signed with
`codesign -s - -f --entitlements internal/autosign/vz.entitlements <bin>`, each building a
`VZVirtualMachineConfiguration` (generic platform + `VZEFIBootLoader` + variable store),
calling `validateWithError:` and then `startWithCompletionHandler:` on the VM's dispatch
queue, and reporting both outcomes. Two harness notes that cost real time:

- Build them with `-fobjc-arc`. Under manual retain/release the `__block VZVirtualMachine *`
  is not retained and the harness segfaults during teardown, which looks exactly like the
  private API crashing.
- Private accessors must go through `objc_msgSend` with a correctly typed cast;
  `CGSize`-taking initializers such as `-initWithBackingStoreSize:` need
  `((id(*)(id,SEL,CGSize))objc_msgSend)`.
- Delegate-provider delegates are held **weakly**. Passing a temporary
  (`...initWithDeviceQueue:dq delegate:[MyDelegate new]`) under ARC deallocates it
  immediately, the provider's `_delegate` reads back nil, and `didCreateDevice:` never
  fires — which looks like the API failing. Hold the delegate in a strong variable that
  outlives the VM. This applies to the public `VZCustomVirtioDeviceDelegateProvider` too.
- Do not call `-guestRAMRegions` on the `_VZCustomMMIODevice` handed to
  `didCreateDevice:`; it traps (SIGTRAP) before the VM has finished starting.

The forum searches above cannot be reproduced with `curl` — every non-browser client is
302'd to `/forums/verify-human/verify.html`. Drive a real browser instead:

```bash
cdp -connect-existing -debug-port 9222 \
  -url "https://developer.apple.com/forums/search?q=com.apple.private.virtualization" \
  -wait-ready -await -js '(async()=>{
    for (let i=0;i<25;i++){ await new Promise(r=>setTimeout(r,1200));
      if (document.querySelector("a[href*=\"/forums/thread/\"]")) break; }
    return document.body.innerText.slice(0,600);
  })()'
#   ... No results found
```

The Secure Boot certificate dump used a short ObjC program calling
`initCreatingVariableStoreAtURL:options:error:`,
`enableSecureBootUsingDefaultPlatformKeyWithError:`,
`enrollDefaultSecureBootSignaturesWithError:`, then
`getEnrolledSecureBootSignaturesWithError:`, printing
`SecCertificateCopySubjectSummary` for each `VZEFISignatureX509Certificate`. Note that
`-signatures` and `-certificate` are `NS_REFINED_FOR_SWIFT`; from ObjC they must be
called via `objc_msgSend` with `sel_registerName`, and `VZEFISignatureList` must be
built with `-initWithSignatures:` rather than `+signatureListFromSignatures:`.
