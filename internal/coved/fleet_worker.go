package coved

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/fleetcontrol"
)

const (
	DefaultFleetHeartbeatInterval     = 10 * time.Second
	DefaultFleetAssignmentInterval    = 5 * time.Second
	DefaultFleetAssignmentTimeout     = 30 * time.Minute
	DefaultFleetAssignmentOutputLimit = 64 << 10
)

type FleetWorkerConfig struct {
	ControllerURL      string
	ID                 string
	Host               string
	Version            string
	VMRoot             string
	ImageRoot          string
	Labels             map[string]string
	HTTPClient         *http.Client
	Log                *slog.Logger
	CoveBin            string
	HeartbeatInterval  time.Duration
	AssignmentInterval time.Duration
	AssignmentTimeout  time.Duration
	OutputLimit        int64
}

type FleetWorker struct {
	base               *url.URL
	id                 string
	host               string
	version            string
	vmRoot             string
	imageRoot          string
	labels             map[string]string
	httpClient         *http.Client
	log                *slog.Logger
	coveBin            string
	heartbeatInterval  time.Duration
	assignmentInterval time.Duration
	assignmentTimeout  time.Duration
	outputLimit        int64
}

func NewFleetWorker(cfg FleetWorkerConfig) (*FleetWorker, error) {
	controllerURL := strings.TrimSpace(cfg.ControllerURL)
	if controllerURL == "" {
		return nil, fmt.Errorf("fleet controller url required")
	}
	base, err := url.Parse(controllerURL)
	if err != nil {
		return nil, fmt.Errorf("parse fleet controller url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("fleet controller url must use http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("fleet controller url host required")
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host, _ = os.Hostname()
	}
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = host
	}
	if id == "" {
		return nil, fmt.Errorf("fleet worker id required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	heartbeatInterval := cfg.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = DefaultFleetHeartbeatInterval
	}
	assignmentInterval := cfg.AssignmentInterval
	if assignmentInterval <= 0 {
		assignmentInterval = DefaultFleetAssignmentInterval
	}
	assignmentTimeout := cfg.AssignmentTimeout
	if assignmentTimeout <= 0 {
		assignmentTimeout = DefaultFleetAssignmentTimeout
	}
	outputLimit := cfg.OutputLimit
	if outputLimit <= 0 {
		outputLimit = DefaultFleetAssignmentOutputLimit
	}
	coveBin := strings.TrimSpace(cfg.CoveBin)
	if coveBin == "" {
		coveBin = "cove"
	}
	return &FleetWorker{
		base:               base,
		id:                 id,
		host:               host,
		version:            strings.TrimSpace(cfg.Version),
		vmRoot:             strings.TrimSpace(cfg.VMRoot),
		imageRoot:          strings.TrimSpace(cfg.ImageRoot),
		labels:             cloneStringMap(cfg.Labels),
		httpClient:         client,
		log:                cfg.Log,
		coveBin:            coveBin,
		heartbeatInterval:  heartbeatInterval,
		assignmentInterval: assignmentInterval,
		assignmentTimeout:  assignmentTimeout,
		outputLimit:        outputLimit,
	}, nil
}

func (w *FleetWorker) Run(ctx context.Context) {
	if err := w.Register(ctx); err != nil {
		w.warn("fleet register", err)
	}
	heartbeat := time.NewTicker(w.heartbeatInterval)
	assignments := time.NewTicker(w.assignmentInterval)
	defer heartbeat.Stop()
	defer assignments.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := w.Heartbeat(ctx); err != nil {
				w.warn("fleet heartbeat", err)
			}
		case <-assignments.C:
			if err := w.PollAssignmentAsync(ctx); err != nil {
				w.warn("fleet assignment", err)
			}
		}
	}
}

func (w *FleetWorker) Register(ctx context.Context) error {
	_, err := w.postHeartbeat(ctx, "/v1/workers/register")
	return err
}

func (w *FleetWorker) Heartbeat(ctx context.Context) error {
	_, err := w.postHeartbeat(ctx, "/v1/workers/heartbeat")
	return err
}

func (w *FleetWorker) AwaitAssignment(ctx context.Context) (*fleetcontrol.Assignment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.endpoint("/v1/workers/"+url.PathEscape(w.id)+"/assignments"), nil)
	if err != nil {
		return nil, fmt.Errorf("create await-assignment request: %w", err)
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("await assignment: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var assignment fleetcontrol.Assignment
		if err := json.NewDecoder(resp.Body).Decode(&assignment); err != nil {
			return nil, fmt.Errorf("decode assignment: %w", err)
		}
		return &assignment, nil
	default:
		return nil, responseError("await assignment", resp)
	}
}

