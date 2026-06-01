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

	"github.com/tmc/cove/internal/ociimage"
)

type imageBundleVerifyReport struct {
	Path             string             `json:"path"`
	Source           string             `json:"source,omitempty"`
	Ref              string             `json:"ref,omitempty"`
	VM               string             `json:"vm,omitempty"`
	Target           string             `json:"target,omitempty"`
	Verdict          imageVerifyStatus  `json:"verdict"`
	IndexDigest      string             `json:"index_digest,omitempty"`
	IndexFileDigest  string             `json:"index_file_digest,omitempty"`
	ManifestDigest   string             `json:"manifest_digest,omitempty"`
	SelectedDigest   string             `json:"selected_digest,omitempty"`
	SelectedPlatform string             `json:"selected_platform,omitempty"`
	Format           string             `json:"format,omitempty"`
	DiskFormat       string             `json:"disk_format,omitempty"`
	DiskSize         int64              `json:"disk_size,omitempty"`
	ChildCount       int                `json:"child_count"`
	Checks           []imageVerifyCheck `json:"checks"`
}

func runImageBundle(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printImageBundleUsage(env.Stdout)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "verify":
		return runImageBundleVerify(env, rest)
	default:
		printImageBundleUsage(env.Stderr)
		return fmt.Errorf("image bundle: unknown subcommand %q", sub)
	}
}

func runImageBundleVerify(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image bundle verify", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	fs.Usage = func() { printImageBundleVerifyUsage(fs.Output()) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{"json": false})); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image bundle verify requires <dir>")
	}
	report := VerifyImageBundle(fs.Arg(0))
	if *asJSON {
		if err := writeImageBundleVerifyJSON(env.Stdout, report); err != nil {
			return err
		}
	} else {
		writeImageBundleVerifyText(env.Stdout, report)
	}
	if report.Verdict == imageVerifyFail {
		return fmt.Errorf("image bundle verify: %s", fs.Arg(0))
	}
	return nil
}

func VerifyImageBundle(path string) imageBundleVerifyReport {
	report := imageBundleVerifyReport{Path: strings.TrimSpace(path)}
	if report.Path == "" {
		report.addCheck("path", imageVerifyFail, "empty bundle path")
		report.finish()
		return report
	}
	summary, err := readManifestBundleSummaryFile(report.Path)
	if err != nil {
		report.addCheck("summary", imageVerifyFail, err.Error())
		report.finish()
		return report
	}
	report.Source = summary.Source
	report.Ref = summary.Ref
	report.VM = summary.VM
	report.Target = summary.Target
	report.IndexDigest = summary.IndexDigest
	report.IndexFileDigest = summary.IndexFileDigest
	report.ManifestDigest = summary.ManifestDigest
	report.SelectedDigest = summary.SelectedDigest
	report.SelectedPlatform = summary.SelectedPlatform
	report.Format = summary.Format
	report.DiskFormat = summary.DiskFormat
	report.DiskSize = summary.DiskSize
	report.ChildCount = summary.ChildCount

	verifyManifestBundleSummary(report.Path, summary, &report)
	report.finish()
	return report
}

func readManifestBundleSummaryFile(dir string) (manifestBundleSummary, error) {
	var summary manifestBundleSummary
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		return summary, fmt.Errorf("read summary.json: %w", err)
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return summary, fmt.Errorf("parse summary.json: %w", err)
	}
	return summary, nil
}

func verifyManifestBundleSummary(dir string, summary manifestBundleSummary, report *imageBundleVerifyReport) {
	if summary.SchemaVersion != 1 {
		report.addCheck("summary schema", imageVerifyFail, fmt.Sprintf("schema_version %d, want 1", summary.SchemaVersion))
	} else {
		report.addCheck("summary schema", imageVerifyPass, "schema_version=1")
	}
	if summary.ChildCount != len(summary.Children) {
		report.addCheck("child count", imageVerifyFail, fmt.Sprintf("summary child_count %d, entries %d", summary.ChildCount, len(summary.Children)))
	} else {
		report.addCheck("child count", imageVerifyPass, fmt.Sprintf("%d children", summary.ChildCount))
	}

	indexData, ok := verifyManifestBundleFile(dir, "index", "index.json", summary.IndexPath, summary.IndexFileDigest, report)
	index, indexOK := verifyManifestBundleIndex(indexData, ok, report)
	selectedData, selectedOK := verifyManifestBundleFile(dir, "selected", "selected.json", summary.SelectedPath, summary.SelectedFileDigest, report)
	if selectedOK {
		verifyManifestBundleDigestClaim("selected digest", summary.SelectedDigest, digestData(selectedData), report)
		verifyManifestBundleDigestClaim("manifest digest", summary.ManifestDigest, digestData(selectedData), report)
	}
	if ok {
		verifyManifestBundleDigestClaim("index digest", summary.IndexDigest, digestData(indexData), report)
	}
	if !indexOK {
		return
	}
	verifyManifestBundleChildren(dir, summary, index, selectedData, selectedOK, report)
}

