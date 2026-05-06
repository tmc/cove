package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const counterFileName = "runs.counter"

var ErrBudgetExceeded = errors.New("run budget exceeded")

func CounterPath(vmDir string) string {
	return filepath.Join(vmDir, counterFileName)
}

func RunsUsed(vmDir string) (int, error) {
	data, err := os.ReadFile(CounterPath(vmDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read run counter: %w", err)
	}
	return parseCounter(data)
}

func ConsumeRunBudget(vmDir string, budget int) (int, error) {
	if budget <= 0 {
		return 0, nil
	}
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return 0, fmt.Errorf("create vm dir: %w", err)
	}
	f, err := os.OpenFile(CounterPath(vmDir), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return 0, fmt.Errorf("open run counter: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, fmt.Errorf("lock run counter: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := os.ReadFile(f.Name())
	if err != nil {
		return 0, fmt.Errorf("read run counter: %w", err)
	}
	used, err := parseCounter(data)
	if err != nil {
		return 0, err
	}
	next := used + 1
	if next > budget {
		return used, ErrBudgetExceeded
	}
	if err := f.Truncate(0); err != nil {
		return 0, fmt.Errorf("truncate run counter: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("seek run counter: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", next); err != nil {
		return 0, fmt.Errorf("write run counter: %w", err)
	}
	return next, nil
}

func parseCounter(data []byte) (int, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("parse run counter: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("parse run counter: negative count")
	}
	return n, nil
}