func (w *FleetWorker) PollAssignment(ctx context.Context) error {
	assignment, err := w.AwaitAssignment(ctx)
	if err != nil || assignment == nil {
		return err
	}
	return w.HandleAssignment(ctx, *assignment)
}

func (w *FleetWorker) PollAssignmentAsync(ctx context.Context) error {
	assignment, err := w.AwaitAssignment(ctx)
	if err != nil || assignment == nil {
		return err
	}
	go func() {
		if err := w.HandleAssignment(ctx, *assignment); err != nil {
			w.warn("fleet assignment", err)
		}
	}()
	return nil
}

func (w *FleetWorker) HandleAssignment(ctx context.Context, assignment fleetcontrol.Assignment) error {
	report := fleetcontrol.WorkerReport{
		AssignmentID: assignment.ID,
		Status:       "unsupported",
		Error:        fmt.Sprintf("unsupported assignment verb %q", assignment.Verb),
	}
	switch strings.TrimSpace(assignment.Verb) {
	case "noop":
		report.Status = "complete"
		report.Error = ""
	case "cove":
		report = w.runCoveAssignment(ctx, assignment)
	case "cove-control":
		report = w.runControlAssignment(ctx, assignment)
	}
	if _, err := w.ReportStatus(ctx, report); err != nil {
		return err
	}
	if report.Status == "complete" && assignmentRefreshesImageRefs(assignment) {
		if err := w.Heartbeat(ctx); err != nil {
			w.warn("fleet image refresh heartbeat", err)
		}
	}
	return nil
}

func assignmentRefreshesImageRefs(assignment fleetcontrol.Assignment) bool {
	if strings.TrimSpace(assignment.ImageRef) != "" {
		return true
	}
	return assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "image" && assignment.Args[1] == "gc"
}

func (w *FleetWorker) runCoveAssignment(ctx context.Context, assignment fleetcontrol.Assignment) fleetcontrol.WorkerReport {
	report := fleetcontrol.WorkerReport{
		AssignmentID: assignment.ID,
		Status:       "failed",
		ExitCode:     -1,
	}
	runCtx, cancel := context.WithTimeout(ctx, w.assignmentTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, w.coveBin, assignment.Args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		report.Error = err.Error()
		return report
	}
	activeStatus := "running"
	w.reportAssignmentStatus(runCtx, assignment.ID, activeStatus)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	ticker := time.NewTicker(w.assignmentInterval)
	defer ticker.Stop()
	var err error
	for {
		select {
		case err = <-done:
			goto finished
		case <-ticker.C:
			if activeStatus != "ready" && assignmentNeedsReadyProbe(assignment) && w.warmPoolReady(runCtx, assignment) {
				activeStatus = "ready"
			}
			w.reportAssignmentStatus(runCtx, assignment.ID, activeStatus)
		}
	}
finished:
	report.Stdout = trimReportOutput(stdout.String(), w.outputLimit)
	report.Stderr = trimReportOutput(stderr.String(), w.outputLimit)
	if cmd.ProcessState != nil {
		report.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		report.Error = fmt.Sprintf("assignment timed out after %s", w.assignmentTimeout)
		return w.finishCoveAssignment(ctx, assignment, report)
	}
	if err != nil {
		report.Error = err.Error()
		return w.finishCoveAssignment(ctx, assignment, report)
	}
	report.Status = "complete"
	report.Error = ""
	return w.finishCoveAssignment(ctx, assignment, report)
}