func verifyManifestBundleFile(dir, label, wantRel, gotRel, wantDigest string, report *imageBundleVerifyReport) ([]byte, bool) {
	rel := strings.TrimSpace(gotRel)
	if rel == "" {
		rel = wantRel
	}
	if rel != wantRel {
		report.addCheck(label+" path", imageVerifyFail, fmt.Sprintf("%q, want %q", rel, wantRel))
		return nil, false
	}
	clean, err := manifestBundleCleanRelPath(rel)
	if err != nil {
		report.addCheck(label+" path", imageVerifyFail, err.Error())
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(clean)))
	if err != nil {
		report.addCheck(label+" file", imageVerifyFail, fmt.Sprintf("read %s: %v", rel, err))
		return nil, false
	}
	gotDigest := digestData(data)
	if strings.TrimSpace(wantDigest) == "" {
		report.addCheck(label+" digest", imageVerifyFail, "summary missing file digest")
		return data, false
	}
	if gotDigest != wantDigest {
		report.addCheck(label+" digest", imageVerifyFail, fmt.Sprintf("%s digest %s, want %s", rel, gotDigest, wantDigest))
		return data, false
	}
	report.addCheck(label+" digest", imageVerifyPass, fmt.Sprintf("%s %s", rel, gotDigest))
	return data, true
}

func verifyManifestBundleIndex(data []byte, ok bool, report *imageBundleVerifyReport) (ociimage.Index, bool) {
	var index ociimage.Index
	if !ok {
		return index, false
	}
	if err := json.Unmarshal(data, &index); err != nil {
		report.addCheck("index parse", imageVerifyFail, err.Error())
		return index, false
	}
	if index.SchemaVersion != 2 {
		report.addCheck("index parse", imageVerifyFail, fmt.Sprintf("schemaVersion %d, want 2", index.SchemaVersion))
		return index, false
	}
	if index.MediaType != ociimage.MediaTypeImageIndex && index.MediaType != ociimage.MediaTypeDockerList {
		report.addCheck("index parse", imageVerifyFail, fmt.Sprintf("mediaType %q is not an OCI index or Docker manifest list", index.MediaType))
		return index, false
	}
	report.addCheck("index parse", imageVerifyPass, fmt.Sprintf("%s manifests=%d", index.MediaType, len(index.Manifests)))
	return index, true
}

func verifyManifestBundleDigestClaim(name, claim, got string, report *imageBundleVerifyReport) {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return
	}
	if !validSHA256Digest(claim) {
		report.addCheck(name, imageVerifyWarn, fmt.Sprintf("non-canonical registry digest %q", claim))
		return
	}
	if claim != got {
		report.addCheck(name, imageVerifyFail, fmt.Sprintf("%s, file digest %s", claim, got))
		return
	}
	report.addCheck(name, imageVerifyPass, claim)
}

