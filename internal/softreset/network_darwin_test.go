package softreset

import "testing"

func TestParseLsofNetworkLine(t *testing.T) {
	got, ok := parseLsofNetworkLine("cove 123 tmc 10u IPv4 0x1 0t0 TCP 127.0.0.1:49152->127.0.0.1:80 (ESTABLISHED)")
	if !ok {
		t.Fatal("parseLsofNetworkLine failed")
	}
	if got.Protocol != "tcp" || got.Process != "cove" || got.State != "ESTABLISHED" {
		t.Fatalf("socket = %+v", got)
	}
}
