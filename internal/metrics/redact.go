// Package metrics: redact.go - secret-value masker for run logs.
//
// A Masker holds a set of secret byte values registered before any
// guest output is captured. Apply rewrites occurrences of any registered
// value to "***". The masker has no state beyond its value set; multiple
// goroutines may call Apply concurrently after registration completes.
//
// The intent mirrors GitHub Actions' add-mask: keys may stay, values get
// scrubbed. Empty and one-byte values are ignored to avoid pathological
// rewrites of the surrounding stream.
package metrics

import (
	"bytes"
	"sort"
	"sync"
)

// Mask is the replacement token written in place of each registered value.
const Mask = "***"

// Masker scrubs registered secret values from byte streams.
type Masker struct {
	mu     sync.RWMutex
	values [][]byte
}

// NewMasker returns an empty Masker.
func NewMasker() *Masker { return &Masker{} }

// Add registers a secret value. Empty or single-byte values are ignored.
// Add may be called concurrently with Apply but registrations made after
// Apply has started observing a stream will not retroactively scrub
// already-emitted bytes.
func (m *Masker) Add(value []byte) {
	if m == nil || len(value) < 2 {
		return
	}
	v := append([]byte(nil), value...)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.values {
		if bytes.Equal(existing, v) {
			return
		}
	}
	m.values = append(m.values, v)
	// Longest first so a longer secret containing a shorter one masks
	// the longer match before its substring.
	sort.Slice(m.values, func(i, j int) bool {
		return len(m.values[i]) > len(m.values[j])
	})
}

// AddString is Add for string values.
func (m *Masker) AddString(value string) { m.Add([]byte(value)) }

// Apply returns p with every registered value replaced by Mask.
// The returned slice is always a fresh copy when any replacement occurs;
// when no value matches Apply returns p unchanged.
func (m *Masker) Apply(p []byte) []byte {
	if m == nil {
		return p
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.values) == 0 {
		return p
	}
	out := p
	copied := false
	for _, v := range m.values {
		if !bytes.Contains(out, v) {
			continue
		}
		if !copied {
			out = append([]byte(nil), out...)
			copied = true
		}
		out = bytes.ReplaceAll(out, v, []byte(Mask))
	}
	return out
}

// ApplyString is Apply for strings.
func (m *Masker) ApplyString(s string) string {
	if m == nil {
		return s
	}
	return string(m.Apply([]byte(s)))
}