func (w *FleetWorker) runControlAssignment(ctx context.Context, assignment fleetcontrol.Assignment) fleetcontrol.WorkerReport {
	report := fleetcontrol.WorkerReport{
		AssignmentID: assignment.ID,
		Status:       "failed",
		ExitCode:     -1,
	}
	if len(assignment.Args) != 2 {
		report.Error = "control assignment requires vm name and request json"
		return report
	}
	vmName := strings.TrimSpace(assignment.Args[0])
	if vmName == "" {
		report.Error = "control assignment vm name required"
		return report
	}
	request, err := w.controlRequest(vmName, assignment.Args[1])
	if err != nil {
		report.Error = err.Error()
		return report
	}
	runCtx, cancel := context.WithTimeout(ctx, w.assignmentTimeout)
	defer cancel()
	w.reportAssignmentStatus(runCtx, assignment.ID, "running")
	response, err := w.sendControlRequest(runCtx, vmName, request)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			report.Error = fmt.Sprintf("assignment timed out after %s", w.assignmentTimeout)
		} else {
			report.Error = err.Error()
		}
		return report
	}
	report.Stdout = response
	var decoded map[string]any
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		report.Error = fmt.Sprintf("decode control response: %v", err)
		return report
	}
	if success, _ := decoded["success"].(bool); !success {
		report.ExitCode = 1
		if msg, ok := decoded["error"].(string); ok && strings.TrimSpace(msg) != "" {
			report.Error = msg
		} else {
			report.Error = "control request failed"
		}
		return report
	}
	report.Status = "complete"
	report.ExitCode = 0
	report.Error = ""
	return report
}

func (w *FleetWorker) controlRequest(vmName, raw string) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return nil, fmt.Errorf("decode control request: %w", err)
	}
	if request == nil {
		return nil, fmt.Errorf("control request must be a json object")
	}
	if _, ok := request["auth_token"]; !ok {
		token, err := w.controlToken(vmName)
		if err != nil {
			return nil, err
		}
		if token != "" {
			request["auth_token"] = token
		}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode control request: %w", err)
	}
	return append(data, '\n'), nil
}

