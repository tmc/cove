# Guest-resident EFI breakpoint experiments

These standalone ARM64 EFI applications observe a guest exception and a loaded
image entry breakpoint without the host VZ GDB entitlement. They do not provide
an interactive debugger or identify the original Windows stall PC.

Build to scratch with `bash build.sh /tmp/cove-guest-debug-build`. No generated
EFI files or disk images belong in git. The source reuses the existing EFI
diagnostic and the `NO_SHIM` chainloader's protocol/file definitions; no GOP
replacement is performed.

## Controls

`TRAP.EFI` installs an aligned EL1 exception vector around a known `BRK #7`,
records ELR/ESR/FAR/SPSR/SP, then restores the original vector and interrupt masks
before writing `EFIDIAG.TXT`. It requires CurrentEL=EL1 and SPSel=1. The observed
ELR must equal the known instruction, ESR must be `0xf2000007`, and x16/x17
sentinels must survive. FAR is recorded but has no meaningful fault address for
BRK. Its vector is only suitable for this deliberately masked control.

`ENTRY.EFI` loads `\EFI\Microsoft\Boot\bootmgfw.efi` from its own device, checks
the loaded ARM64 PE32+ image and executable entry section, then patches the
in-memory first instruction with `BRK #7`. It does not change the Microsoft file
on disk. It cleans the data/instruction caches, calls firmware StartImage, and
recovers only from the exact expected breakpoint with unchanged TTBR0, TTBR1,
TCR, and SCTLR. Any other exception or address-space change freezes the probe
for the host's bounded stop. Before any filesystem calls, recovery restores the
parent stack, x18-x30, d8-d15, original VBAR, interrupt masks, and TPL; the entry
instruction is restored too.

Recovery deliberately abandons the active StartImage call. The VM is disposable
and must be stopped after collecting the report; normal firmware continuation or
VM reuse is not established. A positive result establishes entry arrival in the
instrumented run, before the original first Windows instruction. A negative
result is inconclusive. No claim is made about handler survival after Windows
changes its exception vectors or address space.

## Apple VZ results, 2026-09-07

All runs used scratch disk clones, ordinary virtualization signing entitlements,
virtio graphics, USB storage, and five-second bounded observations.

- [Known BRK](results/apple-brk.txt): exact ELR, ESR `0xf2000007`, and x16/x17
  sentinel preservation passed.
- [Own EFI child](results/apple-entry-control.txt): same one-shot entry recovery
  passed for `CHILD.EFI`, whose normal entry simply returns success.
- [Microsoft child](results/apple-windows-entry.txt): loaded base `0x26de42000`,
  entry RVA `0x34b70`, expected and observed ELR `0x26de76b70`, ESR `0xf2000007`.
  The original entry instruction was `0xa9bb53f3`.

Microsoft child SHA256:
`26085cabe01870a8921b61efd135a59898a51daae25b64b6bedba5830fb472f9`.
This is the previously verified WinPE media's original EFI loader.

Raw sources, binaries, disposable disk clones, logs, and original report bytes
are preserved in `/tmp/cove-guest-state-20260907/`.

## One-instruction software stepping

`STEP.EFI` first takes the same entry breakpoint, restores the original first
instruction, then enables EL1 software stepping with MDSCR SS/KDE/MDE, SPSR.SS,
and PSTATE.D clear. The handler preserves its scratch registers across resuming
the child and only recovers from the expected same-EL step at entry+4 with an
unchanged address space. It restores MDSCR before returning to firmware code.
The same disposable, abandoned-StartImage limitation applies.

- [Own child step](results/apple-step-control.txt): next PC was entry+4;
  ESR `0xcf000022` has same-EL software-step exception class `0x33`.
- [Microsoft step](results/apple-windows-step.txt): next PC `0x26de76b74` was
  entry+4; ESR was also `0xcf000022`. SP decreased from `0x26fbd8840` to
  `0x26fbd87f0`, matching the first instruction's 80-byte stack allocation.

This establishes execution of one original Windows instruction in the
instrumented run, not the location of the subsequent original stall.

## Bounded instruction tracing

