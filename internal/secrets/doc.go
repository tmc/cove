// Package secrets resolves secret URI references for cove configuration.
//
// It supports environment-variable and file-backed providers and returns copied
// byte slices so callers can handle secret material without aliasing provider state.
package secrets
