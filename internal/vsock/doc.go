// Package vsock documents cove's host-guest virtio socket conventions.
//
// cove uses vsock for control traffic between the host process that owns the
// virtual machine and agents inside the guest. The root daemon agent listens on
// port 1024. The per-user agent listens on port 1025 and is the default route
// for work that needs the logged-in user's session, home directory, or TCC/FDA
// grants. Callers should use the daemon port only for explicit root work.
//
// Vsock is a byte stream, not a message transport. Protocols layered on it must
// define their own framing and keep one reader responsible for each connection.
// Control socket clients use length-prefixed protobuf messages. Attach and
// shell paths use JSON-line frames and keep each exec session's frame stream
// independent so concurrent sessions cannot consume each other's input.
package vsock
