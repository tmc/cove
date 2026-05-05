package softreset

import "testing"

func TestParseSSNetworkLine(t *testing.T) {
	got, ok := parseSSNetworkLine(`tcp LISTEN 0 128 127.0.0.1:22 0.0.0.0:* users:(("sshd",pid=1,fd=3))`)
	if !ok {
		t.Fatal("parseSSNetworkLine failed")
	}
	if got.Protocol != "tcp" || got.State != "LISTEN" || got.Local != "127.0.0.1:22" {
		t.Fatalf("socket = %+v", got)
	}
}
