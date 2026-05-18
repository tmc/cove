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

type UDPRelay struct{}

func StartUDPRelay(context.Context, uint32, string) (*UDPRelay, error) {
	return nil, errors.New("vsock udp relay unsupported on windows")
}

func (r *UDPRelay) Close() error {
	return nil
}

type ReverseTCPRelay struct{}

func StartReverseTCPRelay(context.Context, int, uint32) (*ReverseTCPRelay, error) {
	return nil, errors.New("reverse tcp relay unsupported on windows")
}

func (r *ReverseTCPRelay) Close() error {
	return nil
}

type ReverseUDPRelay struct{}

func StartReverseUDPRelay(context.Context, int, uint32) (*ReverseUDPRelay, error) {
	return nil, errors.New("reverse udp relay unsupported on windows")
}

func (r *ReverseUDPRelay) Close() error {
	return nil
}

func startITerm2Relay(context.Context) {}
