package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultAppleLogPredicate = `subsystem BEGINSWITH[c] "com.apple.virtualization" OR senderImagePath CONTAINS "Virtualization.framework" OR process CONTAINS "Virtual Machine Service" OR subsystem BEGINSWITH[c] "com.apple.MobileDevice" OR senderImagePath CONTAINS "MobileDevice.framework"`

// maybeStartAppleLogStream starts `log stream` if -apple-log is set and returns
// a stop function. The stop function is always safe to call.
func maybeStartAppleLogStream() func() {
	if !appleLog {
		return func() {}
	}

	predicate := strings.TrimSpace(appleLogPredicate)
	if predicate == "" {
		predicate = defaultAppleLogPredicate
	}

	args := []string{
		"stream",
		"--style", "compact",
		"--info",
		"--predicate", predicate,
	}
	cmd := exec.Command("log", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("warning: apple log stream disabled: %v\n", err)
		return func() {}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Printf("warning: apple log stream disabled: %v\n", err)
		return func() {}
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("warning: failed to start apple log stream: %v\n", err)
		return func() {}
	}

	fmt.Printf("Apple log stream started (pid=%d)\n", cmd.Process.Pid)
	fmt.Printf("  predicate: %s\n", predicate)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		prefixLines(stdout, "[apple-log]")
	}()
	go func() {
		defer wg.Done()
		prefixLines(stderr, "[apple-log]")
	}()

	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		wg.Wait()
		close(waitDone)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(os.Interrupt)
			}

			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-waitDone
			}
		})
	}
}

func prefixLines(r io.Reader, prefix string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fmt.Printf("%s %s\n", prefix, line)
	}
}
