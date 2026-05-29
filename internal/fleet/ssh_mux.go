package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// MuxControlPersist is the ControlPersist window passed to ssh. The master
// connection lingers this long after the last client exits so that the next
// fleet command to the same host reuses the established TCP/SSH session.
const MuxControlPersist = "60s"

// muxDisableEnv opts out of SSH connection multiplexing when set to any
// non-empty value.
const muxDisableEnv = "COVE_FLEET_NO_MUX"

// MuxEnabled reports whether SSH ControlMaster multiplexing should be applied
// to fleet ssh invocations. Multiplexing is on by default and disabled when
// COVE_FLEET_NO_MUX is set to any non-empty value.
func MuxEnabled() bool {
	return os.Getenv(muxDisableEnv) == ""
}

// SSHMuxOptions returns the ssh -o options that enable connection
// multiplexing for the given remote. Repeated fleet commands to the same
// (user, host) share one master connection via a stable ControlPath. When
// multiplexing is disabled (see MuxEnabled) it returns nil so callers append
// nothing.
func SSHMuxOptions(remote Remote) []string {
	if !MuxEnabled() {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=" + MuxControlPersist,
		"-o", "ControlPath=" + MuxControlPath(remote),
	}
}

// MuxControlPath returns the filesystem path of the ssh control socket for the
// given remote. The path is stable for a given (user, host) pair and distinct
// across different pairs, so multiple invocations multiplex onto one master
// connection while separate hosts stay isolated.
func MuxControlPath(remote Remote) string {
	return filepath.Join(muxDir(), "mux-"+muxKey(remote)+".sock")
}

// muxKey derives a short, filesystem-safe identifier for a remote. The full
// target is hashed to keep the resulting ControlPath well under the unix
// socket path length limit regardless of host or user length.
func muxKey(remote Remote) string {
	sum := sha256.Sum256([]byte(remoteTarget(remote)))
	return hex.EncodeToString(sum[:8])
}

// muxDir is the directory holding ssh control sockets. It honors HOME via
// os.UserHomeDir and falls back to the OS temp dir when no home is available.
func muxDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".vz", "fleet-ssh")
	}
	return filepath.Join(os.TempDir(), "cove-fleet-ssh")
}

// EnsureMuxDir creates the multiplexing socket directory with private
// permissions. It is best-effort: ssh will surface a clear error if the
// directory is missing, so callers may ignore the returned error when they
// only want to nudge the directory into existence.
func EnsureMuxDir() error {
	return os.MkdirAll(muxDir(), 0700)
}
