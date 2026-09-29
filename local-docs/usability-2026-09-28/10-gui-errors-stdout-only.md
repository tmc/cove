# GUI errors only go to stdout/stderr

Status: FIXED b639a8c0

Runtime actions (runtime_actions.go), shared-folder add/apply/mount
(toolbar.go saveAndApplySharedFolders, applySharedFoldersToVM,
ensureGuestSharedFoldersMounted), and start failures print to the terminal
only. A user in the window sees nothing — this is why 01 went unnoticed.

Fix:
- Added `reportGUIError(window, title, err)` in `cmd/cove/gui_alert.go` to present errors on VM windows as sheets (or modal dialogs when window is detached).
- Added `DispatchAsyncMain` in `cmd/cove/blocks.go` to safely schedule presentation on the main dispatch queue.
- Routed runtime action errors to `reportGUIError`:
  - `requestVMStop` (stop error, force stop error)
  - `toggleVMStartPause` (pause error, resume error, start error)
  - `restartVM` (stop error, start error)
  - `bootVMToRecovery` (stop error, recovery start error)
  - `requestVMSuspend` (suspend error)
  - `saveCurrentVMScreenshot` (capture error, save error, encode error)
- Routed VM start failures in GUI mode (`runVMWithGUI` in `cmd/cove/macos.go` for both start-first and window-first orders) to `reportGUIError`.
- In `cmd/cove/toolbar.go`:
  - Sequenced shared folder apply and guest mount operations on a single background goroutine so hotplugging completes before attempting guest mount.
  - Routed save, apply, and mount failures to `reportGUIError`.
  - On mount success, displayed the guest path (`/Volumes/My Shared Files/<tag>` for single share, `/Volumes/My Shared Files` for multiple shares) in the window subtitle.
