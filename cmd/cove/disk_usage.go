package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type diskSpace struct {
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	Status     string `json:"status"`
}

type diskUsageReport struct {
	VM                  string     `json:"vm"`
	ImagePath           string     `json:"image_path"`
	ImageFileBytes      uint64     `json:"image_file_bytes"`
	ImageAllocatedBytes uint64     `json:"image_allocated_bytes"`
	Host                diskSpace  `json:"host"`
	Guest               *diskSpace `json:"guest,omitempty"`
	APFS                string     `json:"apfs,omitempty"`
	ResizeBlocker       string     `json:"resize_blocker,omitempty"`
	Scan                string     `json:"scan,omitempty"`
	Warnings            []string   `json:"warnings,omitempty"`
	Next                []string   `json:"next"`
}

func diskSpaceStatus(total, free uint64) string {
	if free < 1<<30 || total > 0 && float64(free)/float64(total) < .02 {
		return "critical"
	}
	if free < 10<<30 || total > 0 && float64(free)/float64(total) < .1 {
		return "low"
	}
	return "ok"
}

func parseDiskDF(out string) (*diskSpace, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return nil, fmt.Errorf("expected one filesystem in df output")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 6 {
		return nil, fmt.Errorf("incomplete df output")
	}
	values := make([]uint64, 3)
	for i := range values {
		n, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil || n > ^uint64(0)/1024 {
			return nil, fmt.Errorf("invalid df byte count %q", fields[i+1])
		}
		values[i] = n * 1024
	}
	return &diskSpace{TotalBytes: values[0], UsedBytes: values[1], FreeBytes: values[2], Status: diskSpaceStatus(values[0], values[2])}, nil
}

func diskUsageTarget(args []string, fs *flag.FlagSet) (string, string, error) {
	if fs.NArg() > 1 {
		return "", "", fmt.Errorf("expected at most one VM name")
	}
	name := vmName
	if fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name == "" {
		return "", "", fmt.Errorf("VM required: cove disk %s <vm>", args[0])
	}
	dir, err := requireExistingVMDir("disk "+args[0], name)
	return name, dir, err
}

func runDiskUsage(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("disk usage", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON")
	check := fs.Bool("check", false, "return failure when guest space is low, critical, or unavailable")
	scanPath := fs.String("scan-path", "", "absolute guest directory to scan instead of the data volume root")
	scan := fs.Bool("scan", false, "scan top-level guest data directories (read-only, up to two minutes)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove disk usage [-json] [-check] [-scan] [-scan-path path] <vm>\n\nReport guest free space, host free space, backing image allocation and APFS layout.\nA stopped or disconnected guest reports host information with a warning.")
	}
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	name, dir, err := diskUsageTarget([]string{"usage"}, fs)
	if err != nil {
		return err
	}
	if *scanPath != "" && !strings.HasPrefix(*scanPath, "/") {
		return fmt.Errorf("scan path must be an absolute guest path")
	}
	report, err := collectDiskUsage(name, dir, *scan || *scanPath != "", *scanPath)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(report)
	} else {
		err = writeDiskUsage(env.Stdout, report)
	}
	if err != nil {
		return err
	}
	if *check && (report.Guest == nil || report.Guest.Status != "ok") {
		return fmt.Errorf("guest disk space is low or unavailable; see disk usage report")
	}
	return nil
}

