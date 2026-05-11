package controlserver

import (
	"net"
	"testing"
)

func TestHTTPListenersCloseAll(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		n    int
	}{
		{name: "none", n: 0},
		{name: "one", n: 1},
		{name: "two", n: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h HTTPListeners
			var lns []net.Listener
			for i := 0; i < tt.n; i++ {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				lns = append(lns, ln)
				h.Add(ln)
			}

			h.CloseAll()
			h.CloseAll()
			for _, ln := range lns {
				if _, err := ln.Accept(); err == nil {
					t.Fatal("Accept after CloseAll succeeded")
				}
			}
		})
	}
}

func TestHTTPListenersCanReuseAfterCloseAll(t *testing.T) {
	_ = t.TempDir()
	var h HTTPListeners
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.Add(ln1)
	h.CloseAll()

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.Add(ln2)
	h.CloseAll()

	for _, ln := range []net.Listener{ln1, ln2} {
		if _, err := ln.Accept(); err == nil {
			t.Fatal("Accept after CloseAll succeeded")
		}
	}
}
