# Menu shortcuts take plain Cmd keys from the guest

Status: FIXED 55f9c07e

mainmenu.go:41-53,69 binds Cmd-. Stop, Cmd-P Pause, Cmd-R Restart,
Cmd-K Capture, Cmd-S Screenshot, Cmd-T Toggle Toolbar. With system-key
capture off (default), Cmd-S in a guest editor may open cove's screenshot
panel and Cmd-R may restart the VM.

Fix: Ctrl-Cmd or Cmd-Opt for VM actions.
