# Delegated-exit gate: only 0x16 accepted

Host: macOS 27.0, build 26A5425a. No VM launched; no special entitlements added to the probe.

Command:

    clang /tmp/vz-hv-survey/classes.m -framework Foundation -o /tmp/vz-hv-survey/classes
    /tmp/vz-hv-survey/classes > /tmp/vz-hv-survey/classes.txt

The probe creates a fresh VZVirtualMachineStartOptions for each integer 0..63, calls _setDelegatedExceptionClasses: with a singleton NSArray of NSNumber, and catches NSException. All 64 results are in classes.txt. The only accepted value was 0x16. In particular:

    EC 0x00 rejected: *** -[VZVirtualMachineStartOptions _setDelegatedExceptionClasses:]: Unsupported delegated exception class.
    EC 0x16 setter accepted
    EC 0x18 rejected: *** -[VZVirtualMachineStartOptions _setDelegatedExceptionClasses:]: Unsupported delegated exception class.

The repository names 0x16 ExceptionClassHVC64 in x/hvfkit/arm64/regs.go:14. This is an HVC candidate, not evidence for general system-register interception.

Disassembly corroborates the exact-value check:

    ipsw dyld disass /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld/dyld_shared_cache_arm64e --vaddr 0x223fa4220 --count 90 --quiet

Literal instructions:

    0x223fa42c8: bl 0x2280037b0
    0x223fa42cc: cmp x0, #0x16
    0x223fa42d0: b.ne 0x223fa4364

Full output: setter-quiet.asm. The marked-up disassembler spent almost two minutes scanning public symbols; those two runs were deliberately stopped and replaced by quiet disassembly, which completed. This was an inspection-tool cost, not a VM failure.

Delivery lead:

    ipsw dyld macho /System/Volumes/Preboot/Cryptexes/OS/System/Library/dyld/dyld_shared_cache_arm64e /System/Library/Frameworks/Virtualization.framework/Versions/A/Virtualization --symbols --strings

Literal output includes:

    0x2240fae00: _objc_msgSend$_virtualMachine:cpuDidExitWithContext:completion:

The same symbol inventory includes VzVirtualMachineMessenger::cpu_did_exit and a completion callable taking _VZCPUExitOutcome. This establishes a selector reference, not a live callback or its receiver. Receiver selection, completion outcome values, and runtime authorization remain unverified. CPUExit and SIMD ABI defects identified in report.md remain unfixed.

Conclusion: the proposed direct EC 0 / EC 0x18 interception path is rejected by the unmodified setter on this build. Do not claim HVF-grade general sysreg interception inside VZ. An HVC cooperative-guest experiment is a separate hypothesis; neither HVC delivery nor a completion round-trip has been demonstrated. No justification remains to run the proposed sysreg EFI experiment as though its prerequisite passed.
