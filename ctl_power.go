package main

import (
	"fmt"
	"strconv"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func ctlPowerCommand(sock string, args []string, timeout time.Duration, raw bool) error {
	if len(args) < 1 {
		return fmt.Errorf("power requires action: status, keep-awake, or allow-sleep")
	}

	var script string
	switch args[0] {
	case "status":
		script = guestPowerStatusScript()
	case "keep-awake":
		script = guestPowerKeepAwakeScript()
	case "allow-sleep":
		minutes := 10
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid sleep minutes: %q", args[1])
			}
			minutes = n
		}
		script = guestPowerAllowSleepScript(minutes)
	default:
		return fmt.Errorf("unknown power action: %s (use status, keep-awake, or allow-sleep)", args[0])
	}

	req := &controlpb.ControlRequest{
		Type:      "agent-exec",
		AuthToken: resolveControlTokenForSocket(sock),
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"/bin/sh", "-lc", script},
			},
		},
	}
	resp, err := ctlSendRequest(sock, req, timeout, "power")
	if err != nil {
		return err
	}
	if !resp.Success {
		return ctlAgentCommandError(sock, "agent-exec", resp.Error)
	}
	return ctlPrintResponse(resp, "agent-exec", raw, "")
}

func guestPowerStatusScript() string {
	return `pmset -g custom
printf '\nScreen saver idleTime: '
defaults read /Library/Preferences/com.apple.screensaver idleTime 2>/dev/null || echo unset`
}

func guestPowerKeepAwakeScript() string {
	return `pmset -a displaysleep 0 sleep 0 disksleep 0 disablesleep 1
defaults write /Library/Preferences/com.apple.screensaver idleTime -int 0
echo 'guest display sleep disabled'
echo
` + guestPowerStatusScript()
}

func guestPowerAllowSleepScript(minutes int) string {
	return fmt.Sprintf(`pmset -a disablesleep 0
pmset -a displaysleep %[1]d sleep %[1]d disksleep %[1]d
defaults write /Library/Preferences/com.apple.screensaver idleTime -int %[2]d
echo 'guest display sleep restored'
echo
%[3]s`, minutes, minutes*60, guestPowerStatusScript())
}
