# VM menu items don't reflect VM state

Status: FIXED 40667e30

No validateMenuItem: on the toolbar delegate: Pause never becomes Resume,
Suspend stays enabled when unsupported, Capture Input never shows state.
Stop has no force option; a guest that ignores the request leaves no
feedback. No hint for releasing captured input. No min window size with
automaticallyReconfiguresDisplay on.

status_item.go:195-240 already has correct runStateTitle/runStateEnabled
logic to reuse.

Fix:
- Implemented `validateMenuItem:` and `validateToolbarItem:` on the toolbar delegate (`VMToolbar` in `cmd/cove/toolbar.go`).
- Shared run-state title and enabled status helper functions (`vmRunStateTitle`, `vmRunStateEnabled`, `isVMStateBusy`) between `toolbar.go` and `status_item.go`.
- Dynamic menu validation updates:
  - `startPauseVM:` updates title to "Resume" when paused, "Pause" when running, "Start" when stopped/error; disabled when busy.
  - `suspendVM:` enabled only when running or paused, `canSaveRestore` is true, session allows suspend, and not in boot transition.
  - `restartVM:` enabled only when running.
  - `stopVM:` enabled when running or paused.
  - `captureInput:` enabled when running; dynamically shows "Release Input (Ctrl-Opt)" when captured and "Capture Input" when uncaptured.
  - `takeScreenshot:` enabled only when running.
  - `bootRecovery:` enabled when not busy and (running, paused, or stopped).
  - `addSharedFolder:`, `removeSharedFolder:`, `removeAllSharedFolders:` enabled when not busy.
- Enforced 800x600 minimum content size (`minWindowWidth`, `minWindowHeight`) on VM windows in `macos.go`, `gui_control.go`, and `installer.go`.