func collectDiskUsage(name, dir string, scan bool, scanPath string) (diskUsageReport, error) {
	r := diskUsageReport{VM: name, ImagePath: vmPrimaryDiskPath(dir)}
	info, err := os.Stat(r.ImagePath)
	if err != nil {
		return r, fmt.Errorf("inspect disk image: %w", err)
	}
	if info.IsDir() {
		return r, fmt.Errorf("disk image is a directory")
	}
	r.ImageFileBytes = uint64(info.Size())
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		r.ImageAllocatedBytes = uint64(st.Blocks) * 512
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return r, fmt.Errorf("inspect host free space: %w", err)
	}
	r.Host = diskSpace{TotalBytes: uint64(st.Blocks) * uint64(st.Bsize), UsedBytes: uint64(st.Blocks-st.Bfree) * uint64(st.Bsize), FreeBytes: uint64(st.Bavail) * uint64(st.Bsize)}
	r.Host.Status = diskSpaceStatus(0, r.Host.FreeBytes)
	client := NewControlClient(GetControlSocketPathForVM(dir))
	client.SetTimeout(5 * time.Second)
	if _, err := client.AgentPingTyped(); err != nil {
		r.Warnings = append(r.Warnings, "guest capacity unavailable: "+err.Error())
		r.Next = append(r.Next, "start the VM and wait for its guest agent, then rerun disk usage")
		return r, nil
	}
	platform := vmconfig.DetectOSType(dir)
	if platform == "Windows" {
		r.Warnings = append(r.Warnings, "guest space inspection currently supports macOS and Linux; use Get-Volume inside Windows")
		return r, nil
	}
	root := "/"
	if platform == "macOS" {
		root = "/System/Volumes/Data"
	}
	result, err := client.AgentDaemonExecTypedTimeout([]string{"df", "-Pk", root}, map[string]string{"LC_ALL": "C"}, "", 15*time.Second)
	if err == nil {
		err = checkDiskExec(result)
	}
	if err == nil {
		r.Guest, err = parseDiskDF(result.Stdout)
	}
	if err != nil {
		r.Warnings = append(r.Warnings, "guest capacity unavailable: "+err.Error())
	}
	if platform == "macOS" {
		res, e := client.AgentDaemonExecTypedTimeout([]string{"/usr/sbin/diskutil", "info", "/"}, map[string]string{"LC_ALL": "C"}, "", 15*time.Second)
		if e == nil {
			e = checkDiskExec(res)
		}
		if e == nil {
			for _, line := range strings.Split(res.Stdout, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "APFS Container:") || strings.HasPrefix(line, "APFS Physical Store:") || strings.HasPrefix(line, "Container Total Space:") || strings.HasPrefix(line, "Container Free Space:") {
					r.APFS += line + "\n"
				}
			}
		} else {
			r.Warnings = append(r.Warnings, "APFS inspection: "+e.Error())
		}
		if e == nil {
			store := diskInfoValue(res.Stdout, "APFS Physical Store")
			disk, _, ok := splitDiskPartition(store)
			if ok && strings.HasPrefix(disk, "disk") {
				layout, layoutErr := client.AgentDaemonExecTypedTimeout([]string{"/usr/sbin/diskutil", "list", "/dev/" + disk}, map[string]string{"LC_ALL": "C"}, "", 15*time.Second)
				if layoutErr == nil {
					layoutErr = checkDiskExec(layout)
				}
				if layoutErr == nil {
					r.ResizeBlocker = diskRecoveryBlocker(layout.Stdout, store)
				} else {
					r.Warnings = append(r.Warnings, "partition layout unavailable: "+layoutErr.Error())
				}
			}
		}
	}
	if scan {
		if scanPath == "" {
			scanPath = root
		}
		res, e := client.AgentDaemonExecTypedTimeout([]string{"du", "-x", "-k", "-d", "1", scanPath}, map[string]string{"LC_ALL": "C"}, "", 2*time.Minute)
		if res != nil {
			r.Scan = res.Stdout
		}
		if e == nil {
			e = checkDiskExec(res)
		}
		if e != nil {
			r.Warnings = append(r.Warnings, "directory scan incomplete: "+e.Error())
		}
	}
	if r.Guest != nil && r.Guest.Status != "ok" {
		r.Next = append(r.Next, fmt.Sprintf("cove disk clean %s (preview regenerable Go build cache)", name), fmt.Sprintf("cove ctl -vm %s disk resize --preflight 0 <size> (check APFS layout before growing)", name))
	}
	if r.ResizeBlocker != "" {
		r.Warnings = append(r.Warnings, "APFS expansion blocked by Recovery partition "+r.ResizeBlocker+"; growing the image alone will not add guest free space")
		r.Next = append(r.Next, "reclaim selected regenerable caches or migrate data to a shared folder; preserve Recovery")
	}
	if r.Host.Status != "ok" {
		r.Next = append(r.Next, "cove storage census", "cove storage prune (dry run; host storage only)")
	}
	r.Next = append(r.Next, "cove compact prepares free blocks for smaller image uploads; it does not add guest free space")
	return r, nil
}