`TRACE.EFI` extends the step control with a 4096-step ceiling and a 128-PC ring,
preserved only in guest memory until recovery. Its handler saves all x0-x30 and
returns with the child's SPSR flags. Before decoding the next instruction, it
checks that TTBR0/1, TCR, and SCTLR are unchanged and the PC is inside the loaded
image. It stops before system writes/cache/hints (except NOP/BTI), exception
returns and exception-generating instructions. Asynchronous vector slots freeze
rather than interpreting a potentially stale ESR. Synchronous exceptions have a
separate reason from step limits, system-instruction stops, and image exits.

This is intentionally conservative instrumentation. Its 256-byte save area
below the child SP, debug mask changes, and timing changes can affect behavior;
it is not a nonperturbing sampler. An abandoned StartImage is not a clean detach.
The arbitrary synchronous-fault recovery branch is not yet separately validated
by an intentional data-abort child; the controls below exercise software steps.

- [Branching child](results/apple-trace-control.txt): 25 steps match its loop and
  comparisons, x9-x12 retain `0x1111` through `0x4444`, and it stops before the
  expected `MSR DAIFSet` instruction (`0xd50342df`, entry+0x4c).
- [Microsoft trace](results/apple-windows-trace.txt): 67 steps reach an indirect
  call from bootmgfw RVA `0xbe6c0` into firmware at `0x26fae1980`. The trace stops
  at this image boundary, before fetching a firmware instruction. LR is
  `0x26df006c4`, the expected return address. This is a chosen observability
  boundary, not the original Windows stall.

All control and Microsoft runs used the same respective instrument binary and
fresh disposable image clones. None used the restricted host debugger, amfidont,
or a host security change.

## Breakpoints and tracing at a selected image RVA

`bash build-site.sh /tmp/site-build 0xbe6c4 site` builds a one-shot breakpoint at
the specified RVA. Use `trace` instead of `site` to begin bounded stepping there.
The RVA must lie in an executable section of the loaded PE. Original image entry
and chosen breakpoint RVA are reported separately. `site` also captures x0-x8.

- [Second-site own child](results/apple-site-control.txt): the child executes a
  BL/RET normally, then the breakpoint at entry+8 captures the known returned
  x0=`0x1234`.
- [SetupMode query return](results/apple-setupmode-return.txt): breakpoint RVA
  `0xbe6c4` is reached with x0=0 (`EFI_SUCCESS`). The first EFI variable query
  returns in this instrumented run.
- [Initial routine return](results/apple-init-return.txt): breakpoint RVA
  `0x34bb0`, after the call to RVA `0xbe620`, is reached with x0=0.
- [Mid-entry trace control](results/apple-trace-site-control.txt): tracing begins
  after the branching child's first instruction; the remaining 24 steps preserve
  the same sentinels and stop before its expected `MSR DAIFSet`.
- [Trace after initialization](results/apple-after-init-trace.txt): starting at
  RVA `0x34bb0`, 34 steps reach another firmware call at RVA `0x34c24`, with
  return address RVA `0x34c28`. The out-of-image target is `0x26fbe003c`.

Interrupt masking is performed at probe setup. Firmware may subsequently alter
DAIF; captured SPSR values demonstrate this. Continuous masking throughout
StartImage or runtime-service calls is not established.

## Captured PMCCNTR fault without patching or stepping

The [trace after the firmware-call group](results/apple-pmu-step-fault.txt),
starting at RVA `0x34c9c`, captured a synchronous exception after 777 steps at
RVA `0x35804`. The instruction is `0xd53b9d09`, `MRS x9, PMCCNTR_EL0`; ESR is
`0x02000000` (unknown instruction exception class, 32-bit instruction).

To test whether stepping induced this, `fault.c`/`fault.S` install only an
exception vector and run the original loaded image bytes. They perform no code
patch and do not touch MDSCR or enable single-stepping. Recovery requires the
exact expected PC, ESR, opcode, and unchanged address-space registers. Async
vectors freeze. Build with `bash build-site.sh /tmp/fault-build 0x35804 fault`.

- [Own MRS child](results/apple-pmu-fault-control.txt): an unpatched child whose
  first instruction reads PMCCNTR_EL0 faults at that exact instruction with
  ESR `0x02000000`.
- [Unpatched Windows](results/apple-pmu-fault-only.txt): the same exception is
  captured at loaded PC `0x26de77804`, RVA `0x35804`, without code patching or
  stepping. The Microsoft child retains its previously recorded SHA256.

