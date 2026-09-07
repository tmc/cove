# vphone parity implementation status

Status: incomplete. The approved plan remains the full scope. This first
increment is configuration and planning infrastructure, not a bootable runtime.

Baseline: `a367dca86e9ffb070dc0e898e1c2bd1f9e1f5fcd`.

## Implemented in increment 1

- `internal/iosbundle.Config`: versioned metadata, pinned research display
  defaults, all five variant names, network/MAC and digest validation, ROM
  references, boot arguments, no-binpack and runtime no-vphoned settings.
- `internal/vmconfig`: preserve the iOS object during hardware edits; reject
  invalid metadata on load/save; detect valid iOS metadata before `hw.model`.
  Malformed configuration is reported as unknown rather than guessed macOS.
- `internal/vmrun`: `GuestIOS`, full-duplex audio planning and rejection of
  incompatible generic install/Linux/virtio guest-service options.
- macOS runtime rejects iOS configuration before state creation or mutation.
  This guard is temporary containment pending the actual iOS runtime dispatcher;
  it is not counted as runtime parity.

Tests cover metadata round trips, preserving metadata through hardware edits,
invalid-save preservation, marker precedence, malformed/unknown schemas,
conflicting boot options and macOS runner rejection before guest-state mutation.
The focused package and runner tests pass. `go build ./...` passes.
The full suite's native `TestPrivateAPI_NameGetSet` failure is tracked separately.

## Remaining ledger

F03 (configuration), F12 (dispatch), F17 (device planning) have partial foundations.
None is accepted as complete. F01–F02, F04–F11, F13–F16 and F18–F39 remain
unimplemented for iOS. In particular, there is no iOS CLI entry point, PV=3 VZ
constructor, DFU observation, firmware adapter, guest transport or GUI integration.
No host/firmware profile has been qualified and no iOS boot success is claimed.

Next: create the bundle/identity and runtime graph, propagate iOS through CLI and
Finder dispatch, and prove signed DFU enumeration before provisioning stages.
The planned durable stage manifests and all guest/desktop/media requirements
remain required. Notebook review must assess actual implementation against every
ledger row, never treat the previous plan approval as implementation completion.
