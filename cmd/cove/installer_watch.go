package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// vmDirWatcher watches a VM directory and its parent for any
// remove/rename/create events while an install is in flight, logging
// each event with a Go stack trace so we can pinpoint which caller
// produced it. Used to diagnose blockers-next.md #1: vmDir disappears
// between MkdirAll and stopVMAndInject on a fresh install.
//
// The watcher attaches to two paths:
//   - vmDir itself: catches child events (disk.img unlink, aux.img rename)
//   - parent dir:   catches events on vmDir itself (its own rmdir/rename)
//
// Self-removal of a watched directory does not always produce events
// inside the watched directory, so the parent watch is required.
type vmDirWatcher struct {
	w       *fsnotify.Watcher
	vmDir   string
	parent  string
	stopCh  chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
}

// startVMDirWatcher begins watching vmDir and its parent. It returns a
// non-nil watcher even if attaching to one path fails (so the caller
// can always defer Stop), and never blocks. Events fire as vzlog lines
// in the same stream as the existing install instrumentation.
func startVMDirWatcher(vmDir string) *vmDirWatcher {
	parent := filepath.Dir(vmDir)
	v := &vmDirWatcher{
		vmDir:  vmDir,
		parent: parent,
		stopCh: make(chan struct{}),
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		vzlog("vmDirWatcher: fsnotify.NewWatcher failed: %v", err)
		return v
	}
	v.w = w
	if err := w.Add(parent); err != nil {
		vzlog("vmDirWatcher: add parent %q failed: %v", parent, err)
	} else {
		vzlog("vmDirWatcher: watching parent %q", parent)
	}
	if err := w.Add(vmDir); err != nil {
		vzlog("vmDirWatcher: add vmDir %q failed: %v", vmDir, err)
	} else {
		vzlog("vmDirWatcher: watching vmDir %q", vmDir)
	}

	v.wg.Add(1)
	go v.run()
	return v
}

func (v *vmDirWatcher) run() {
	defer v.wg.Done()
	if v.w == nil {
		return
	}
	for {
		select {
		case <-v.stopCh:
			return
		case ev, ok := <-v.w.Events:
			if !ok {
				return
			}
			v.logEvent(ev)
		case err, ok := <-v.w.Errors:
			if !ok {
				return
			}
			vzlog("vmDirWatcher: error: %v", err)
		}
	}
}

// logEvent records a single event with the operation, path, and a
// truncated Go stack trace from the watcher goroutine. The stack
// trace is from the watcher itself, not the caller that performed
// the FS op — fsnotify cannot capture the originating goroutine.
// We include it anyway in case the watcher is invoked from a known
// thread (e.g., some main-queue handler) that narrows the suspect set.
func (v *vmDirWatcher) logEvent(ev fsnotify.Event) {
	rel := ev.Name
	if r, err := filepath.Rel(v.parent, ev.Name); err == nil {
		rel = r
	}
	op := ev.Op.String()
	// Highlight the bug-relevant ops: REMOVE / RENAME of the vmDir or
	// its disk.img. Other events still log but with less alarm.
	target := ev.Name
	flag := ""
	if target == v.vmDir || strings.HasSuffix(target, string(filepath.Separator)+"disk.img") {
		flag = " [CRITICAL]"
	}
	vzlog("vmDirWatcher: event op=%s path=%q rel=%q%s", op, ev.Name, rel, flag)
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		// Stack-trace dump on remove/rename — most likely to identify
		// the caller if our process is responsible. Truncated to keep
		// the log readable.
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		stack := string(buf[:n])
		// Print only frames inside our module (drop runtime + fsnotify).
		lines := strings.Split(stack, "\n")
		var keep []string
		for _, ln := range lines {
			if strings.Contains(ln, "vz-macos") {
				keep = append(keep, ln)
			}
		}
		if len(keep) > 0 {
			vzlog("vmDirWatcher: watcher-goroutine stack (vz-macos frames):\n%s", strings.Join(keep, "\n"))
		}
	}
}

// Stop terminates the watcher goroutine and closes the underlying
// fsnotify watcher. Safe to call multiple times.
func (v *vmDirWatcher) Stop() {
	v.once.Do(func() {
		close(v.stopCh)
		if v.w != nil {
			_ = v.w.Close()
		}
		v.wg.Wait()
		vzlog("vmDirWatcher: stopped")
	})
}
