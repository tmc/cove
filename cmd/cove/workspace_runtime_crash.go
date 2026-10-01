package main

import "bytes"

const workspaceRuntimeCrashLimit = 16 << 10
const workspaceRuntimeCrashOverlap = 128

// Crash context is a bounded diagnostic observation, never cleanup authority.
type workspaceRuntimeCrash struct {
	pending []byte
	context []byte
}

func (c *workspaceRuntimeCrash) append(p []byte) bool {
	if len(c.context) > 0 {
		remaining := workspaceRuntimeCrashLimit - len(c.context)
		if remaining == 0 || len(p) == 0 {
			return false
		}
		if len(p) > remaining {
			p = p[:remaining]
		}
		c.context = append(c.context, p...)
		return true
	}
	// Search the previous suffix and the new chunk separately, so a large
	// write never requires an equally large scratch allocation.
	prefixLen := workspaceRuntimeCrashOverlap
	if len(p) < prefixLen {
		prefixLen = len(p)
	}
	bridge := append(append([]byte(nil), c.pending...), p[:prefixLen]...)
	index := workspaceRuntimeCrashMarker(bridge)
	if index >= 0 {
		c.context = append(c.context, bridge[index:]...)
		remaining := workspaceRuntimeCrashLimit - len(c.context)
		rest := p[prefixLen:]
		if len(rest) > remaining {
			rest = rest[:remaining]
		}
		c.context = append(c.context, rest...)
		c.pending = nil
		return true
	}
	if index = workspaceRuntimeCrashMarker(p); index >= 0 {
		end := index + workspaceRuntimeCrashLimit
		if end > len(p) {
			end = len(p)
		}
		c.context = append(c.context, p[index:end]...)
		c.pending = nil
		return true
	}
	if len(p) >= workspaceRuntimeCrashOverlap {
		c.pending = append(c.pending[:0], p[len(p)-workspaceRuntimeCrashOverlap:]...)
	} else {
		c.pending = append(c.pending, p...)
		if len(c.pending) > workspaceRuntimeCrashOverlap {
			c.pending = append(c.pending[:0], c.pending[len(c.pending)-workspaceRuntimeCrashOverlap:]...)
		}
	}
	return false
}

func workspaceRuntimeCrashMarker(p []byte) int {
	first := -1
	for _, marker := range []string{"panic:", "fatal error:", "SIGSEGV:", "SIGABRT:", "SIGBUS:", "runtime: out of memory", "runtime: goroutine stack exceeds ", "runtime: failed to create new OS thread", "unexpected fault address"} {
		if index := bytes.Index(p, []byte(marker)); index >= 0 && (first < 0 || index < first) {
			first = index
		}
	}
	return first
}
