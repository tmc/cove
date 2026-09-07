package main

// holderCommand returns the executable path and name for a process
// holding a VM disk open, so stale-lock detection can match it against
// isVirtualizationVMProcess. It is best-effort: an empty string means the
// process could not be inspected.
func holderCommand(pid int) string {
	if err := ensureLibproc(); err != nil {
		return ""
	}
	if bsd, ok := darwinBSDInfo(int32(pid)); ok {
		return darwinProcessCommand(int32(pid), bsd)
	}
	return darwinProcName(int32(pid))
}
