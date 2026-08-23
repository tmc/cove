package vmconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSaveConcurrentNeverCorrupts reproduces the shared-temp-file race:
// Save used to stage every write at the fixed path config.json.tmp, so
// two concurrent writers could interleave write and rename and publish a
// half-written file. Every observed config.json must parse.
func TestSaveConcurrentNeverCorrupts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	const writers = 8
	const rounds = 40
	// A long slice makes a partially written file easy to detect.
	volumes := make([]VolumeMount, 64)
	for i := range volumes {
		volumes[i].Tag = strings.Repeat("x", 64)
	}

	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				t.Errorf("read config.json: %v", err)
				return
			}
			var cfg Config
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Errorf("config.json is not parsable: %v\n%s", err, data)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				cfg := &Config{CPU: uint(i + 1), MemoryGB: uint64(r + 1), Volumes: volumes}
				if err := Save(dir, cfg); err != nil {
					t.Errorf("Save() error = %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	readerWG.Wait()

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load() after concurrent saves error = %v", err)
	}
}

// TestSaveLeavesNoTempFiles checks that the unique temp files Save stages
// through are always renamed or cleaned up.
func TestSaveLeavesNoTempFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	for i := 0; i < 20; i++ {
		if err := Save(dir, &Config{CPU: uint(i)}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config-") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %q", e.Name())
		}
	}
}

// TestUpdateConcurrentNoLostUpdates runs one goroutine per config field
// and asserts that every update survives. Without a lock held across
// load-mutate-save, the setters silently clobber one another.
func TestUpdateConcurrentNoLostUpdates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	mutators := []struct {
		name   string
		mutate func(*Config)
		check  func(*Config) bool
	}{
		{"cpu", func(c *Config) { c.CPU = 7 }, func(c *Config) bool { return c.CPU == 7 }},
		{"memory", func(c *Config) { c.MemoryGB = 11 }, func(c *Config) bool { return c.MemoryGB == 11 }},
		{"uid", func(c *Config) { c.GuestUserUID = 501 }, func(c *Config) bool { return c.GuestUserUID == 501 }},
		{"gid", func(c *Config) { c.GuestUserGID = 20 }, func(c *Config) bool { return c.GuestUserGID == 20 }},
		{"recipes", func(c *Config) { c.PostInstallRecipes = "homebrew" }, func(c *Config) bool { return c.PostInstallRecipes == "homebrew" }},
		{"parentVM", func(c *Config) { c.ParentVM = "base" }, func(c *Config) bool { return c.ParentVM == "base" }},
		{"parentSnapshot", func(c *Config) { c.ParentSnapshot = "snap" }, func(c *Config) bool { return c.ParentSnapshot == "snap" }},
		{"parentImage", func(c *Config) { c.ParentImage = "img:v1" }, func(c *Config) bool { return c.ParentImage == "img:v1" }},
		{"volumes", func(c *Config) { c.Volumes = []VolumeMount{{Tag: "share"}} }, func(c *Config) bool { return len(c.Volumes) == 1 }},
		{"agent", func(c *Config) { c.Agent = &AgentConfig{Verified: true} }, func(c *Config) bool { return c.Agent != nil && c.Agent.Verified }},
	}

	var wg sync.WaitGroup
	wg.Add(len(mutators))
	for _, m := range mutators {
		go func(mutate func(*Config)) {
			defer wg.Done()
			if _, err := Update(dir, func(cfg *Config) (bool, error) {
				mutate(cfg)
				return true, nil
			}); err != nil {
				t.Errorf("Update() error = %v", err)
			}
		}(m.mutate)
	}
	wg.Wait()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, m := range mutators {
		if !m.check(cfg) {
			t.Errorf("update %q was lost; config = %+v", m.name, cfg)
		}
	}
}

// TestSettersConcurrentNoLostUpdates exercises the exported setters,
// which must serialize their read-modify-write against each other.
func TestSettersConcurrentNoLostUpdates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		if _, err := SetHardware(dir, Hardware{CPU: 4, MemoryGB: 8}); err != nil {
			t.Errorf("SetHardware() error = %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := SetGuestUser(dir, 501, 20); err != nil {
			t.Errorf("SetGuestUser() error = %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := SetPostInstallRecipes(dir, "golang"); err != nil {
			t.Errorf("SetPostInstallRecipes() error = %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := SetVolumes(dir, []VolumeMount{{Tag: "share"}}); err != nil {
			t.Errorf("SetVolumes() error = %v", err)
		}
	}()
	wg.Wait()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CPU != 4 || cfg.MemoryGB != 8 {
		t.Errorf("SetHardware lost: CPU = %d, MemoryGB = %d", cfg.CPU, cfg.MemoryGB)
	}
	if cfg.GuestUserUID != 501 || cfg.GuestUserGID != 20 {
		t.Errorf("SetGuestUser lost: uid = %d, gid = %d", cfg.GuestUserUID, cfg.GuestUserGID)
	}
	if cfg.PostInstallRecipes != "golang" {
		t.Errorf("SetPostInstallRecipes lost: got %q", cfg.PostInstallRecipes)
	}
	if len(cfg.Volumes) != 1 {
		t.Errorf("SetVolumes lost: got %v", cfg.Volumes)
	}
}

// TestConfigLockIsAdvisory checks that a lock another holder never
// releases cannot wedge a writer forever: withConfigLock times out and
// runs the update anyway.
func TestConfigLockIsAdvisory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()

	// Shorten the wait so the test does not sit out the full timeout.
	saved := configLockTimeout
	configLockTimeout = 50 * time.Millisecond
	defer func() { configLockTimeout = saved }()

	release, err := acquireConfigLock(dir)
	if err != nil {
		t.Skipf("config lock unsupported: %v", err)
	}
	defer release()

	// withConfigLock must fall through to the update on timeout.
	done := make(chan error, 1)
	go func() { done <- Save(dir, &Config{CPU: 3}) }()
	if err := <-done; err != nil {
		t.Fatalf("Save() while lock held error = %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CPU != 3 {
		t.Errorf("CPU = %d, want 3", cfg.CPU)
	}
}
