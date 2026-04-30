package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type buildScratch struct {
	ID       string
	Dir      string
	PIDPath  string
	DiskPath string
	LogPath  string
	Created  time.Time
}

type buildScratchMeta struct {
	ID               string    `json:"id"`
	PID              int       `json:"pid"`
	PlanDigest       string    `json:"plan_digest,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	KeepIntermediate bool      `json:"keep_intermediate"`
}

func defaultBuildScratchRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "cove-build-scratch")
	}
	return filepath.Join(home, ".vz", "build-scratch")
}

func (e *buildExecutor) createScratch(parentDisk string) (buildScratch, error) {
	if e.scratchRoot == "" {
		e.scratchRoot = defaultBuildScratchRoot()
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.pid == 0 {
		e.pid = os.Getpid()
	}
	created := e.now().UTC()
	id, err := newBuildScratchID(created)
	if err != nil {
		return buildScratch{}, err
	}
	sc := buildScratch{
		ID:       id,
		Dir:      filepath.Join(e.scratchRoot, id),
		PIDPath:  filepath.Join(e.scratchRoot, id, "build.pid"),
		DiskPath: filepath.Join(e.scratchRoot, id, "disk.img"),
		LogPath:  filepath.Join(e.scratchRoot, id, "build.log"),
		Created:  created,
	}
	if err := os.MkdirAll(sc.Dir, 0755); err != nil {
		return buildScratch{}, fmt.Errorf("create build scratch: %w", err)
	}
	if err := os.WriteFile(sc.PIDPath, []byte(strconv.Itoa(e.pid)+"\n"), 0644); err != nil {
		os.RemoveAll(sc.Dir)
		return buildScratch{}, fmt.Errorf("write build scratch pid: %w", err)
	}
	meta := buildScratchMeta{
		ID:               id,
		PID:              e.pid,
		PlanDigest:       digestBuildPlan(e.plan),
		CreatedAt:        created,
		KeepIntermediate: e.opts.KeepIntermediate,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.RemoveAll(sc.Dir)
		return buildScratch{}, fmt.Errorf("marshal build scratch metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(sc.Dir, "build.json"), data, 0644); err != nil {
		os.RemoveAll(sc.Dir)
		return buildScratch{}, fmt.Errorf("write build scratch metadata: %w", err)
	}
	if parentDisk != "" {
		if err := ForkVMDisk(parentDisk, sc.DiskPath); err != nil {
			os.RemoveAll(sc.Dir)
			return buildScratch{}, err
		}
	}
	return sc, nil
}

func (e *buildExecutor) cleanupScratch(sc buildScratch) error {
	if sc.Dir == "" {
		return nil
	}
	if err := os.RemoveAll(sc.Dir); err != nil {
		return fmt.Errorf("remove build scratch %s: %w", sc.Dir, err)
	}
	return nil
}

func gcBuildScratch(root string, isLive func(int) bool) error {
	if root == "" {
		return nil
	}
	if isLive == nil {
		isLive = processLive
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read build scratch: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		pid, ok := readBuildScratchPID(filepath.Join(dir, "build.pid"))
		if !ok {
			continue
		}
		if isLive != nil && isLive(pid) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove stale build scratch %s: %w", dir, err)
		}
	}
	return nil
}

func readBuildScratchPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func processLive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func newBuildScratchID(t time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("build scratch id: %w", err)
	}
	return t.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

func digestBuildPlan(plan buildPlan) string {
	type step struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		LayerDigest string `json:"layer_digest,omitempty"`
		CacheHit    bool   `json:"cache_hit,omitempty"`
	}
	in := struct {
		Name         string   `json:"name"`
		Base         string   `json:"base"`
		ParentDigest string   `json:"parent_digest"`
		Tags         []string `json:"tags,omitempty"`
		Steps        []step   `json:"steps,omitempty"`
	}{
		Name:         plan.Name,
		Base:         plan.Base,
		ParentDigest: plan.ParentDigest,
		Tags:         append([]string(nil), plan.Tags...),
	}
	for _, s := range plan.Steps {
		in.Steps = append(in.Steps, step{Name: s.Name, Key: s.Key, LayerDigest: s.LayerDigest, CacheHit: s.CacheHit})
	}
	data, err := json.Marshal(in)
	if err != nil {
		return ""
	}
	return digestBytes(data)
}
