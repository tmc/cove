package main

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
	"sync/atomic"
)

var pprofStarted atomic.Bool

func maybeStartPprofServer() {
	addr := strings.TrimSpace(pprofAddr)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("VZMAC_PPROF"))
	}
	if addr == "" || pprofStarted.Load() {
		return
	}
	addr = normalizePprofAddr(addr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: pprof listen %s: %v\n", addr, err)
		return
	}
	if !pprofStarted.CompareAndSwap(false, true) {
		ln.Close()
		return
	}
	fmt.Fprintf(os.Stderr, "pprof listening on http://%s/debug/pprof/\n", ln.Addr())
	go func() {
		if err := http.Serve(ln, http.DefaultServeMux); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "warning: pprof serve: %v\n", err)
		}
	}()
}

func normalizePprofAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr
	}
	return addr
}