func verifyManifestBundleChildren(dir string, summary manifestBundleSummary, index ociimage.Index, selectedData []byte, selectedOK bool, report *imageBundleVerifyReport) {
	children := map[string]manifestBundleChildSummary{}
	for _, child := range summary.Children {
		if child.Digest == "" {
			report.addCheck("child summary", imageVerifyFail, "child missing digest")
			continue
		}
		if _, exists := children[child.Digest]; exists {
			report.addCheck("child summary", imageVerifyFail, fmt.Sprintf("duplicate child %s", child.Digest))
			continue
		}
		children[child.Digest] = child
	}
	if len(index.Manifests) != len(summary.Children) {
		report.addCheck("index coverage", imageVerifyFail, fmt.Sprintf("index manifests %d, summary children %d", len(index.Manifests), len(summary.Children)))
	} else {
		report.addCheck("index coverage", imageVerifyPass, fmt.Sprintf("%d manifests covered", len(index.Manifests)))
	}

	selectedDigest := strings.TrimSpace(summary.SelectedDigest)
	selectedMatches := 0
	var selectedSummary *manifestBundleChildSummary
	for _, desc := range index.Manifests {
		child, exists := children[desc.Digest]
		if !exists {
			report.addCheck("index coverage", imageVerifyFail, fmt.Sprintf("missing child summary for %s", desc.Digest))
			continue
		}
		verifyManifestBundleChild(dir, child, desc, selectedDigest, report)
		if child.Selected {
			selectedMatches++
		}
		if selectedDigest != "" && desc.Digest == selectedDigest {
			c := child
			selectedSummary = &c
		}
		if selectedDigest != "" && desc.Digest == selectedDigest && selectedOK {
			childData, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifestBundleChildPath(desc.Digest))))
			if err != nil {
				report.addCheck("selected child", imageVerifyFail, fmt.Sprintf("read selected child %s: %v", desc.Digest, err))
			} else if digestData(childData) != digestData(selectedData) {
				report.addCheck("selected child", imageVerifyFail, fmt.Sprintf("selected.json differs from %s", manifestBundleChildPath(desc.Digest)))
			} else {
				report.addCheck("selected child", imageVerifyPass, desc.Digest)
			}
		}
	}
	for digest := range children {
		if !manifestBundleIndexHasDigest(index, digest) {
			report.addCheck("index coverage", imageVerifyFail, fmt.Sprintf("summary child %s not present in index", digest))
		}
	}
	switch {
	case selectedDigest == "":
		report.addCheck("selected child", imageVerifyFail, "summary missing selected_digest")
	case selectedMatches != 1:
		report.addCheck("selected child", imageVerifyFail, fmt.Sprintf("selected children %d, want 1", selectedMatches))
	}
	if selectedSummary != nil {
		verifyManifestBundleSelectedSummary(summary, *selectedSummary, report)
	}
}

func verifyManifestBundleChild(dir string, child manifestBundleChildSummary, desc ociimage.IndexDescriptor, selectedDigest string, report *imageBundleVerifyReport) {
	expectedPath := manifestBundleChildPath(desc.Digest)
	data, ok := verifyManifestBundleFile(dir, "child "+desc.Digest, expectedPath, child.Path, child.FileDigest, report)
	if !ok {
		return
	}
	gotDigest := digestData(data)
	if desc.Digest != gotDigest {
		report.addCheck("child descriptor", imageVerifyFail, fmt.Sprintf("%s file digest %s", desc.Digest, gotDigest))
	}
	if desc.Size != int64(len(data)) {
		report.addCheck("child descriptor", imageVerifyFail, fmt.Sprintf("%s size %d, file bytes %d", desc.Digest, desc.Size, len(data)))
	}
	if desc.MediaType != "" && child.MediaType != "" && desc.MediaType != child.MediaType {
		report.addCheck("child descriptor", imageVerifyFail, fmt.Sprintf("%s media %q, summary %q", desc.Digest, desc.MediaType, child.MediaType))
	}
	if platform := remotePlatformString(desc.Platform); platform != child.Platform {
		report.addCheck("child platform", imageVerifyFail, fmt.Sprintf("%s platform %q, summary %q", desc.Digest, platform, child.Platform))
	}
	shouldSelect := selectedDigest != "" && desc.Digest == selectedDigest
	if child.Selected != shouldSelect {
		report.addCheck("child selected", imageVerifyFail, fmt.Sprintf("%s selected=%v, want %v", desc.Digest, child.Selected, shouldSelect))
	}
	verifyManifestBundleChildManifest(data, child, report)
}

func verifyManifestBundleChildManifest(data []byte, child manifestBundleChildSummary, report *imageBundleVerifyReport) {
	var manifest ociimage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		report.addCheck("child parse", imageVerifyFail, fmt.Sprintf("%s: %v", child.Digest, err))
		return
	}
	base := remoteInspectManifestBase(manifest)
	var detail ImageRemoteIndexManifest
	if isCoveImageArtifactManifest(manifest) {
		detail = remoteIndexManifestFromOutput(inspectRemoteCoveImageArtifactManifestOnly(manifest, base))
	} else {
		parsed, err := ociimage.ParseManifest(manifest)
		if err != nil {
			report.addCheck("child parse", imageVerifyFail, fmt.Sprintf("%s: %v", child.Digest, err))
			return
		}
		detail = remoteIndexManifestFromOutput(inspectRemoteVMManifest(parsed, base))
	}
	if err := compareManifestBundleChildSummary(child, detail); err != nil {
		report.addCheck("child metadata", imageVerifyFail, fmt.Sprintf("%s: %v", child.Digest, err))
		return
	}
	report.addCheck("child metadata", imageVerifyPass, child.Digest)
}

