package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/imagestore"
)

type imageVerifyStatus string

const (
	imageVerifyPass imageVerifyStatus = "PASS"
	imageVerifyWarn imageVerifyStatus = "WARN"
	imageVerifyFail imageVerifyStatus = "FAIL"
	imageVerifyInfo imageVerifyStatus = "INFO"
)

type imageVerifyCheck struct {
	Name   string            `json:"name"`
	Status imageVerifyStatus `json:"status"`
	Detail string            `json:"detail,omitempty"`
}

type imageVerifyReport struct {
	Ref       string               `json:"ref"`
	Verdict   imageVerifyStatus    `json:"verdict"`
	Legacy    bool                 `json:"legacy_manifest,omitempty"`
	ForkCount int                  `json:"fork_count"`
	Checks    []imageVerifyCheck   `json:"checks"`
	Manifest  *imagestore.Manifest `json:"manifest,omitempty"`
}

type imageVerifyOptions struct {
	Strict    bool
	NewerThan time.Duration
	Now       time.Time
}

func VerifyImage(ref imagestore.Ref, opts imageVerifyOptions) imageVerifyReport {
	report := imageVerifyReport{Ref: ref.String(), Verdict: imageVerifyPass}
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		report.addCheck("manifest", imageVerifyFail, err.Error())
		report.Manifest = nil
		report.Verdict = imageVerifyFail
		return report
	}
	report.Manifest = manifest
	report.Legacy = legacyImageManifest(manifest)
	report.addCheck("manifest", imageVerifyPass, "parsed")
	if report.Legacy {
		report.addCheck("legacy manifest", imageVerifyWarn, "missing provenance fields")
	}

	if err := verifyImageLayout(ref, manifest); err != nil {
		report.addCheck("layout", imageVerifyFail, err.Error())
	} else {
		report.addCheck("layout", imageVerifyPass, strings.Join(imageLayoutRequiredFiles(manifest.OSType), ", ")+" present")
	}

	agentStatus, detail := verifyImageAgentFeatures(manifest, opts.Strict)
	report.addCheck("agent features", agentStatus, detail)

	coveStatus, coveDetail := verifyImageCoveCommit(manifest)
	report.addCheck("cove commit", coveStatus, coveDetail)

	if opts.NewerThan > 0 {
		freshStatus, freshDetail := verifyImageFreshness(manifest, opts)
		report.addCheck("freshness", freshStatus, freshDetail)
	}

	if forks, err := VMsForkedFromImage(ref); err != nil {
		report.addCheck("forks", imageVerifyWarn, fmt.Sprintf("fork count unavailable: %v", err))
	} else {
		report.ForkCount = len(forks)
		if len(forks) == 0 {
			report.addCheck("forks", imageVerifyInfo, "0 reachable VMs")
		} else {
			report.addCheck("forks", imageVerifyInfo, fmt.Sprintf("%d reachable VM(s): %s", len(forks), strings.Join(forks, ", ")))
		}
	}

	report.Verdict = report.worstStatus()
	return report
}

