package main

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	bootpdDefaultsPath          = "/Library/Preferences/SystemConfiguration/com.apple.InternetSharing.default.plist"
	defaultDHCPLeaseTimeSecs    = 86400
	recommendedDHCPLeaseTimeSec = 600
)

var dhcpLeaseTimeRE = regexp.MustCompile(`DHCPLeaseTimeSecs\s*=\s*(\d+)`)

func warnIfDHCPLeaseTimeLong(w io.Writer) {
	warnIfDHCPLeaseTimeLongFrom(w, readBootPDDefaults)
}

func warnIfDHCPLeaseTimeLongForServe(w io.Writer, listenAddr string) {
	if isLocalServeListenAddr(listenAddr) {
		return
	}
	warnIfDHCPLeaseTimeLong(w)
}

func warnIfDHCPLeaseTimeLongFrom(w io.Writer, read func() (string, error)) {
	out, err := read()
	if err != nil {
		return
	}
	secs, ok := parseDHCPLeaseTimeSecs(out)
	if !ok {
		secs = defaultDHCPLeaseTimeSecs
	}
	if secs < defaultDHCPLeaseTimeSecs {
		return
	}
	fmt.Fprintf(w, "cove serve: warning: macOS Internet Sharing DHCP lease time is %ds; this matters when the gateway drives high-throughput VM forks on a shared NAT. To reduce leases to %ds, run:\n  sudo defaults write %s bootpd -dict DHCPLeaseTimeSecs -int %d\nIf /var/db/dhcpd_leases is already full, remove stale leases with:\n  sudo rm /var/db/dhcpd_leases\n", secs, recommendedDHCPLeaseTimeSec, bootpdDefaultsPath, recommendedDHCPLeaseTimeSec)
}

func isLocalServeListenAddr(addr string) bool {
	if addr == "" {
		return true
	}
	if strings.HasPrefix(addr, "unix://") {
		return true
	}
	host := addr
	if strings.HasPrefix(addr, "tcp://") {
		host = strings.TrimPrefix(addr, "tcp://")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func readBootPDDefaults() (string, error) {
	out, err := exec.Command("/usr/bin/defaults", "read", bootpdDefaultsPath, "bootpd").CombinedOutput()
	if err != nil {
		return "", nil
	}
	return string(out), nil
}

func parseDHCPLeaseTimeSecs(s string) (int, bool) {
	m := dhcpLeaseTimeRE.FindStringSubmatch(s)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
