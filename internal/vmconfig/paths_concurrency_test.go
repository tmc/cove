package vmconfig

import (
	"path/filepath"
	"sync"
	"testing"
)

// TestEnsureDirConcurrentSameName reproduces a production incident where two
// cove invocations racing on the same not-yet-existing VM name caused
// os.MkdirAll to fail with EEXIST for the loser of the race, which EnsureDir
// used to surface as a hard "create VM dir" error. All concurrent callers
// asking for the same VM name should succeed and agree on the resolved path.
func TestEnsureDirConcurrentSameName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const n = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make([]string, 0, n)
		errs    = make([]error, 0, n)
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			got, err := EnsureDir("racer", "")
			mu.Lock()
			results = append(results, got)
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("EnsureDir() goroutine %d error = %v", i, err)
		}
	}

	want := resolvePath(filepath.Join(BaseDir(), "racer.covevm"))
	for i, got := range results {
		if got != want {
			t.Errorf("EnsureDir() goroutine %d = %q, want %q", i, got, want)
		}
	}
}
