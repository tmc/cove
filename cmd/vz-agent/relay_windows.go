package main

import (
	"context"
	"errors"
)

type TCPRelay struct{}

func StartTCPRelay(context.Context, uint32, string) (*TCPRelay, error) {
	return nil, errors.New("vsock tcp relay unsupported on windows")
}

func (r *TCPRelay) Close() error {
	return nil
}

func startITerm2Relay(context.Context) {}
