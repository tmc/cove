package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const DefaultPlacementLeaseTTL = 2 * time.Minute

type PlacementLease struct {
	Host  string    `json:"host"`
	Until time.Time `json:"until"`
}

type placementLeaseFile struct {
	Leases []PlacementLease `json:"leases,omitempty"`
}

func ActivePlacementLeaseCounts(configPath string, now time.Time) (map[string]int, error) {
	counts := make(map[string]int)
	err := withPlacementLeaseLock(configPath, func(path string) error {
		leases, err := readPlacementLeaseFile(path)
		if err != nil {
			return err
		}
		for _, lease := range leases.Leases {
			if lease.Host == "" || !lease.Until.After(now) {
				continue
			}
			counts[lease.Host]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

func RecordPlacementLease(configPath, host string, now time.Time, ttl time.Duration) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("placement lease: host required")
	}
	if ttl <= 0 {
		ttl = DefaultPlacementLeaseTTL
	}
	until := now.Add(ttl)
	return withPlacementLeaseLock(configPath, func(path string) error {
		leases, err := readPlacementLeaseFile(path)
		if err != nil {
			return err
		}
		active := leases.Leases[:0]
		for _, lease := range leases.Leases {
			if lease.Host != "" && lease.Until.After(now) {
				active = append(active, lease)
			}
		}
		active = append(active, PlacementLease{Host: host, Until: until})
		return writePlacementLeaseFile(path, placementLeaseFile{Leases: active})
	})
}

func placementLeasePath(configPath string) string {
	return configPath + ".leases.json"
}

func withPlacementLeaseLock(configPath string, fn func(string) error) error {
	if configPath == "" {
		return errors.New("placement lease: config path required")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return fmt.Errorf("create fleet config dir: %w", err)
	}
	lockPath := configPath + ".leases.lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open placement lease lock: %w", err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock placement leases: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn(placementLeasePath(configPath))
}

func readPlacementLeaseFile(path string) (placementLeaseFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return placementLeaseFile{}, nil
		}
		return placementLeaseFile{}, fmt.Errorf("read placement leases: %w", err)
	}
	var leases placementLeaseFile
	if err := json.Unmarshal(data, &leases); err != nil {
		return placementLeaseFile{}, fmt.Errorf("parse placement leases: %w", err)
	}
	return leases, nil
}

func writePlacementLeaseFile(path string, leases placementLeaseFile) error {
	data, err := json.MarshalIndent(leases, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal placement leases: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write placement leases: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename placement leases: %w", err)
	}
	return nil
}
