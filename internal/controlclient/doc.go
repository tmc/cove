// Package controlclient is a programmatic client for the cove VM
// control socket.
//
// New returns a Client bound to a Unix socket path; the client loads
// the per-VM auth token from control.token and sends length-prefixed
// protobuf requests. FormatDialError and RunHintForSocket produce
// user-facing diagnostics when the socket is missing or the VM is not
// running. KeyCode constants name the host-side virtual key codes used
// by input requests.
package controlclient
