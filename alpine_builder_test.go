package main

import (
	"strings"
	"testing"
)

func TestAlpineBuilderScript(t *testing.T) {
	script := alpineBuilderScript("/mnt/src/cove/.tmp/alpine-builder", "https://example.test/alpine.tar.gz", true)
	for _, want := range []string{
		"apk add openrc e2fsprogs e2fsprogs-extra dhcpcd openssh ca-certificates",
		"missing required tool: $tool",
		"before hwdrivers machine-id",
		"'dev sysinit'",
		"cp -a /lib/modules/$(uname -r)",
		"modprobe vsock",
		"command=\"/usr/local/bin/vz-agent\"",
		"touch \"$ROOT/var/lib/cove-setup.done\"",
		"gzip -dc /boot/vmlinuz-$(uname -r) > \"$WORK/vmlinuz\"",
		"printf '/dev/vda1\\n' > \"$WORK/linux-root-device.txt\"",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestAlpineBuilderScriptWithoutAgent(t *testing.T) {
	script := alpineBuilderScript("/mnt/src/cove/.tmp/alpine-builder", "https://example.test/alpine.tar.gz", false)
	if strings.Contains(script, "cp \"$WORK/vz-agent\"") {
		t.Fatalf("script installs agent when installAgent=false:\n%s", script)
	}
}

func TestShellQuote(t *testing.T) {
	if got, want := alpineShellQuote("/tmp/a'b"), `'/tmp/a'\''b'`; got != want {
		t.Fatalf("alpineShellQuote = %q, want %q", got, want)
	}
}
