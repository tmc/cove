package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/vz-macos/internal/filehandle"
)

var (
	fileHandleNetworkMu      sync.Mutex
	fileHandleNetworkSession *filehandle.Session
	fileHandleNetworkCancel  context.CancelFunc
	fileHandleNetworkDone    chan error
)

func prepareFileHandleNetworkDevice() (vz.VZVirtioNetworkDeviceConfiguration, error) {
	session, err := filehandle.NewSession(filehandle.Config{
		PCAPPath: pcapPath,
	})
	if err != nil {
		return vz.VZVirtioNetworkDeviceConfiguration{}, fmt.Errorf("create filehandle network session: %w", err)
	}

	fileHandleNetworkMu.Lock()
	if fileHandleNetworkSession != nil {
		_ = fileHandleNetworkSession.Close()
	}
	fileHandleNetworkSession = session
	fileHandleNetworkCancel = nil
	fileHandleNetworkDone = nil
	fileHandleNetworkMu.Unlock()
	return session.DeviceConfiguration(), nil
}

func startPreparedFileHandleNetwork() {
	fileHandleNetworkMu.Lock()
	session := fileHandleNetworkSession
	if session == nil || fileHandleNetworkCancel != nil {
		fileHandleNetworkMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	fileHandleNetworkCancel = cancel
	fileHandleNetworkDone = done
	fileHandleNetworkMu.Unlock()

	go func() {
		done <- session.Pump(ctx, nil)
	}()
	if verbose {
		fmt.Printf("Filehandle network capture enabled")
		if pcapPath != "" {
			fmt.Printf(" (pcap: %s)", pcapPath)
		}
		fmt.Println()
	}
}

func stopPreparedFileHandleNetwork() {
	fileHandleNetworkMu.Lock()
	session := fileHandleNetworkSession
	cancel := fileHandleNetworkCancel
	done := fileHandleNetworkDone
	fileHandleNetworkSession = nil
	fileHandleNetworkCancel = nil
	fileHandleNetworkDone = nil
	fileHandleNetworkMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && verbose {
			fmt.Printf("warning: filehandle network pump: %v\n", err)
		}
	}
	if session != nil {
		fmt.Println(session.Summary())
		_ = session.Close()
	}
}
