// Package softreset runs empirical probes that test whether a host
// soft-reset (without fork/restore) leaves no residue across runs.
//
// Each probe writes a sentinel into a scratch root, applies a reset
// function, and checks for residue in filesystem attributes, memory,
// network state, processes, and extended attributes. A probe returns
// StatusPass when residue is absent, StatusFail when it survives, and
// StatusLimit when the host cannot evaluate the property. Phase C of
// design 015 used these probes to conclude that fork/restore is
// required for isolation.
package softreset
