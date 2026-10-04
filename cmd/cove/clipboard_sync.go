package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/applicationservices"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
	agentstate "github.com/tmc/cove/internal/agent"
)

const clipboardTextLimit = 1 << 20
const clipboardFlavorPromised applicationservices.PasteboardFlavorFlags = 1 << 9

type clipboardText struct {
	change    int
	text      string
	available bool
	pending   bool
}

type clipboardPushState struct {
	change int
	synced bool
}

func (s *clipboardPushState) sync(text clipboardText, push func(string) error) error {
	if text.pending {
		return nil
	}
	if s.synced && text.change == s.change {
		return nil
	}
	if text.available && len(text.text) <= clipboardTextLimit {
		if err := push(text.text); err != nil {
			return err
		}
	}
	s.change, s.synced = text.change, true
	return nil
}

type hostClipboardReader struct {
	board applicationservices.PasteboardRef
}

func (r *hostClipboardReader) close() {
	DispatchAsyncMain(func() {
		if r.board != 0 {
			corefoundation.CFRelease(unsafe.Pointer(r.board))
			r.board = 0
		}
	})
}

func (r *hostClipboardReader) read(ctx context.Context) (clipboardText, error) {
	result := make(chan clipboardText, 1)
	DispatchAsyncMain(func() {
		if ctx.Err() != nil {
			return
		}
		var text clipboardText
		objc.AutoreleasePool(func() {
			board := appkit.GetNSPasteboardClass().GeneralPasteboard()
			text.change = board.ChangeCount()
			if r.board == 0 {
				name := corefoundation.CFStringRef(objc.String("com.apple.pasteboard.clipboard"))
				if applicationservices.PasteboardCreate(name, &r.board) != 0 || r.board == 0 {
					text.pending = true
					return
				}
			}
			text = readClipboardText(r.board, text.change)
			if board.ChangeCount() != text.change {
				text.available, text.pending = false, true
			}
		})
		result <- text
	})
	select {
	case text := <-result:
		return text, nil
	case <-ctx.Done():
		return clipboardText{}, ctx.Err()
	}
}

func readClipboardText(board applicationservices.PasteboardRef, change int) clipboardText {
	text := clipboardText{change: change}
	applicationservices.PasteboardSynchronize(board)
	var count uint
	if applicationservices.PasteboardGetItemCount(board, unsafe.Pointer(&count)) != 0 {
		text.pending = true
		return text
	}
	if count == 0 {
		return text
	}
	var item applicationservices.PasteboardItemID
	if applicationservices.PasteboardGetItemIdentifier(board, 1, &item) != 0 {
		text.pending = true
		return text
	}
	typeName := corefoundation.CFStringRef(objc.String("public.utf8-plain-text"))
	var flags applicationservices.PasteboardFlavorFlags
	if applicationservices.PasteboardGetItemFlavorFlags(board, item, typeName, &flags) != 0 {
		return text
	}
	// Resolving a promise can reenter Virtualization's SPICE reader and throw
	// an Objective-C exception. Poll only materialized data on this snapshot.
	if flags&clipboardFlavorPromised != 0 {
		text.pending = true
		return text
	}
	var data corefoundation.CFDataRef
	if applicationservices.PasteboardCopyItemFlavorData(board, item, typeName, &data) != 0 || data == 0 {
		text.pending = true
		return text
	}
	defer corefoundation.CFRelease(unsafe.Pointer(data))
	n := int(corefoundation.CFDataGetLength(data))
	if n < 0 || n > clipboardTextLimit {
		return text
	}
	p := corefoundation.CFDataGetBytePtr(data)
	if p == nil && n != 0 {
		text.pending = true
		return text
	}
	value := unsafe.Slice(p, n)
	if utf8.Valid(value) {
		text.text, text.available = string(value), true
	}
	return text
}

func hostClipboardChangeCount() int {
	return appkit.GetNSPasteboardClass().GeneralPasteboard().ChangeCount()
}

func (s *ControlServer) monitorHostClipboard() {
	s.mu.Lock()
	enabled := s.runConfig.EnableClipboard && s.hostConfig.RuntimeProfile != "minimal" && agentstate.Platform(s.vmDir) == agentstate.PlatformMacOS
	s.mu.Unlock()
	if !enabled {
		return
	}
	ctx := s.lifecycleContext()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var state clipboardPushState
	var reader hostClipboardReader
	defer reader.close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.bridge.HealthSnapshot().UserStatus != "connected" {
			continue
		}
		if state.synced && hostClipboardChangeCount() == state.change {
			continue
		}
		text, err := reader.read(ctx)
		if err != nil {
			return
		}
		if err := state.sync(text, func(value string) error {
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := s.pushHostClipboardText(callCtx, text.change, value)
			s.clipboardMonitor.record(err)
			return err
		}); err != nil {
			slog.Debug("clipboard: host text sync unavailable", "error", err)
		}
	}
}

func (s *ControlServer) pushHostClipboardText(ctx context.Context, change int, text string) error {
	user, err := s.getUserAgent()
	if err != nil {
		return err
	}
	current, err := user.UserExec(ctx, []string{"/usr/bin/pbpaste"}, nil, "")
	if err != nil {
		return fmt.Errorf("read guest clipboard: %w", err)
	}
	if current.ExitCode != 0 {
		return fmt.Errorf("read guest clipboard: exit %d", current.ExitCode)
	}
	if bytes.Equal(current.Stdout, []byte(text)) {
		return nil
	}
	if hostClipboardChangeCount() != change {
		return fmt.Errorf("host clipboard changed during sync")
	}
	result, err := user.UserExecWithStdin(ctx, []string{"/usr/bin/pbcopy"}, nil, "", []byte(text))
	if err != nil {
		return fmt.Errorf("write guest clipboard: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("write guest clipboard: exit %d", result.ExitCode)
	}
	return nil
}