func verifyImageLayout(ref imagestore.Ref, manifest *imagestore.Manifest) error {
	required := imageLayoutRequiredFiles(manifest.OSType)
	for _, name := range required {
		path := filepath.Join(ref.Path(), name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("%s missing: %w", name, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", name)
		}
	}
	diskName := imageLayoutDiskFile(manifest.OSType)
	diskInfo, err := os.Stat(filepath.Join(ref.Path(), diskName))
	if err != nil {
		return err
	}
	if manifest.DiskSize > 0 && diskInfo.Size() != manifest.DiskSize {
		return fmt.Errorf("%w: have %d, manifest %d", ErrImageDiskSizeMismatch, diskInfo.Size(), manifest.DiskSize)
	}
	return nil
}

func imageLayoutRequiredFiles(osType string) []string {
	var names []string
	for _, f := range cloneRequiredFiles(osType) {
		if f.required {
			names = append(names, f.name)
		}
	}
	return names
}

func imageLayoutDiskFile(osType string) string {
	switch osType {
	case "Linux":
		return "linux-disk.img"
	case "Windows":
		return "windows-disk.img"
	default:
		return "disk.img"
	}
}

// ErrImageDiskSizeMismatch is returned by verifyImageLayout when the
// on-disk disk.img size does not match the manifest's DiskSize.
// Callers can branch on this with errors.Is to distinguish a corrupt
// image from a missing manifest field or layout problem.
var ErrImageDiskSizeMismatch = errors.New("image disk.img size mismatch")

func verifyImageAgentFeatures(manifest *imagestore.Manifest, strict bool) (imageVerifyStatus, string) {
	features := normalizeAgentFeatures(manifest.AgentFeatures)
	if len(features) == 0 {
		if strict {
			return imageVerifyFail, "missing agent_features; want execattach.v3"
		}
		return imageVerifyWarn, "missing agent_features; want execattach.v3"
	}
	if !featureSliceContains(features, "execattach.v3") {
		if strict {
			return imageVerifyFail, fmt.Sprintf("missing execattach.v3 (have %s)", strings.Join(features, ", "))
		}
		return imageVerifyWarn, fmt.Sprintf("missing execattach.v3 (have %s)", strings.Join(features, ", "))
	}
	return imageVerifyPass, strings.Join(features, ", ")
}

func verifyImageCoveCommit(manifest *imagestore.Manifest) (imageVerifyStatus, string) {
	if strings.TrimSpace(manifest.CoveCommit) == "" {
		return imageVerifyWarn, "missing cove_commit"
	}
	host := hostVersion()
	switch agentstate.CompareVersions(host, manifest.CoveCommit) {
	case agentstate.VersionEqual:
		return imageVerifyPass, fmt.Sprintf("%s matches current %s", manifest.CoveCommit, host)
	case agentstate.VersionGuestOlder:
		return imageVerifyWarn, fmt.Sprintf("%s older than current %s", manifest.CoveCommit, host)
	case agentstate.VersionGuestNewer:
		return imageVerifyFail, fmt.Sprintf("%s newer than current %s", manifest.CoveCommit, host)
	case agentstate.VersionDifferent:
		return imageVerifyWarn, fmt.Sprintf("%s differs from current %s", manifest.CoveCommit, host)
	default:
		return imageVerifyWarn, fmt.Sprintf("cannot compare %s against %s", manifest.CoveCommit, host)
	}
}

func verifyImageFreshness(manifest *imagestore.Manifest, opts imageVerifyOptions) (imageVerifyStatus, string) {
	timestamp := manifest.BuiltAt
	if timestamp.IsZero() {
		timestamp = manifest.CreatedAt
	}
	if timestamp.IsZero() {
		return imageVerifyFail, "manifest has no built_at or createdAt timestamp"
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(timestamp)
	if age < 0 {
		return imageVerifyWarn, fmt.Sprintf("image timestamp %s is in the future", timestamp.UTC().Format(time.RFC3339))
	}
	if age > opts.NewerThan {
		return imageVerifyFail, fmt.Sprintf("image age %s exceeds %s", age.Round(time.Second), opts.NewerThan)
	}
	return imageVerifyPass, fmt.Sprintf("image age %s within %s", age.Round(time.Second), opts.NewerThan)
}

func (r *imageVerifyReport) addCheck(name string, status imageVerifyStatus, detail string) {
	if detail != "" {
		detail = strings.TrimSpace(detail)
	}
	r.Checks = append(r.Checks, imageVerifyCheck{Name: name, Status: status, Detail: detail})
}

func (r *imageVerifyReport) worstStatus() imageVerifyStatus {
	worst := imageVerifyPass
	for _, c := range r.Checks {
		switch c.Status {
		case imageVerifyFail:
			return imageVerifyFail
		case imageVerifyWarn:
			if worst != imageVerifyFail {
				worst = imageVerifyWarn
			}
		}
	}
	return worst
}

func runImageVerify(args []string) error {
	fs := flag.NewFlagSet("image verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	quiet := fs.Bool("quiet", false, "only print on failure")
	strict := fs.Bool("strict", false, "treat missing execattach.v3 as an error")
	newerThan := fs.Duration("newer-than", 0, "require image built or created within duration")
	fs.Usage = func() { printImageVerifyUsage(fs.Output()) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{"json": false})); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image verify requires <name[:tag]>")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	report := VerifyImage(ref, imageVerifyOptions{Strict: *strict, NewerThan: *newerThan})
	if *asJSON {
		if err := writeImageVerifyJSON(os.Stdout, report); err != nil {
			return err
		}
	} else if !*quiet || report.Verdict == imageVerifyFail {
		writeImageVerifyText(os.Stdout, report)
	}
	if report.Verdict == imageVerifyFail {
		return fmt.Errorf("image verify: %s", ref)
	}
	return nil
}

func printImageVerifyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image verify <name[:tag]> [-strict] [-json] [-quiet] [-newer-than <duration>]

Verify a local image ref and report manifest, layout, agent, provenance, freshness,
and downstream fork checks. The -json flag may appear before or after the image
ref.

Flags:
  -json                  emit machine-readable JSON
  -newer-than duration   require image built or created within duration
  -quiet                 only print on failure
  -strict                treat missing execattach.v3 as an error`)
}

func writeImageVerifyJSON(w io.Writer, report imageVerifyReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verify output: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeImageVerifyText(w io.Writer, report imageVerifyReport) {
	fmt.Fprintf(w, "Image %s\n", report.Ref)
	for _, check := range report.Checks {
		if check.Detail != "" {
			fmt.Fprintf(w, "  %-15s %s: %s\n", strings.ToLower(string(check.Status)), check.Name, check.Detail)
			continue
		}
		fmt.Fprintf(w, "  %-15s %s\n", strings.ToLower(string(check.Status)), check.Name)
	}
	fmt.Fprintf(w, "  summary:        %s\n", report.Verdict)
}

func featureSliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
