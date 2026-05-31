package coved

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
			if err := w.PollAssignment(ctx); err != nil {
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
	}
	if _, err := w.ReportStatus(ctx, report); err != nil {
		return err
	}
	if report.Status == "complete" && strings.TrimSpace(assignment.ImageRef) != "" {
		if err := w.Heartbeat(ctx); err != nil {
			w.warn("fleet image refresh heartbeat", err)
		}
	}
	return nil
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
	w.reportRunning(runCtx, assignment.ID)
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
			w.reportRunning(runCtx, assignment.ID)
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
		return report
	}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Status = "complete"
	report.Error = ""
	return report
}

func (w *FleetWorker) reportRunning(ctx context.Context, assignmentID string) {
	_, err := w.ReportStatus(ctx, fleetcontrol.WorkerReport{
		AssignmentID: assignmentID,
		Status:       "running",
	})
	if err != nil {
		w.warn("fleet assignment running report", err)
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
	imageRefs := listImageRefs(w.imageRoot)
	return fleetcontrol.WorkerHeartbeat{
		ID:        w.id,
		Host:      w.host,
		Version:   w.version,
		Labels:    cloneStringMap(w.labels),
		ImageRefs: imageRefs,
		Capacity:  w.capacity(len(imageRefs)),
	}
}

func (w *FleetWorker) capacity(images int) fleetcontrol.Capacity {
	return fleetcontrol.Capacity{
		CPUs:   runtime.NumCPU(),
		VMs:    countDirEntries(w.vmRoot),
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
	if strings.TrimSpace(root) == "" {
		return nil
	}
	seen := make(map[string]bool)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Base(path) == "manifest.json" {
			ref, ok := imageRefForManifest(root, path)
			if ok {
				seen[ref] = true
			}
		}
		return nil
	})
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
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
