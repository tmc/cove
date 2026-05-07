package controlclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	TokenFileName = "control.token"
	TokenEnvVar   = "VZ_MACOS_CTL_TOKEN"
)

// LoadTokenFromPath reads a control token file and trims whitespace.
func LoadTokenFromPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// FormatDialError rewrites low-level dial errors with VM-aware guidance.
func FormatDialError(sock string, err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())
	runHint := vmRunHintForSocket(sock)

	if os.IsNotExist(err) || strings.Contains(msg, "no such file or directory") {
		return fmt.Errorf("vm is not running: control socket not found at %s\n  start it with: %s", sock, runHint)
	}

	if strings.Contains(msg, "connection refused") {
		vmName := filepath.Base(filepath.Dir(sock))
		if _, statErr := os.Stat(sock); statErr == nil {
			return fmt.Errorf("vm %q control socket exists but is not accepting connections at %s\n  vm may still be booting or may have exited uncleanly\n  if booting: retry in a few seconds\n  if exited: restart with: %s", vmName, sock, runHint)
		}
		return fmt.Errorf("vm %q is not running: control socket unavailable at %s\n  start it with: %s", vmName, sock, runHint)
	}

	if strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "operation timed out") ||
		strings.Contains(msg, "resource temporarily unavailable") {
		return fmt.Errorf("vm control socket is present but not ready at %s\n  vm may still be booting; retry shortly", sock)
	}

	return fmt.Errorf("connect to control socket %s: %w", sock, err)
}

func RunHintForSocket(sock string) string {
	vmName := filepath.Base(filepath.Dir(sock))
	if vmName == "" || vmName == "." || vmName == string(filepath.Separator) {
		return "cove run"
	}
	return fmt.Sprintf("cove -vm %s run", vmName)
}

func vmRunHintForSocket(sock string) string {
	return RunHintForSocket(sock)
}

type SharedFoldersRuntimeStatus struct {
	Running  bool   `json:"running"`
	VirtioFS bool   `json:"virtiofs"`
	State    string `json:"state,omitempty"`
	Message  string `json:"message,omitempty"`
}
