# Window close suspends/stops without asking

Status: FIXED c4eb5472

run -gui: closing the window calls quitRuntime (macos.go:2286-2294) ->
suspend on the main thread (beachball), or hardStopVM when suspend isn't
allowed (macos.go:2264). Attached GUI (gui_control.go:290-312) only hides.
ShouldTerminate always returns NSTerminateCancel (macos.go:2303), which
blocks host logout/shutdown.

Fix: sheet with Suspend / Shut Down / Keep Running / Cancel; do the work
off-main and return NSTerminateLater + replyToApplicationShouldTerminate:.
