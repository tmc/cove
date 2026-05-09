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
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove diff <ref-a> <ref-b> [-json]")
		fs.PrintDefaults()
	}
	if err := parseFlagsOrHelp(fs, args); err != nil {
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

func imageDiff(a, b ImageRef) (imageDiffOutput, error) {
	file, err := diffImageFile(a, b, "disk.img")
	if err != nil {
		return imageDiffOutput{}, err
	}
	return imageDiffOutput{
		Refs:    [2]string{a.String(), b.String()},
		Files:   []imageDiffFile{file},
		Changed: file.Status != "UNCHANGED",
	}, nil
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