func compareManifestBundleChildSummary(child manifestBundleChildSummary, detail ImageRemoteIndexManifest) error {
	if child.Format != "" && child.Format != detail.Format {
		return fmt.Errorf("format %q, parsed %q", child.Format, detail.Format)
	}
	if child.Kind != "" && child.Kind != detail.Kind {
		return fmt.Errorf("kind %q, parsed %q", child.Kind, detail.Kind)
	}
	if child.PullPlan != "" && child.PullPlan != detail.PullPlan {
		return fmt.Errorf("pull_plan %q, parsed %q", child.PullPlan, detail.PullPlan)
	}
	if child.DiskSize != 0 && child.DiskSize != detail.DiskSize {
		return fmt.Errorf("disk_size %d, parsed %d", child.DiskSize, detail.DiskSize)
	}
	if child.DiskFormat != "" && child.DiskFormat != detail.DiskFormat {
		return fmt.Errorf("disk_format %q, parsed %q", child.DiskFormat, detail.DiskFormat)
	}
	return nil
}

func verifyManifestBundleSelectedSummary(summary manifestBundleSummary, child manifestBundleChildSummary, report *imageBundleVerifyReport) {
	var mismatches []string
	if summary.Format != "" && child.Format != "" && summary.Format != child.Format {
		mismatches = append(mismatches, fmt.Sprintf("format %q, child %q", summary.Format, child.Format))
	}
	if summary.DiskFormat != "" && child.DiskFormat != "" && summary.DiskFormat != child.DiskFormat {
		mismatches = append(mismatches, fmt.Sprintf("disk_format %q, child %q", summary.DiskFormat, child.DiskFormat))
	}
	if summary.DiskSize != 0 && child.DiskSize != 0 && summary.DiskSize != child.DiskSize {
		mismatches = append(mismatches, fmt.Sprintf("disk_size %d, child %d", summary.DiskSize, child.DiskSize))
	}
	if len(mismatches) > 0 {
		report.addCheck("selected summary", imageVerifyFail, strings.Join(mismatches, "; "))
		return
	}
	report.addCheck("selected summary", imageVerifyPass, child.Digest)
}

func manifestBundleCleanRelPath(path string) (string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("absolute path %q", path)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe path %q", path)
	}
	return clean, nil
}

func manifestBundleIndexHasDigest(index ociimage.Index, digest string) bool {
	for _, desc := range index.Manifests {
		if desc.Digest == digest {
			return true
		}
	}
	return false
}

func (r *imageBundleVerifyReport) addCheck(name string, status imageVerifyStatus, detail string) {
	if detail != "" {
		detail = strings.TrimSpace(detail)
	}
	r.Checks = append(r.Checks, imageVerifyCheck{Name: name, Status: status, Detail: detail})
}

func (r *imageBundleVerifyReport) finish() {
	r.Verdict = imageVerifyPass
	for _, check := range r.Checks {
		switch check.Status {
		case imageVerifyFail:
			r.Verdict = imageVerifyFail
			return
		case imageVerifyWarn:
			r.Verdict = imageVerifyWarn
		}
	}
}

func writeImageBundleVerifyJSON(w io.Writer, report imageBundleVerifyReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bundle verify output: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeImageBundleVerifyText(w io.Writer, report imageBundleVerifyReport) {
	fmt.Fprintf(w, "Image bundle %s\n", report.Path)
	for _, check := range report.Checks {
		if check.Detail != "" {
			fmt.Fprintf(w, "  %-15s %s: %s\n", strings.ToLower(string(check.Status)), check.Name, check.Detail)
			continue
		}
		fmt.Fprintf(w, "  %-15s %s\n", strings.ToLower(string(check.Status)), check.Name)
	}
	fmt.Fprintf(w, "  summary:        %s\n", report.Verdict)
}

func printImageBundleUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image bundle <subcommand> [options]

Subcommands:
  verify <dir> [-json]   Verify an offline manifest bundle`)
}

func printImageBundleVerifyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image bundle verify <dir> [-json]

Verify an offline manifest bundle written by cove image inspect -remote
-manifest-dir or cove pull --dry-run --fetch-manifest --manifest-dir. The check
does not contact the registry; it validates summary.json, index.json,
selected.json, and every manifests/<digest>.json child file.

Flags:
  -json   emit machine-readable JSON`)
}
