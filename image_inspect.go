// image_inspect.go — `cove image inspect <ref>` subcommand.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImageInspectOutput is the JSON shape emitted by `cove image inspect -json`.
// Field order matches the Go struct; `encoding/json` honors that for stable
// output across runs.
type ImageInspectOutput struct {
	Ref            string   `json:"ref"`
	Name           string   `json:"name"`
	Tag            string   `json:"tag"`
	ManifestPath   string   `json:"manifest_path"`
	DiskSize       int64    `json:"disk_size"`
	DiskSHA256     string   `json:"disk_sha256"`
	BaseImage      string   `json:"base_image,omitempty"`
	CoveCommit     string   `json:"cove_commit,omitempty"`
	AgentCommit    string   `json:"agent_commit,omitempty"`
	AgentFeatures  []string `json:"agent_features,omitempty"`
	BuildRecipe    string   `json:"build_recipe,omitempty"`
	SourceImage    string   `json:"source_image,omitempty"`
	Created        string   `json:"created,omitempty"`
	BuiltAt        string   `json:"built_at,omitempty"`
	DefaultNetwork string   `json:"default_network,omitempty"`
	DefaultSandbox string   `json:"default_sandbox,omitempty"`
	LegacyManifest bool     `json:"legacy_manifest,omitempty"`
	MachineModelID string   `json:"machine_model_id,omitempty"`
	Forks          []string `json:"forks"`
	ForkCount      int      `json:"fork_count"`
}

// InspectImage assembles the inspect output for ref. The fork list reuses
// VMsForkedFromImage so it stays in lockstep with `cove image rm`'s gate.
func InspectImage(ref ImageRef) (ImageInspectOutput, error) {
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		return ImageInspectOutput{}, fmt.Errorf("inspect %s: %w", ref, err)
	}
	forks, err := VMsForkedFromImage(ref)
	if err != nil {
		return ImageInspectOutput{}, fmt.Errorf("inspect %s: %w", ref, err)
	}
	if forks == nil {
		forks = []string{}
	}
	out := ImageInspectOutput{
		Ref:            ref.String(),
		Name:           manifest.Name,
		Tag:            manifest.Tag,
		ManifestPath:   filepath.Join(ref.Path(), "manifest.json"),
		DiskSize:       manifest.DiskSize,
		DiskSHA256:     manifest.DiskSHA256,
		BaseImage:      manifest.BaseImage,
		CoveCommit:     manifest.CoveCommit,
		AgentCommit:    manifest.AgentCommit,
		AgentFeatures:  append([]string(nil), manifest.AgentFeatures...),
		BuildRecipe:    manifest.BuildRecipe,
		SourceImage:    manifest.SourceImage,
		DefaultNetwork: manifest.DefaultNetwork,
		DefaultSandbox: manifest.DefaultSandbox,
		LegacyManifest: legacyImageManifest(manifest),
		Forks:          forks,
		ForkCount:      len(forks),
	}
	if !manifest.CreatedAt.IsZero() {
		out.Created = manifest.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !manifest.BuiltAt.IsZero() {
		out.BuiltAt = manifest.BuiltAt.UTC().Format(time.RFC3339)
	}
	if id, err := machineModelFingerprint(ref); err == nil {
		out.MachineModelID = id
	}
	return out, nil
}

// machineModelFingerprint returns a stable short-hash of hw.model (the
// VZMacHardwareModel data blob). The raw bytes are an opaque Apple
// payload; fingerprint keeps inspect output a single scalar while still
// letting callers diff identity across images.
func machineModelFingerprint(ref ImageRef) (string, error) {
	data, err := os.ReadFile(filepath.Join(ref.Path(), "hw.model"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func runImageInspect(args []string) error {
	fs := flag.NewFlagSet("image inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: cove image inspect <name[:tag]> [-json]")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	out, err := InspectImage(ref)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeInspectJSON(os.Stdout, out)
	}
	return writeInspectText(os.Stdout, out)
}

func writeInspectJSON(w io.Writer, out ImageInspectOutput) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inspect output: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeInspectText(w io.Writer, out ImageInspectOutput) error {
	fmt.Fprintf(w, "Image %s\n", out.Ref)
	fmt.Fprintf(w, "  manifest:    %s\n", out.ManifestPath)
	fmt.Fprintf(w, "  disk:        %d bytes\n", out.DiskSize)
	fmt.Fprintf(w, "  sha256:      %s\n", out.DiskSHA256)
	if out.BaseImage != "" {
		fmt.Fprintf(w, "  base image:  %s\n", out.BaseImage)
	}
	if out.LegacyManifest {
		fmt.Fprintf(w, "  legacy:      yes\n")
	}
	if out.CoveCommit != "" {
		fmt.Fprintf(w, "  cove commit: %s\n", out.CoveCommit)
	}
	if out.AgentCommit != "" {
		fmt.Fprintf(w, "  agent:       %s\n", out.AgentCommit)
	}
	if len(out.AgentFeatures) > 0 {
		fmt.Fprintf(w, "  features:    %s\n", strings.Join(out.AgentFeatures, ", "))
	}
	if out.BuildRecipe != "" {
		fmt.Fprintf(w, "  recipe:      %s\n", out.BuildRecipe)
	}
	if out.SourceImage != "" {
		fmt.Fprintf(w, "  source image: %s\n", out.SourceImage)
	}
	if out.Created != "" {
		fmt.Fprintf(w, "  created:     %s\n", out.Created)
	}
	if out.BuiltAt != "" {
		fmt.Fprintf(w, "  built at:    %s\n", out.BuiltAt)
	}
	if out.DefaultNetwork != "" {
		fmt.Fprintf(w, "  network:     %s\n", out.DefaultNetwork)
	}
	if out.DefaultSandbox != "" {
		fmt.Fprintf(w, "  sandbox:     %s\n", out.DefaultSandbox)
	}
	if out.MachineModelID != "" {
		fmt.Fprintf(w, "  hw.model:    sha256:%s\n", out.MachineModelID)
	}
	fmt.Fprintf(w, "  forks:       %d\n", out.ForkCount)
	for _, f := range out.Forks {
		fmt.Fprintf(w, "    - %s\n", f)
	}
	return nil
}
