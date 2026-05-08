// Package runs reads and renders cove-action run records from the
// metrics event log.
//
// List scans a metrics root for run_complete events and returns
// Summary entries filtered by status and limit. LoadShow assembles a
// Show for a single run prefix; RenderShow writes a human-readable
// view. ExportJSON, ExportGHASummary, and ExportTarGz produce machine
// or CI-friendly artifacts for the same run.
package runs
