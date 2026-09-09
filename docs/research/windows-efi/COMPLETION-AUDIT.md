> Current status (2026-09-09): this is the historical September 7 audit.
> Landing subsequently succeeded. See the [installation report](installation/README.md),
> [native input evidence](installation/receipts/native-input.json), and
> [current acceptance plan](installation-plan.md) for installation, UI,
> validation scope, and the default-VM metadata incident.

# Windows boot investigation: completion audit

2026-09-07. The original Windows goal was created at 06:42:16Z in the supplied
session transcript. It calls for the aligned bounded experiments, preserving
default/iOS work, recording evidence and limitations, and landing atomic
source/documentation commits through the required helper, with notes and push.
Later user requests add working guest debugging and a direct-HVF assessment.
The NotebookLM self-prompting objective continues that investigation.

| Requirement | Current evidence | Status |
| --- | --- | --- |
| Deterministic scratch boot harness | `cmd/winbootprobe` accepts a supplied scratch EFI disk, creates fresh EFI state, checks copies and bounds runtime; per-run commands and content hashes accompany receipts. Production shell-cache behavior was not changed. | Established for the experimental harness |
| Apple guest GOP/ACPI/chainload diagnostics | `results/apple.txt`, `apple-chainload.txt`; two GOP handles, BltOnly modes, ACPI tables and explicit chainload status. | Established |
| Same-media QEMU ramfb Windows control | `winpe-receipt/qemu-command.json`, `qemu-receipt.txt`; actual WinPE userspace and observed Setup. | Established |
| Gated RAM-backed GOP causal test | `shim/`, `results/apple-shim-v2.txt`, matched control records; shim alone did not clear the park. | Completed, with qualified negative |
| Identify next blocker or milestone | Natural PMCCNTR fault at bootmgfw RVA0x35804; native PMU plus GOP shim reaches WinPE. `guest-debug/` and `native-pmu/`. | Established |
| Working guest debugging | Own-child breakpoint/step controls, Windows entry/stepping and natural-fault receipts in `guest-debug/`. Host GDB remains unavailable, with limitations recorded. | Established through guest-resident instrumentation |
| Assess direct Hypervisor.framework | `hypervisor-assessment.md`, `hvf-debug/`; documented ownership limits and qualified QEMU control. | Completed |
| Conditional private LFB/spare-host work | Ordinary-entitlement LFB launch was rejected as restricted. Native PMU plus guest capture provides progress without a spare-host security change. No further policy change is justified by the present experiment. | Deferred by its gate |
| Preserve default VM and iOS work | Runs used named disposable clones, never the default as a Windows target; iOS checkout/history was not merged. | Honored during this investigation |
| Source/evidence without staged binaries | Explicit source/text paths staged; raw EFI/PE files, media, captures and random URL tokens remain outside git. | Staging audited |
| Quality gates | Fresh `go build ./...` passes. Full test run reproduces NameGetSet SIGTRAP. Queue-confined access traps at the same PC; the trial was reverted. | Executed; known baseline failure remains |
| Atomic commits, notes and push | HEAD remains062e4ab6. Required helper retried with documented model fallback; backend says insufficient credits and creates no commit. | Blocked on helper backend |

Additional evidence exceeds the original boot experiment: guest GDI capture,
NetKVM/DHCP, live PNG upload and visible Unicode text input all passed. Browser
UI validation failed independently. Full Windows installation, a polished viewer,
mouse/shortcut coverage and production integration are follow-on product work,
not hidden requirements of the original bounded investigation. They remain
explicitly unverified; no desktop/installation claim is made.

Research requirements are answered, but the goal is not complete while required
landing remains blocked. The full-suite failure is reported rather than hidden. This audit does not waive them.


The final NotebookLM scope review found no additional original research omissions.
Its claim that exactly14 commits are required is unsupported and is not adopted.
The actual requirement is atomic source/documentation commits, notes and push.
Proposed landing slices are the host probe, then its research source/evidence.
Raw review: /tmp/cove-nlm-self-20260907/completion-review.md.
