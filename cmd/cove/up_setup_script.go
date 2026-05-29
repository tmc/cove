// up_setup_script.go — Run a plain text setup script via the guest agent.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// parseSetupScript reads a setup script and returns its non-blank,
// non-comment lines in order. Lines whose first non-whitespace
// character is '#' are treated as comments and skipped.
func parseSetupScript(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// loadSetupScript reads and parses a setup script file from disk.
func loadSetupScript(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSetupScript(f)
}

// runSetupScript reads a plain text script at path, then for each
// non-blank, non-comment line runs the line as `bash -c <line>` in the
// user-session guest agent. Stdout and stderr stream to the host
// terminal as data arrives. The first line that exits non-zero stops
// execution and returns an error.
func runSetupScript(path string) error {
	lines, err := loadSetupScript(path)
	if err != nil {
		return fmt.Errorf("read setup script: %w", err)
	}
	if len(lines) == 0 {
		fmt.Printf("setup-script %s: no commands\n", path)
		return nil
	}

	cfg := vzscriptConfig{
		socketPath:  GetControlSocketPath(),
		execTimeout: 30 * time.Minute,
	}
	onStdout := func(chunk []byte) { _, _ = os.Stdout.Write(chunk) }
	onStderr := func(chunk []byte) { _, _ = os.Stderr.Write(chunk) }

	for i, line := range lines {
		fmt.Printf("\n[setup-script %d/%d] %s\n", i+1, len(lines), line)
		_, _, exitCode, err := guestExecStream(
			cfg,
			[]string{"/bin/bash", "-c", line},
			cfg.execTimeout,
			onStdout,
			onStderr,
		)
		if err != nil {
			return fmt.Errorf("setup-script line %d: %w", i+1, err)
		}
		if exitCode != 0 {
			return fmt.Errorf("setup-script line %d exited %d: %s", i+1, exitCode, line)
		}
	}
	return nil
}
