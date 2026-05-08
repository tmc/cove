// Package action implements the cove action subcommands that prepare
// hosts and images for cove-action runs.
//
// RunDoctor evaluates host preflight checks (macOS version, disk
// headroom, virtualization entitlement, sandbox state) and returns a
// Report whose checks are pass, warn, or fail. RunPrepare validates
// that an image reference can serve as a cove-action runner and
// records the result. DoctorMain, RunDoctorCommand, and
// RunPrepareCommand are the CLI entry points; WriteReport renders a
// Report as text or JSON.
package action
