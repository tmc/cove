package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

type imageDiffOutput struct {
	Refs    [2]string       `json:"refs"`
	Files   []imageDiffFile `json:"files"`
	Changed bool            `json:"changed"`
}

type imageDiffFile struct {
	Name   string              `json:"name"`
	Status string              `json:"status"`
	Old    *imageDiffFileValue `json:"old,omitempty"`
	New    *imageDiffFileValue `json:"new,omitempty"`
}

type imageDiffFileValue struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func diffCommand(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	fs.Usage = func() { printDiffUsage(fs.Output()) }
	if err := parseFlagsOrHelp(fs, moveKnownFlagsFirst(args, map[string]bool{"json": false})); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: cove diff <ref-a> <ref-b> [-json]")
	}
	a, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := ParseImageRef(fs.Arg(1))
	if err != nil {
		return err
	}
	out, err := imageDiff(a, b)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeImageDiffJSON(os.Stdout, out)
	}
	return writeImageDiffText(os.Stdout, out)
}

func printDiffUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove diff <ref-a> <ref-b> [-json]

Compare local image disk layer metadata for two image refs.

Flags:
  -json    emit machine-readable JSON`)
}

func imageDiff(a, b ImageRef) (imageDiffOutput, error) {
	if err := requireImageRefDir(a); err != nil {
		return imageDiffOutput{}, err
	}
	if err := requireImageRefDir(b); err != nil {
		return imageDiffOutput{}, err
	}
	names, err := imageDiffDiskNames(a, b)
	if err != nil {
		return imageDiffOutput{}, err
	}
	files := make([]imageDiffFile, 0, len(names))
	changed := false
	for _, name := range names {
		file, err := diffImageFile(a, b, name)
		if err != nil {
			return imageDiffOutput{}, err
		}
		files = append(files, file)
		if file.Status != "UNCHANGED" {
			changed = true
		}
	}
	return imageDiffOutput{
		Refs:    [2]string{a.String(), b.String()},
		Files:   files,
		Changed: changed,
	}, nil
}

func imageDiffDiskNames(a, b ImageRef) ([]string, error) {
	ma, err := LoadImageManifest(a)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", a, err)
	}
	mb, err := LoadImageManifest(b)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", b, err)
	}
	return imageDiskNamesForManifests(ma, mb), nil
}

func imageDiffLayerNames(a, b ImageRef) ([]string, error) {
	ma, err := LoadImageManifest(a)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", a, err)
	}
	mb, err := LoadImageManifest(b)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", b, err)
	}
	return imageLayerNamesForManifests(ma, mb), nil
}

func imageDiskNamesForManifests(manifests ...*ImageManifest) []string {
	var names []string
	seen := make(map[string]bool)
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		name := imageLayoutDiskFile(manifest.OSType)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func imageLayerNamesForManifests(manifests ...*ImageManifest) []string {
	var names []string
	seen := make(map[string]bool)
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		for _, name := range imageLayoutRequiredFiles(manifest.OSType) {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func requireImageRefDir(ref ImageRef) error {
	info, err := os.Stat(ref.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("diff: image ref not found: %s", ref)
		}
		return fmt.Errorf("diff: inspect image ref %s: %w", ref, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("diff: image ref is not a directory: %s", ref)
	}
	return nil
}

func diffImageFile(a, b ImageRef, name string) (imageDiffFile, error) {
	av, aok, err := inspectImageLayer(a, name)
	if err != nil {
		return imageDiffFile{}, fmt.Errorf("diff %s %s: %w", a, name, err)
	}
	bv, bok, err := inspectImageLayer(b, name)
	if err != nil {
		return imageDiffFile{}, fmt.Errorf("diff %s %s: %w", b, name, err)
	}
	status := "UNCHANGED"
	switch {
	case !aok && bok:
		status = "ADDED"
	case aok && !bok:
		status = "REMOVED"
	case aok && bok && (av.Size != bv.Size || av.Digest != bv.Digest):
		status = "CHANGED"
	}
	return imageDiffFile{
		Name:   name,
		Status: status,
		Old:    imageDiffValue(av, aok),
		New:    imageDiffValue(bv, bok),
	}, nil
}

func imageDiffValue(v imageInspectLayerValue, ok bool) *imageDiffFileValue {
	if !ok {
		return nil
	}
	return &imageDiffFileValue{
		Size:   v.Size,
		SHA256: v.Digest,
	}
}

func writeImageDiffJSON(w io.Writer, out imageDiffOutput) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image diff: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeImageDiffText(w io.Writer, out imageDiffOutput) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "File\t%s\t%s\tStatus\n", out.Refs[0], out.Refs[1])
	fmt.Fprintf(tw, "----\t%s\t%s\t------\n", "----------", "----------")
	for _, file := range out.Files {
		fmt.Fprintf(tw, "%s\t%s\t%s\t[%s]\n", file.Name, imageDiffString(file.Old), imageDiffString(file.New), file.Status)
	}
	return tw.Flush()
}

func imageDiffString(v *imageDiffFileValue) string {
	if v == nil {
		return "<missing>"
	}
	return fmt.Sprintf("%s (%d bytes)", v.SHA256, v.Size)
}
