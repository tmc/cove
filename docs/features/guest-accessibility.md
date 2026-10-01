# Guest accessibility observations

`cove ui status -vm NAME` reports the logged-in guest user agent's session and
Accessibility permission state without prompting. `cove ui inspect -vm NAME
-pid PID` reads one guest application's accessibility tree. `cove ui find -vm
NAME -pid PID -role AXButton -label Save` returns exact matches and reports
ambiguity. Add `-json` for structured output.

The VM must already be running. These commands use the user agent on vsock
1025, never the root agent on 1024. Older agents report an unsupported protocol;
other guest operating systems report unsupported capability. A missing user
agent, permission denial, locked session, stale generation and unresponsive
application remain distinct states.

The installed executable is `/usr/local/bin/vz-agent`, launched by the guest's
`com.tmc.cove.vz-agent-user` LaunchAgent. Signing or installing that executable
does not grant Accessibility permission. Grant permission explicitly in the
guest's System Settings. The status operation does not request permission or
modify TCC. Screen lock information is optional; if unavailable, the status
reason says so rather than asserting the screen is unlocked.

Reads default to depth 5, 256 nodes, 64 KiB and two seconds. Hard limits are
depth 16, 1024 nodes, 256 KiB and ten seconds. Native messaging calls use at
most 250 ms of the remaining request budget. Truncated searches report an
incomplete match result. Values are never queried, and editable and static text
labels are omitted. Other labels may still contain filenames or personal data.

Observation handles are random-generation-scoped hashes, not native pointers.
Each read produces a new observation. An expected generation rejects stale
reads after host restore, runtime replacement or observed user-session changes.
No actions, coordinate fallback, persistent element handles or wait operations
are provided by this read-only interface.

The `github.com/tmc/cove/guest` package exposes `NewSession`, `Ready`, `Inspect`
and `Find` over the authenticated control socket. `Close` cancels client
requests and does not stop the VM. SDK responses are bounded to 4 MiB, including
the control envelope. Guest native reads remain subject to their request budget.

The native bridge binds public ApplicationServices functions directly. The SDK
header declares `AXUIElementSetMessagingTimeout(AXUIElementRef, float)`; the
bridge uses `float32`, avoiding the dependency's incorrect generated pointer
signature. It does not use the private AX window helper. Protocol changes are
made in `proto/agent.proto` and `proto/control.proto`; `make proto` regenerates
both Go and Swift outputs.

Controller, transport, cancellation, byte-limit and host ABI fixtures pass.
Physical guest permission-denied/trusted, logout, restore and native/browser
application observations remain unqualified until the user agent and explicit
guest Accessibility trust are available. A host ABI fixture is not a guest
runtime acceptance test.
