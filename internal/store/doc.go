// Package store is cove's content-addressed blob store for OCI image
// layers and manifests.
//
// Blobs are written under Dir/blobs/sha256/<digest> and verified by
// digest and size on read. GC reclaims blobs unreachable from any VM
// or build-cache reference and older than GCGrace. Shared and
// exclusive file locks coordinate concurrent readers and GC.
package store
