package main

import (
	"strings"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// guestSystemTCCDB is readable only by processes with Full Disk Access,
// including root. Opening it in the agent process is direct FDA evidence.
const guestSystemTCCDB = "/Library/Application Support/com.apple.TCC/TCC.db"

// tccFDAStatus is the outcome of an in-process Full Disk Access probe.
type tccFDAStatus string

const (
	tccFDAAuthorized tccFDAStatus = "authorized"
	tccFDADenied     tccFDAStatus = "denied"
	tccFDAUnknown    tccFDAStatus = "unknown"
)

// handleAgentTCCFDAProbe asks the root agent to open the system TCC
// database in its own process. No file contents leave the guest. Agent
// acquisition and the open share one budget; failing to reach the agent
// reports unknown, never denied.
func (s *ControlServer) handleAgentTCCFDAProbe() *controlpb.ControlResponse {
	ctx, cancel := s.timeoutContext(tccFDAProbeBudget)
	defer cancel()
	status, detail := tccFDAUnknown, ""
	if a, err := s.bridge.GetAgentContext(ctx); err != nil {
		detail = "agent unavailable: " + err.Error()
	} else {
		status, detail = classifyTCCFDAOpen(a.OpenFile(ctx, guestSystemTCCDB))
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    formatTCCFDAProbe(status, detail),
	}
}

const tccFDAProbeBudget = 15 * time.Second

func classifyTCCFDAOpen(err error) (tccFDAStatus, string) {
	if err == nil {
		return tccFDAAuthorized, ""
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "operation not permitted") {
		return tccFDADenied, msg
	}
	return tccFDAUnknown, msg
}

func formatTCCFDAProbe(status tccFDAStatus, detail string) string {
	if detail == "" {
		return string(status)
	}
	return string(status) + " " + detail
}

func parseTCCFDAProbe(data string) (tccFDAStatus, string) {
	head, detail, _ := strings.Cut(strings.TrimSpace(data), " ")
	switch status := tccFDAStatus(head); status {
	case tccFDAAuthorized, tccFDADenied:
		return status, detail
	}
	return tccFDAUnknown, strings.TrimSpace(data)
}
