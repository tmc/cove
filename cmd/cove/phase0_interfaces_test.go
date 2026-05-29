package main

import "testing"

func TestNewControlServerGuestConnectorNilReturnsZeroValue(t *testing.T) {
	got := newControlServerGuestConnector(nil)
	if got.vm.ID != 0 || got.queue.Handle() != 0 {
		t.Fatalf("got = %+v, want zero connector for nil server", got)
	}
}
