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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tmc/cove/internal/fleetcontrol"
)

const (
	DefaultFleetHeartbeatInterval  = 10 * time.Second
	DefaultFleetAssignmentInterval = 5 * time.Second
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
	HeartbeatInterval  time.Duration
	AssignmentInterval time.Duration
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
	heartbeatInterval  time.Duration
	assignmentInterval time.Duration
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
		heartbeatInterval:  heartbeatInterval,
		assignmentInterval: assignmentInterval,
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
	status := "unsupported"
	errText := fmt.Sprintf("unsupported assignment verb %q", assignment.Verb)
	if strings.TrimSpace(assignment.Verb) == "noop" {
		status = "complete"
		errText = ""
	}
	_, err := w.ReportStatus(ctx, fleetcontrol.WorkerReport{
		AssignmentID: assignment.ID,
		Status:       status,
		Error:        errText,
	})
	return err
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
	return fleetcontrol.WorkerHeartbeat{
		ID:       w.id,
		Host:     w.host,
		Version:  w.version,
		Labels:   cloneStringMap(w.labels),
		Capacity: w.capacity(),
	}
}

func (w *FleetWorker) capacity() fleetcontrol.Capacity {
	return fleetcontrol.Capacity{
		CPUs:   runtime.NumCPU(),
		VMs:    countDirEntries(w.vmRoot),
		Images: countImageManifests(w.imageRoot),
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

func countImageManifests(root string) int {
	var n int
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Base(path) == "manifest.json" {
			n++
		}
		return nil
	})
	return n
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
