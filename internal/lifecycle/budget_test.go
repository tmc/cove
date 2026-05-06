package lifecycle

import (
	"errors"
	"sync"
	"testing"
)

func TestConsumeRunBudgetConcurrent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const (
		workers = 10
		budget  = 4
	)
	vmDir := t.TempDir()
	results := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ConsumeRunBudget(vmDir, budget)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	var succeeded, exceeded int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrBudgetExceeded):
			exceeded++
		default:
			t.Fatalf("ConsumeRunBudget() error = %v", err)
		}
	}
	if succeeded != budget {
		t.Fatalf("succeeded = %d, want %d", succeeded, budget)
	}
	if exceeded != workers-budget {
		t.Fatalf("exceeded = %d, want %d", exceeded, workers-budget)
	}
	used, err := RunsUsed(vmDir)
	if err != nil {
		t.Fatalf("RunsUsed(): %v", err)
	}
	if used != budget {
		t.Fatalf("RunsUsed() = %d, want %d", used, budget)
	}
}

func TestConsumeRunBudgetNoBudgetDoesNotCreateCounter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	vmDir := t.TempDir()
	used, err := ConsumeRunBudget(vmDir, 0)
	if err != nil {
		t.Fatalf("ConsumeRunBudget() error = %v", err)
	}
	if used != 0 {
		t.Fatalf("ConsumeRunBudget() used = %d, want 0", used)
	}
	used, err = RunsUsed(vmDir)
	if err != nil {
		t.Fatalf("RunsUsed(): %v", err)
	}
	if used != 0 {
		t.Fatalf("RunsUsed() = %d, want 0", used)
	}
}
