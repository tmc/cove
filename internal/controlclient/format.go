package controlclient

import (
	"encoding/json"
	"fmt"
	"strings"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func FormatAgentSSHDResponse(resp *controlpb.ControlResponse) string {
	exitCode, stdout, stderr, ok := agentSSHDResult(resp)
	if !ok {
		return ""
	}
	status := sshdStatusFromOutput(stdout, stderr)
	if status == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "exitCode: %d\n", exitCode)
	if errText := strings.TrimSpace(stderr); errText != "" {
		fmt.Fprintf(&b, "stderr: %s\n", errText)
	}
	return b.String()
}

func agentSSHDResult(resp *controlpb.ControlResponse) (int32, string, string, bool) {
	if exec := resp.GetAgentExecResult(); exec != nil {
		return exec.GetExitCode(), exec.GetStdout(), exec.GetStderr(), true
	}
	if strings.TrimSpace(resp.Data) == "" {
		return 0, "", "", false
	}
	var result struct {
		ExitCode int32  `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &result); err != nil {
		return 0, "", "", false
	}
	return result.ExitCode, result.Stdout, result.Stderr, true
}

func sshdStatusFromOutput(stdout, stderr string) string {
	text := strings.TrimSpace(stdout)
	if text == "" {
		text = strings.TrimSpace(stderr)
	}
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "active", "inactive", "failed", "activating", "deactivating", "unknown":
			return line
		}
		if strings.HasPrefix(line, "Active:") {
			fields := strings.Fields(strings.TrimPrefix(line, "Active:"))
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// FormatOperationsResponse renders Result.Operation or Result.OperationsList.
// It returns "" when the response carries neither field.
func FormatOperationsResponse(resp *controlpb.ControlResponse) string {
	if op := resp.GetOperation(); op != nil {
		return formatOperationInfo(op)
	}
	if list := resp.GetOperationsList(); list != nil {
		ops := list.GetOperations()
		if len(ops) == 0 {
			return "no operations\n"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%-22s %-10s %-24s %s\n", "ID", "STATUS", "RESOURCE", "UPDATED")
		for _, op := range ops {
			fmt.Fprintf(&b, "%-22s %-10s %-24s %s\n", op.GetId(), op.GetStatus(), op.GetResource(), op.GetUpdatedAt())
		}
		return b.String()
	}
	return ""
}

func formatOperationInfo(op *controlpb.OperationInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id:        %s\n", op.GetId())
	fmt.Fprintf(&b, "status:    %s\n", op.GetStatus())
	fmt.Fprintf(&b, "resource:  %s\n", op.GetResource())
	if v := op.GetCreatedAt(); v != "" {
		fmt.Fprintf(&b, "created:   %s\n", v)
	}
	if v := op.GetUpdatedAt(); v != "" {
		fmt.Fprintf(&b, "updated:   %s\n", v)
	}
	if v := op.GetErrorCode(); v != "" {
		fmt.Fprintf(&b, "errorCode: %s\n", v)
	}
	if v := op.GetErrorMessage(); v != "" {
		fmt.Fprintf(&b, "error:     %s\n", v)
	}
	return b.String()
}
