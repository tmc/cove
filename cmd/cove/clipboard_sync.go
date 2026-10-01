package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	agentstate "github.com/tmc/cove/internal/agent"
)

const clipboardTextLimit = 1 << 20

type clipboardText struct {
	change    int
	text      string
	available bool
}

type clipboardPushState struct {
	change int
	synced bool
}

func (s *clipboardPushState) sync(text clipboardText, push func(string) error) error {
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

func hostClipboardText() clipboardText {
	var text clipboardText
	objc.AutoreleasePool(func() {
		board := appkit.GetNSPasteboardClass().GeneralPasteboard()
		text.change = board.ChangeCount()
		id := objc.Send[objc.ID](board.ID, objc.Sel("stringForType:"), objc.String("public.utf8-plain-text"))
		if id != 0 {
			text.available = true
			text.text = foundation.NSStringFromID(id).String()
		}
	})
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
		text := hostClipboardText()
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
