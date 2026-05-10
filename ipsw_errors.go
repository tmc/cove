package main

import "errors"

// ErrIPSWTooSmall is returned by the IPSW download path when the
// completed download is smaller than ipswMinSize. It usually means a
// truncated transfer or a CDN error page served as the body. Callers
// can branch on this with errors.Is to retry or print a clear restart
// message.
var ErrIPSWTooSmall = errors.New("downloaded IPSW too small")

// ipswMinSize is the smallest plausible Apple Silicon IPSW (1 GB).
// Anything smaller is almost certainly a truncated body.
const ipswMinSize = 1 * 1024 * 1024 * 1024
