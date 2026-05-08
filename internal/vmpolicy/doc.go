// Package vmpolicy persists per-VM lifecycle stop thresholds.
//
// A Policy records idle timeout, max age, and run-budget limits used
// by lifecycle enforcement. Load and Save read and write policy.json
// in a VM directory; Merge layers a partial override onto a base.
package vmpolicy