This establishes an actual early Windows PMCCNTR read fault on Apple VZ,
independently of the cumulative single-step trace. It does not establish that
correcting this first obstacle is sufficient to boot Windows. The custom vector
still changes fault handling and recovery discards the guest; no transparent
interactive debugger or original unmodified-host PC sampler is claimed.

## Counter capability control

`COUNTER.EFI` reads the advertised debug feature register and architectural timer
registers. Each access is protected by the known exception-control vector;
original VBAR/DAIF are restored before reporting any result.

[Apple counter result](results/apple-counter-capabilities.txt):
`ID_AA64DFR0_EL1=0x10305006`, whose PMUVer field is zero;
`CNTFRQ_EL0=24000000`. Three CNTVCT reads completed and advanced monotonically.
This validates an available advancing generic virtual timer, not a replacement
with proven PMU cycle-counter timing semantics or CPU-cycle frequency.

## Bounded counter-read emulation experiment

`EMU.EFI` leaves image bytes and MDSCR unchanged. On the exact unknown-instruction
exception for `MRS PMCCNTR_EL0`, within a validated executable section and with
unchanged address-space registers, it writes CNTVCT to the saved destination
register and advances ELR by four. Register 31 discards the value. It preserves
other general registers and returns with the original SPSR. At most 64 reads are
emulated; a later matching read or different synchronous exception triggers safe
recovery. There is no exception-handler file I/O. Counter values are 24MHz generic
timer ticks, not proven equivalent to CPU cycles. Below-SP stack use and altered
fault timing remain instrumentation effects.

[Own-child control](results/apple-emu-control.txt) emulates three reads targeting
x0, x9 and xzr; x9 advances beyond x0, x10/x11 sentinels survive, and its success
marker `BRK #99` is captured. The failure marker would have been `BRK #100`.

[Windows run](results/apple-emu-windows.txt) does not produce a recovery report
within five seconds. Only pre-StartImage metadata was persisted. No emulation
count, next fault, or boot milestone is established by this run. Losing the custom
vector, a changed address space, or another wait are unresolved possibilities;
this negative does not establish that emulation failed or reached its read cap.

## Resume and asynchronous-vector checks

The post-read variant places a one-shot BRK after the first original PMCCNTR
instruction. [Its own-child control](results/apple-postread-control.txt) and
[Windows at RVA0x35808](results/apple-postread-windows.txt) both recover after
exactly one emulated read. The returned counter values lie within independently
sampled CNTVCT intervals. Thus one counter substitution demonstrably resumes
Windows past its first fault; this does not establish later boot progress.

The async variant records vector-slot identity rather than freezing all async
slots. [Synchronous regression](results/apple-async-sync-control.txt) retains
three-read emulation and the BRK99 marker. [The IRQ child](results/apple-irq-control.txt)
enables IRQs and spins for at most one generic-timer second; it captures slot5
(EL1h IRQ) and preserves x16/x17 markers despite the extra vector prologue. Its
ESR is explicitly not interpreted. [Windows still produces no recovery receipt](results/apple-async-windows.txt)
within five seconds, leaving later vector loss/guard failure/wait unresolved.
The extra vector prologue makes the transient frame 272 bytes below the child SP.

All recovery guards compare TTBR0, TTBR1, TCR and SCTLR register values. They do
not prove unchanged page-table contents. These experiments remain disposable,
perturbing guest diagnostics; asynchronous recovery abandons the interrupted
execution and is not IRQ forwarding or acknowledgement emulation.

The hybrid counter/step prototype (`hybrid.c`, `hybrid.S`,
`hybrid-child.S`) was tested only against the own-source child. It substituted
one CNTVCT value for a PMCCNTR read, masked IRQ/FIQ during the following
in-image trace, and recovered after 21 steps before DAIFSet with the four
general-register sentinels preserved. See `results/apple-hybrid-control.txt`.
No Windows hybrid run was performed: discovery of the native generic-platform
PMU emulation option superseded this guest workaround experiment.

Rebuild the later prototypes into external scratch directories:

```sh
./build-experiment.sh /tmp/guest-debug-async async
./build-experiment.sh /tmp/guest-debug-postread postread 0x100c
./build-experiment.sh /tmp/guest-debug-hybrid hybrid 0x1010
```

These RVA examples target the included own-source children. The tested Windows
postread stop RVA was `0x35808`. Building does not launch a guest.
