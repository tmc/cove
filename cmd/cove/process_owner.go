package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type runtimeOwnerInfo struct {
	PID           int
	PPID          int
	SessionID     int
	Command       string
	ParentCommand string
	StartSource   string
	StartedAt     time.Time
}

func currentRuntimeOwnerInfo() runtimeOwnerInfo {
	pid := os.Getpid()
	info := runtimeOwnerInfo{
		PID:     pid,
		PPID:    os.Getppid(),
		Command: strings.Join(os.Args, " "),
	}
	if ps, ok := processInfo(pid); ok {
		if ps.PPID != 0 {
			info.PPID = ps.PPID
		}
		info.SessionID = ps.SessionID
		if strings.TrimSpace(ps.Command) != "" {
			info.Command = ps.Command
		}
		info.StartedAt = ps.StartedAt
	}
	info.ParentCommand = processCommand(info.PPID)
	info.StartSource = runtimeStartSource(info.Command)
	return info
}

type hostProcessInfo struct {
	PPID      int
	SessionID int
	Command   string
	StartedAt time.Time
}

func processInfo(pid int) (hostProcessInfo, bool) {
	if pid <= 0 {
		return hostProcessInfo{}, false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=,sess=,command=").Output()
	if err != nil {
		return hostProcessInfo{}, false
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 3 {
		return hostProcessInfo{}, false
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return hostProcessInfo{}, false
	}
	sessionID, err := strconv.Atoi(fields[1])
	if err != nil {
		return hostProcessInfo{}, false
	}
	return hostProcessInfo{
		PPID:      ppid,
		SessionID: sessionID,
		StartedAt: processStartedAt(pid),
		Command:   strings.Join(fields[2:], " "),
	}, true
}

func processCommand(pid int) string {
	if info, ok := processInfo(pid); ok {
		return info.Command
	}
	return ""
}

func processStartedAt(pid int) time.Time {
	if pid <= 0 {
		return time.Time{}
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return time.Time{}
	}
	started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.Local)
	if err != nil {
		return time.Time{}
	}
	return started.UTC()
}

func runtimeStartSource(command string) string {
	fields := strings.Fields(command)
	for i, field := range fields {
		if i == 0 {
			continue
		}
		switch field {
		case "run":
			return "cove run"
		case "up":
			return "cove up"
		}
	}
	return "cove"
}

func enrichRuntimeServerInfo(info *RuntimeServerInfo) bool {
	if info == nil || info.PID <= 0 {
		return false
	}
	ps, ok := processInfo(info.PID)
	if !ok {
		return false
	}
	changed := false
	if info.PPID == 0 && ps.PPID != 0 {
		info.PPID = ps.PPID
		changed = true
	}
	if info.SessionID == 0 && ps.SessionID != 0 {
		info.SessionID = ps.SessionID
		changed = true
	}
	if strings.TrimSpace(info.Command) == "" && strings.TrimSpace(ps.Command) != "" {
		info.Command = ps.Command
		changed = true
	}
	if strings.TrimSpace(info.ParentCommand) == "" && info.PPID > 0 {
		info.ParentCommand = processCommand(info.PPID)
		if strings.TrimSpace(info.ParentCommand) != "" {
			changed = true
		}
	}
	if strings.TrimSpace(info.StartSource) == "" && strings.TrimSpace(info.Command) != "" {
		info.StartSource = runtimeStartSource(info.Command)
		changed = true
	}
	if strings.TrimSpace(info.StartedAt) == "" && !ps.StartedAt.IsZero() {
		info.StartedAt = ps.StartedAt.Format(time.RFC3339)
		changed = true
	}
	return changed
}
