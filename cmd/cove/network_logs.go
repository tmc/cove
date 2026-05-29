package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/metrics"
)

type networkLogsArgs struct {
	VM     string
	Follow bool
}

func parseNetworkLogsArgs(args []string) (networkLogsArgs, error) {
	fs := flag.NewFlagSet("network logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	follow := fs.Bool("f", false, "follow")
	fs.BoolVar(follow, "follow", false, "follow")
	if err := fs.Parse(args); err != nil {
		return networkLogsArgs{}, err
	}
	if fs.NArg() != 1 {
		return networkLogsArgs{}, errors.New("usage: cove network logs <vm> [-f]")
	}
	vm := strings.TrimSpace(fs.Arg(0))
	if vm == "" || filepath.Base(vm) != vm {
		return networkLogsArgs{}, fmt.Errorf("network logs: invalid vm name %q", vm)
	}
	return networkLogsArgs{VM: vm, Follow: *follow}, nil
}

func PrintNetworkLogs(w io.Writer, vm string, follow bool) error {
	path, err := newestNetworkAuditLogForVM(vm)
	if err != nil {
		return err
	}
	if err := printNetworkAuditPath(w, path); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	return followNetworkAuditPath(w, path)
}

func printNetworkAuditPath(w io.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("network logs: read %s: %w", path, err)
	}
	_, err = w.Write(data)
	return err
}

type networkAuditLogCandidate struct {
	path    string
	started time.Time
	modTime time.Time
}

func newestNetworkAuditLogForVM(vm string) (string, error) {
	vm = strings.TrimSpace(vm)
	if vm == "" || filepath.Base(vm) != vm {
		return "", fmt.Errorf("network logs: invalid vm name %q", vm)
	}
	root := runsDirHook()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no network audit logs for VM %q", vm)
		}
		return "", fmt.Errorf("network logs: read runs dir: %w", err)
	}
	var matches []networkAuditLogCandidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		logPath := filepath.Join(dir, "network.log")
		st, err := os.Stat(logPath)
		if err != nil {
			continue
		}
		runVM, started, err := runVMNameAndStart(dir)
		if err != nil || runVM != vm {
			continue
		}
		matches = append(matches, networkAuditLogCandidate{path: logPath, started: started, modTime: st.ModTime()})
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no network audit logs for VM %q", vm)
	}
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].started.Equal(matches[j].started) {
			return matches[i].started.After(matches[j].started)
		}
		return matches[i].modTime.After(matches[j].modTime)
	})
	return matches[0].path, nil
}

func runVMNameAndStart(dir string) (string, time.Time, error) {
	f, err := os.Open(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	var vm string
	var started time.Time
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var event metrics.Event
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			return "", time.Time{}, err
		}
		if vm == "" {
			vm = event.VMName
		}
		if started.IsZero() {
			started, _ = time.Parse(time.RFC3339Nano, event.Timestamp)
		}
		if vm != "" && !started.IsZero() {
			break
		}
	}
	if err := scan.Err(); err != nil {
		return "", time.Time{}, err
	}
	return vm, started, nil
}

func followNetworkAuditPath(w io.Writer, path string) error {
	var off int64
	if st, err := os.Stat(path); err == nil {
		off = st.Size()
	}
	for {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("network logs: follow %s: %w", path, err)
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return fmt.Errorf("network logs: seek %s: %w", path, err)
		}
		n, err := io.Copy(w, f)
		f.Close()
		off += n
		if err != nil {
			return fmt.Errorf("network logs: follow %s: %w", path, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
