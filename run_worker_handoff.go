package main

import (
	"encoding/json"
	"fmt"
)

const runWorkerHandoffVersion = 1

type runWorkerHandoff struct {
	Version   int                        `json:"version"`
	Command   string                     `json:"command"`
	VM        runWorkerHandoffVM         `json:"vm"`
	FDs       []runWorkerHandoffFD       `json:"fds,omitempty"`
	Bookmarks []runWorkerHandoffBookmark `json:"bookmarks,omitempty"`
}

type runWorkerHandoffVM struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type runWorkerHandoffFD struct {
	Name   string `json:"name"`
	Index  int    `json:"index"`
	Path   string `json:"path,omitempty"`
	Mode   string `json:"mode,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type runWorkerHandoffBookmark struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Path  string `json:"path,omitempty"`
	Bytes []byte `json:"bytes,omitempty"`
}

func encodeRunWorkerHandoff(h runWorkerHandoff) ([]byte, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(h)
	if err != nil {
		return nil, fmt.Errorf("marshal run-worker handoff: %w", err)
	}
	return data, nil
}

func decodeRunWorkerHandoff(data []byte) (runWorkerHandoff, error) {
	var h runWorkerHandoff
	if err := json.Unmarshal(data, &h); err != nil {
		return runWorkerHandoff{}, fmt.Errorf("decode run-worker handoff: %w", err)
	}
	if err := h.validate(); err != nil {
		return runWorkerHandoff{}, err
	}
	return h, nil
}

func (h runWorkerHandoff) validate() error {
	if h.Version != runWorkerHandoffVersion {
		return fmt.Errorf("run-worker handoff version %d, want %d", h.Version, runWorkerHandoffVersion)
	}
	if h.Command == "" {
		return fmt.Errorf("run-worker handoff missing command")
	}
	seen := map[int]bool{}
	for _, fd := range h.FDs {
		if fd.Name == "" {
			return fmt.Errorf("run-worker handoff fd missing name")
		}
		if fd.Index < 0 {
			return fmt.Errorf("run-worker handoff fd %s has negative index", fd.Name)
		}
		if seen[fd.Index] {
			return fmt.Errorf("run-worker handoff fd index %d is duplicated", fd.Index)
		}
		seen[fd.Index] = true
	}
	for _, bookmark := range h.Bookmarks {
		if bookmark.Key == "" {
			return fmt.Errorf("run-worker handoff bookmark missing key")
		}
		if bookmark.Kind == "" {
			return fmt.Errorf("run-worker handoff bookmark %s missing kind", bookmark.Key)
		}
	}
	return nil
}

func (h runWorkerHandoff) fd(name string) (runWorkerHandoffFD, bool) {
	for _, fd := range h.FDs {
		if fd.Name == name {
			return fd, true
		}
	}
	return runWorkerHandoffFD{}, false
}

func (h runWorkerHandoff) bookmark(kind string) (runWorkerHandoffBookmark, bool) {
	for _, bookmark := range h.Bookmarks {
		if bookmark.Kind == kind {
			return bookmark, true
		}
	}
	return runWorkerHandoffBookmark{}, false
}