func (w *FleetWorker) sendControlRequest(ctx context.Context, vmName string, request []byte) (string, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", w.controlSocketPath(vmName))
	if err != nil {
		return "", fmt.Errorf("dial control socket: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(request); err != nil {
		return "", fmt.Errorf("write control request: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && !(err == io.EOF && line != "") {
		return "", fmt.Errorf("read control response: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("control socket returned no response")
	}
	return line, nil
}

func (w *FleetWorker) controlSocketPath(vmName string) string {
	return filepath.Join(w.vmRoot, vmName, "control.sock")
}

func (w *FleetWorker) controlToken(vmName string) (string, error) {
	data, err := os.ReadFile(filepath.Join(w.vmRoot, vmName, "control.token"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read control token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (w *FleetWorker) finishCoveAssignment(ctx context.Context, assignment fleetcontrol.Assignment, report fleetcontrol.WorkerReport) fleetcontrol.WorkerReport {
	if assignment.WarmPoolSlot != "" && warmPoolClaimVMName(assignment.Args) != "" {
		if err := w.stopClaimedWarmPoolVM(ctx, assignment); err != nil {
			if report.Error != "" {
				report.Error += "; "
			}
			report.Status = "failed"
			report.Error += "cleanup claimed warm pool slot: " + err.Error()
		}
	}
	return report
}

func (w *FleetWorker) stopClaimedWarmPoolVM(ctx context.Context, assignment fleetcontrol.Assignment) error {
	vmName := warmPoolClaimVMName(assignment.Args)
	if vmName == "" {
		return fmt.Errorf("claim assignment vm name missing")
	}
	timeout := w.assignmentInterval
	if timeout < time.Second {
		timeout = time.Second
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	stopCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(stopCtx, w.coveBin, "ctl", "-vm", vmName, "stop")
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func warmPoolClaimVMName(args []string) string {
	if len(args) == 0 || args[0] != "shell" {
		return ""
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--":
			return ""
		case "--env":
			i++
			continue
		default:
			return args[i]
		}
	}
	return ""
}

func assignmentNeedsReadyProbe(assignment fleetcontrol.Assignment) bool {
	return assignment.WarmPool != "" || (assignment.SandboxID != "" && assignment.SandboxRole == "run")
}

func (w *FleetWorker) warmPoolReady(ctx context.Context, assignment fleetcontrol.Assignment) bool {
	vmName := fleetcontrol.WarmPoolAssignmentVMName(assignment)
	if assignment.SandboxID != "" {
		vmName = fleetcontrol.SandboxAssignmentVMName(assignment)
	}
	if vmName == "" {
		return false
	}
	timeout := w.assignmentInterval
	if timeout < time.Second {
		timeout = time.Second
	}
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, w.coveBin, "shell", vmName, "--", "/bin/sh", "-c", "true")
	return cmd.Run() == nil
}

func (w *FleetWorker) reportAssignmentStatus(ctx context.Context, assignmentID, status string) {
	_, err := w.ReportStatus(ctx, fleetcontrol.WorkerReport{
		AssignmentID: assignmentID,
		Status:       status,
	})
	if err != nil {
		w.warn("fleet assignment status report", err)
	}
}

func (w *FleetWorker) ReportStatus(ctx context.Context, report fleetcontrol.WorkerReport) (fleetcontrol.HostRecord, error) {
	report.ID = w.id
	var record fleetcontrol.HostRecord
	if err := w.postJSON(ctx, "/v1/workers/"+url.PathEscape(w.id)+"/reports", report, &record); err != nil {
		return fleetcontrol.HostRecord{}, err
	}
	return record, nil
}

func (w *FleetWorker) postHeartbeat(ctx context.Context, path string) (fleetcontrol.HostRecord, error) {
	var record fleetcontrol.HostRecord
	if err := w.postJSON(ctx, path, w.heartbeat(), &record); err != nil {
		return fleetcontrol.HostRecord{}, err
	}
	return record, nil
}

func (w *FleetWorker) postJSON(ctx context.Context, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode fleet request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint(path), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create fleet request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post fleet request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError("post fleet request", resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode fleet response: %w", err)
	}
	return nil
}

func (w *FleetWorker) heartbeat() fleetcontrol.WorkerHeartbeat {
	imageDetails := listImageInventory(w.imageRoot)
	imageRefs := imageRefsFromInventory(imageDetails)
	return fleetcontrol.WorkerHeartbeat{
		ID:           w.id,
		Host:         w.host,
		Version:      w.version,
		Labels:       cloneStringMap(w.labels),
		ImageRefs:    imageRefs,
		ImageDetails: imageDetails,
		Capacity:     w.capacity(len(imageRefs)),
	}
}

func (w *FleetWorker) capacity(images int) fleetcontrol.Capacity {
	return fleetcontrol.Capacity{
		CPUs:   runtime.NumCPU(),
		VMs:    countDirEntries(w.vmRoot),
		MaxVMs: runtime.NumCPU(),
		Images: images,
	}
}

func (w *FleetWorker) endpoint(path string) string {
	u := *w.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (w *FleetWorker) warn(msg string, err error) {
	if err == nil || w.log == nil {
		return
	}
	w.log.Warn(msg, slog.Any("err", err))
}

func responseError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	} else {
		msg = resp.Status + ": " + msg
	}
	return fmt.Errorf("%s: %s", op, msg)
}

func countDirEntries(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var n int
	for _, entry := range entries {
		if entry.IsDir() {
			n++
		}
	}
	return n
}

func listImageRefs(root string) []string {
	return imageRefsFromInventory(listImageInventory(root))
}

func listImageInventory(root string) []fleetcontrol.WorkerImage {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	seen := make(map[string]fleetcontrol.WorkerImage)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Base(path) == "manifest.json" {
			ref, ok := imageRefForManifest(root, path)
			if ok {
				seen[ref] = fleetcontrol.WorkerImage{
					Ref:                  ref,
					SourceManifestDigest: imageSourceManifestDigest(path),
				}
			}
		}
		return nil
	})
	images := make([]fleetcontrol.WorkerImage, 0, len(seen))
	for _, image := range seen {
		images = append(images, image)
	}
	sort.Slice(images, func(i, j int) bool {
		return images[i].Ref < images[j].Ref
	})
	return images
}

func imageRefsFromInventory(images []fleetcontrol.WorkerImage) []string {
	if len(images) == 0 {
		return nil
	}
	refs := make([]string, 0, len(images))
	for _, image := range images {
		if ref := strings.TrimSpace(image.Ref); ref != "" {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func imageSourceManifestDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var manifest struct {
		SourceManifestDigest string `json:"source_manifest_digest"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ""
	}
	return strings.TrimSpace(manifest.SourceManifestDigest)
}

func imageRefForManifest(root, manifest string) (string, bool) {
	dir := filepath.Dir(manifest)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return "", false
	}
	tag := parts[len(parts)-1]
	name := strings.Join(parts[:len(parts)-1], "/")
	if name == "" || tag == "" {
		return "", false
	}
	return name + ":" + tag, true
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func trimReportOutput(s string, limit int64) string {
	if limit <= 0 || int64(len(s)) <= limit {
		return s
	}
	n := int(limit)
	if limit < 20 {
		return s[:n]
	}
	return s[:n] + "\n[truncated]\n"
}
