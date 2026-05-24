// image_inspect.go — `cove image inspect <ref>` subcommand.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/imagestore"
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

type imageInspectDiff struct {
	Refs      [2]string                        `json:"refs"`
	Added     map[string]imageInspectDiffValue `json:"added"`
	Removed   map[string]imageInspectDiffValue `json:"removed"`
	Changed   map[string]imageInspectDiffValue `json:"changed"`
	Unchanged map[string]imageInspectDiffValue `json:"unchanged,omitempty"`
	Layers    []imageInspectLayerDiff          `json:"layers"`

	fields []imageInspectDiffRow
}

type imageInspectDiffValue struct {
	Old any `json:"old"`
	New any `json:"new"`
}

type imageInspectDiffRow struct {
	Field  string
	Old    any
	New    any
	Status string
}

type imageInspectLayerDiff struct {
	Name   string `json:"name"`
	Old    any    `json:"old"`
	New    any    `json:"new"`
	Status string `json:"status"`
}

type imageInspectLayerValue struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type imageInspectErrorOutput struct {
	Ref   string `json:"ref"`
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

// InspectImage assembles the inspect output for ref. The fork list reuses
// VMsForkedFromImage so it stays in lockstep with `cove image rm`'s gate.
func InspectImage(ref imagestore.Ref) (ImageInspectOutput, error) {
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
func machineModelFingerprint(ref imagestore.Ref) (string, error) {
	data, err := os.ReadFile(filepath.Join(ref.Path(), "hw.model"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func runImageInspect(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image inspect", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printImageInspectUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	diff := fs.Bool("diff", false, "compare two image refs")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if *diff {
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: cove image inspect [-json] --diff <ref-a> <ref-b>")
		}
		a, err := ParseImageRef(fs.Arg(0))
		if err != nil {
			return err
		}
		b, err := ParseImageRef(fs.Arg(1))
		if err != nil {
			return err
		}
		out, err := InspectImageDiff(a, b)
		if err != nil {
			if *asJSON {
				_ = writeCLIErrorJSON(env.Stdout, "image inspect", err, "run 'cove image list' to see local images")
			}
			return err
		}
		if *asJSON {
			return writeInspectDiffJSON(env.Stdout, out)
		}
		return writeInspectDiffText(env.Stdout, out)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove image inspect [-json] <name[:tag]>")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	out, err := InspectImage(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			hint := fmt.Sprintf("run 'cove image list' to see local images or 'cove image search %s' to search refs", ref.Name)
			if *asJSON {
				if jsonErr := writeInspectErrorJSON(env.Stdout, imageInspectErrorOutput{
					Ref:   ref.String(),
					Error: err.Error(),
					Hint:  hint,
				}); jsonErr != nil {
					return jsonErr
				}
			}
			return fmt.Errorf("%w\nhint: %s", err, hint)
		}
		return err
	}
	if *asJSON {
		return writeInspectJSON(env.Stdout, out)
	}
	return writeInspectText(env.Stdout, out)
}

func printImageInspectUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image inspect [-json] <name[:tag]>
       cove image inspect [-json] -diff <ref-a> <ref-b>

Show a local image manifest summary, downstream forks, and provenance. With
-diff, compare two image manifests.

Flags:
  -json    emit machine-readable JSON
  -diff    compare two image refs`)
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

func writeInspectErrorJSON(w io.Writer, out imageInspectErrorOutput) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inspect error: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func InspectImageDiff(a, b imagestore.Ref) (imageInspectDiff, error) {
	ma, err := readImageManifestMap(a)
	if err != nil {
		return imageInspectDiff{}, fmt.Errorf("inspect diff %s: %w", a, err)
	}
	mb, err := readImageManifestMap(b)
	if err != nil {
		return imageInspectDiff{}, fmt.Errorf("inspect diff %s: %w", b, err)
	}
	fields := diffManifestFields(ma, mb)
	layers, err := diffImageLayers(a, b)
	if err != nil {
		return imageInspectDiff{}, err
	}
	out := imageInspectDiff{
		Refs:      [2]string{a.String(), b.String()},
		Added:     make(map[string]imageInspectDiffValue),
		Removed:   make(map[string]imageInspectDiffValue),
		Changed:   make(map[string]imageInspectDiffValue),
		Unchanged: make(map[string]imageInspectDiffValue),
		Layers:    layers,
		fields:    fields,
	}
	for _, row := range fields {
		out.addField(row.Field, row.Old, row.New, row.Status)
	}
	for _, layer := range layers {
		out.addField("layer."+layer.Name, layer.Old, layer.New, layer.Status)
	}
	return out, nil
}

func (d *imageInspectDiff) addField(name string, old, new any, status string) {
	v := imageInspectDiffValue{Old: old, New: new}
	switch status {
	case "ADDED":
		d.Added[name] = v
	case "REMOVED":
		d.Removed[name] = v
	case "CHANGED":
		d.Changed[name] = v
	case "UNCHANGED":
		d.Unchanged[name] = v
	}
}

func readImageManifestMap(ref imagestore.Ref) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(ref.Path(), "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read image manifest: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse image manifest: %w", err)
	}
	return m, nil
}

func diffManifestFields(a, b map[string]any) []imageInspectDiffRow {
	seen := make(map[string]bool)
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range b {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := imageInspectFieldRank(keys[i]), imageInspectFieldRank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	rows := make([]imageInspectDiffRow, 0, len(keys))
	for _, key := range keys {
		av, aok := a[key]
		bv, bok := b[key]
		status := "UNCHANGED"
		switch {
		case !aok && bok:
			status = "ADDED"
		case aok && !bok:
			status = "REMOVED"
		case !jsonValueEqual(av, bv):
			status = "CHANGED"
		}
		rows = append(rows, imageInspectDiffRow{
			Field:  key,
			Old:    missingValue(aok, av),
			New:    missingValue(bok, bv),
			Status: status,
		})
	}
	return rows
}

func imageInspectFieldRank(field string) int {
	order := []string{
		"name",
		"tag",
		"cove_commit",
		"agent_commit",
		"agent_features",
		"built_at",
		"build_recipe",
		"source_image",
		"baseImage",
		"diskSHA256",
		"diskSize",
	}
	for i, f := range order {
		if field == f {
			return i
		}
	}
	return len(order)
}

func diffImageLayers(a, b imagestore.Ref) ([]imageInspectLayerDiff, error) {
	names, err := imageDiffLayerNames(a, b)
	if err != nil {
		return nil, err
	}
	rows := make([]imageInspectLayerDiff, 0, len(names))
	for _, name := range names {
		av, aok, err := inspectImageLayer(a, name)
		if err != nil {
			return nil, fmt.Errorf("inspect diff %s %s: %w", a, name, err)
		}
		bv, bok, err := inspectImageLayer(b, name)
		if err != nil {
			return nil, fmt.Errorf("inspect diff %s %s: %w", b, name, err)
		}
		status := "UNCHANGED"
		switch {
		case !aok && bok:
			status = "ADDED"
		case aok && !bok:
			status = "REMOVED"
		case !reflect.DeepEqual(av, bv):
			status = "CHANGED"
		}
		rows = append(rows, imageInspectLayerDiff{
			Name:   name,
			Old:    missingValue(aok, av),
			New:    missingValue(bok, bv),
			Status: status,
		})
	}
	return rows, nil
}

func inspectImageLayer(ref imagestore.Ref, name string) (imageInspectLayerValue, bool, error) {
	path := filepath.Join(ref.Path(), name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return imageInspectLayerValue{}, false, nil
		}
		return imageInspectLayerValue{}, false, err
	}
	if info.IsDir() {
		return imageInspectLayerValue{}, false, fmt.Errorf("is a directory")
	}
	sum, size, err := sha256AndSize(path)
	if err != nil {
		return imageInspectLayerValue{}, false, err
	}
	return imageInspectLayerValue{Digest: "sha256:" + sum, Size: size}, true, nil
}

func writeInspectDiffJSON(w io.Writer, out imageInspectDiff) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inspect diff: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeInspectDiffText(w io.Writer, out imageInspectDiff) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "Field\t%s\t%s\tStatus\n", out.Refs[0], out.Refs[1])
	fmt.Fprintf(tw, "-----\t%s\t%s\t------\n", "----------", "----------")
	for _, row := range out.fields {
		fmt.Fprintf(tw, "%s\t%s\t%s\t[%s]\n", row.Field, inspectDiffString(row.Old), inspectDiffString(row.New), row.Status)
	}
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "Layers")
	fmt.Fprintf(tw, "Layer\t%s\t%s\tStatus\n", out.Refs[0], out.Refs[1])
	fmt.Fprintf(tw, "-----\t%s\t%s\t------\n", "----------", "----------")
	for _, layer := range out.Layers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t[%s]\n", layer.Name, inspectDiffString(layer.Old), inspectDiffString(layer.New), layer.Status)
	}
	return tw.Flush()
}

func missingValue(ok bool, v any) any {
	if !ok {
		return "<missing>"
	}
	return v
}

func jsonValueEqual(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return reflect.DeepEqual(a, b)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return reflect.DeepEqual(a, b)
	}
	return string(ab) == string(bb)
}

func inspectDiffString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		return inspectDiffListString(v)
	case imageInspectLayerValue:
		if v.Digest == "" && v.Size == 0 {
			return ""
		}
		return fmt.Sprintf("%s (%d bytes)", v.Digest, v.Size)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

func inspectDiffListString(list []any) string {
	parts := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			data, err := json.Marshal(v)
			if err != nil {
				parts = append(parts, fmt.Sprint(v))
				continue
			}
			parts = append(parts, string(data))
			continue
		}
		parts = append(parts, s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
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
