package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIPSWDownloadStallResumes(t *testing.T) {
	data := bytes.Repeat([]byte("restore-image-data"), 32768)
	const prefix = 65536
	var mu sync.Mutex
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		attempt := len(ranges)
		mu.Unlock()
		if attempt == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data[:prefix])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, "Restore.ipsw", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "Restore.ipsw")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := downloadIPSWTransfer(ctx, srv.URL, path, int64(len(data)), nil, ipswDownloadOptions{attempts: 2, stallSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("download differs: got %d bytes, want %d", len(got), len(data))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ranges) != 2 || ranges[0] != "" || ranges[1] != fmt.Sprintf("bytes=%d-", prefix) {
		t.Fatalf("ranges = %q", ranges)
	}
}

func TestIPSWDownloadResponses(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantAttempts int
	}{
		{"not found", http.StatusNotFound, 1},
		{"transient service failure", http.StatusServiceUnavailable, 3},
		{"server ignores range", http.StatusOK, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				attempts++
				mu.Unlock()
				w.WriteHeader(tt.status)
				fmt.Fprint(w, "error or full content")
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "Restore.ipsw")
			prefix := []byte("saved partial download")
			if err := os.WriteFile(path, prefix, 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := downloadIPSWTransfer(ctx, srv.URL, path, 100, nil, ipswDownloadOptions{attempts: 3, stallSeconds: 1})
			if err == nil {
				t.Fatal("download unexpectedly succeeded")
			}
			mu.Lock()
			gotAttempts := attempts
			mu.Unlock()
			if gotAttempts != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d: %v", gotAttempts, tt.wantAttempts, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, prefix) {
				t.Fatalf("partial file changed: %q", got)
			}
		})
	}
}

func TestIPSWDownloadCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 65536))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	path := filepath.Join(t.TempDir(), "Restore.ipsw")
	go func() {
		done <- downloadIPSWTransfer(ctx, srv.URL, path, 1000000, nil, ipswDownloadOptions{attempts: 4, stallSeconds: 30})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	deadline := time.Now().Add(3 * time.Second)
	for downloadFileSize(path) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("body was not saved before cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop download")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("partial file lost: %v", err)
	}
}

func TestIPSWHeadCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int64, 1)
	go func() { done <- getHTTPContentLength(ctx, srv.URL) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case n := <-done:
		if n != 0 {
			t.Fatalf("length = %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HEAD did not stop")
	}
}

func TestDownloadIPSWRejectsLargePartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Restore.ipsw")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = 11 << 30
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	var mu sync.Mutex
	gotRange := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(size+100))
			return
		}
		mu.Lock()
		gotRange = r.Header.Get("Range")
		mu.Unlock()
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	complete := false
	err = downloadIPSW(context.Background(), srv.URL, path, func(_ string, pct float64) {
		if pct == 100 {
			complete = true
		}
	})
	if err == nil || complete {
		t.Fatalf("large partial accepted: err=%v, complete=%v", err, complete)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotRange != fmt.Sprintf("bytes=%d-", size) {
		t.Fatalf("range = %q", gotRange)
	}
	if !strings.Contains(err.Error(), "download") {
		t.Fatalf("error = %v", err)
	}
}
