package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()

	t.Run("empty", func(t *testing.T) {
		p := filepath.Join(dir, "empty")
		if err := os.WriteFile(p, nil, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := fileSHA256(p)
		if err != nil {
			t.Fatal(err)
		}
		want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("content", func(t *testing.T) {
		p := filepath.Join(dir, "data")
		payload := []byte("hello world\n")
		if err := os.WriteFile(p, payload, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := fileSHA256(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload)
		want := hex.EncodeToString(sum[:])
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := fileSHA256(filepath.Join(dir, "does-not-exist"))
		if err == nil {
			t.Fatal("missing file: got nil error, want error")
		}
	})
}

func TestDataPartitionNotFoundError(t *testing.T) {
	err := dataPartitionNotFoundError("/dev/disk7", "0: GUID_partition_scheme\n  1: EFI")
	if err == nil {
		t.Fatal("got nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/dev/disk7") {
		t.Fatalf("missing device in message: %q", msg)
	}
	if !strings.Contains(msg, "GUID_partition_scheme") {
		t.Fatalf("missing diskutil output in message: %q", msg)
	}
	if !errors.Is(err, ErrDataPartitionNotFound) {
		t.Fatalf("err = %v, want ErrDataPartitionNotFound", err)
	}
}