func diskInfoValue(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && label == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func splitDiskPartition(id string) (string, string, bool) {
	i := strings.LastIndexByte(id, 's')
	if i < 5 || !strings.HasPrefix(id, "disk") {
		return "", "", false
	}
	if _, err := strconv.ParseUint(id[4:i], 10, 32); err != nil {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

func diskRecoveryBlocker(out, store string) string {
	disk, part, ok := splitDiskPartition(store)
	n, err := strconv.ParseUint(part, 10, 32)
	if !ok || err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Apple_APFS_Recovery") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[len(fields)-1]
		d, p, ok := splitDiskPartition(id)
		pn, err := strconv.ParseUint(p, 10, 32)
		if ok && err == nil && d == disk && pn > n {
			return id
		}
	}
	return ""
}

func checkDiskExec(r *controlpb.AgentExecResponse) error {
	if r == nil {
		return fmt.Errorf("missing guest response")
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("guest command exited %d: %s", r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return nil
}

func writeDiskUsage(dst io.Writer, r diskUsageReport) error {
	var buf strings.Builder
	w := &buf
	fmt.Fprintf(w, "Disk space: %s\n", r.VM)
	fmt.Fprintf(w, "  Host: %s free (%s)\n", runtimeDiskFormatBytes(r.Host.FreeBytes), r.Host.Status)
	fmt.Fprintf(w, "  Image: %s file length, %s allocated on host\n", runtimeDiskFormatBytes(r.ImageFileBytes), runtimeDiskFormatBytes(r.ImageAllocatedBytes))
	fmt.Fprintln(w, "  "+r.ImagePath)
	if r.Guest != nil {
		fmt.Fprintf(w, "  Guest: %s free of %s (%s)\n", runtimeDiskFormatBytes(r.Guest.FreeBytes), runtimeDiskFormatBytes(r.Guest.TotalBytes), r.Guest.Status)
	}
	if r.APFS != "" {
		fmt.Fprintln(w, strings.TrimSpace(r.APFS))
	}
	if r.Scan != "" {
		fmt.Fprintln(w, "Guest directory sizes (KiB; same filesystem only):")
		fmt.Fprint(w, r.Scan)
	}
	for _, s := range r.Warnings {
		fmt.Fprintln(w, "Warning: "+s)
	}
	for _, s := range r.Next {
		fmt.Fprintln(w, "Next: "+s)
	}
	_, err := io.WriteString(dst, buf.String())
	return err
}

const guestGoTool = `tool=$(command -v go || true)
if [ -z "$tool" ]; then
 for candidate in /usr/local/go/bin/go /opt/homebrew/bin/go /usr/local/bin/go; do
  if [ -x "$candidate" ]; then tool=$candidate; break; fi
 done
fi
[ -n "$tool" ] || { echo 'go tool unavailable for this signed-in user' >&2; exit 1; }
`

func runDiskClean(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("disk clean", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	apply := fs.Bool("apply", false, "remove the selected regenerable cache")
	cache := fs.String("cache", "go-build", "go-build or go-modules (modules require downloading again)")
	scope := fs.String("scope", "user", "user or daemon; daemon targets standard root Go cache paths")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove disk clean [-cache go-build|go-modules] [-scope user|daemon] [-apply] <vm>\n\nPreview a regenerable Go cache. -apply uses go clean for the selected cache.\nModule cleanup requires downloading dependencies again. Documents and snapshots remain intact.")
	}
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	name, dir, err := diskUsageTarget([]string{"clean"}, fs)
	if err != nil {
		return err
	}
	if vmconfig.DetectOSType(dir) == "Windows" {
		return fmt.Errorf("disk clean currently supports macOS and Linux")
	}
	if *cache != "go-build" && *cache != "go-modules" {
		return fmt.Errorf("unknown cache %q: use go-build or go-modules", *cache)
	}
	if *scope != "user" && *scope != "daemon" {
		return fmt.Errorf("unknown scope %q: use user or daemon", *scope)
	}
	client := NewControlClient(GetControlSocketPathForVM(dir))
	script := diskCleanScript(*cache, *scope, *apply)
	execCache := client.AgentUserExecTypedTimeout
	var cacheEnvVars map[string]string
	if *scope == "daemon" {
		execCache = client.AgentDaemonExecTypedTimeout
		cacheEnvVars = map[string]string{"GOCACHE": "/root/.cache/go-build", "GOPATH": "/root/go", "GOMODCACHE": "/root/go/pkg/mod"}
		if vmconfig.DetectOSType(dir) == "macOS" {
			cacheEnvVars = map[string]string{"GOCACHE": "/var/root/Library/Caches/go-build", "GOPATH": "/var/root/go", "GOMODCACHE": "/var/root/go/pkg/mod"}
		}
	}
	res, err := execCache([]string{"/bin/sh", "-c", script}, cacheEnvVars, "", 2*time.Minute)
	if err != nil {
		return fmt.Errorf("inspect/clean %s cache: %w", *scope, err)
	}
	if res != nil {
		fmt.Fprint(env.Stdout, res.Stdout)
	}
	if err := checkDiskExec(res); err != nil {
		if *apply {
			return fmt.Errorf("cache cleanup may have partially completed: %w; run cove disk usage %s to check free space; permission errors require reviewing cache ownership", err, name)
		}
		return err
	}
	if !*apply {
		fmt.Fprintf(env.Stdout, "Preview only. Apply with: cove disk clean -cache %s -scope %s -apply %s\n", *cache, *scope, name)
	} else {
		return runDiskUsage(env, []string{name})
	}
	return nil
}

func diskCleanScript(cache, scope string, apply bool) string {
	cacheEnv, cleanFlag := "GOCACHE", "-cache"
	if cache == "go-modules" {
		cacheEnv, cleanFlag = "GOMODCACHE", "-modcache"
	}
	script := guestGoTool + `cache=$("$tool" env GOCACHE) || exit
[ -n "$cache" ] || { echo "cache location unavailable in this agent environment" >&2; exit 1; }
[ "$cache" != off ] || { echo 'Go build cache disabled'; exit 0; }
case "$cache" in
 /*) ;;
 *) echo 'cache path must be absolute' >&2; exit 1 ;;
esac
case "$cache" in
 /|/Users|/var/root|/root|/Volumes|/mnt) echo 'refusing unsafe cache root' >&2; exit 1 ;;
esac
[ -z "$HOME" ] || [ "$cache" != "$HOME" ] || { echo 'refusing home directory as cache' >&2; exit 1; }
printf 'Go build cache: %s\n' "$cache"
if [ -d "$cache" ]; then du -sk "$cache" || exit; fi
`
	script = strings.ReplaceAll(script, "GOCACHE", cacheEnv)
	script = strings.ReplaceAll(script, "Go build cache", "Go cache ("+cache+", "+scope+")")
	if apply {
		script += `"$tool" clean ` + cleanFlag + ` || exit
printf 'Selected Go cache cleared\n'
`
	}
	return script
}
