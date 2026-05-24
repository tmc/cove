package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/tmc/cove/internal/imagestore"
)

type ImageSearchResult struct {
	Ref     string   `json:"ref"`
	Name    string   `json:"name"`
	Tag     string   `json:"tag"`
	Size    int64    `json:"size"`
	Created string   `json:"created,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	Score   int      `json:"score,omitempty"`
}

func SearchImages(query string) ([]ImageSearchResult, error) {
	entries, err := ListImages()
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var out []ImageSearchResult
	for _, entry := range entries {
		result := ImageSearchResult{
			Ref:  entry.Ref.String(),
			Name: entry.Ref.Name,
			Tag:  entry.Ref.Tag,
		}
		if entry.Manifest != nil {
			result.Size = entry.Manifest.DiskSize
			if !entry.Manifest.CreatedAt.IsZero() {
				result.Created = entry.Manifest.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
		}
		result.Labels = imageSearchLabels(entry.Ref)
		haystack := strings.ToLower(entry.Ref.String() + " " + strings.Join(result.Labels, " "))
		score := imageSearchScore(query, haystack, entry.Ref)
		if query != "" && score == 0 {
			continue
		}
		result.Score = score
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Ref < out[j].Ref
	})
	return out, nil
}

func imageSearchScore(query, haystack string, ref imagestore.Ref) int {
	if query == "" {
		return 1
	}
	refText := strings.ToLower(ref.String())
	switch {
	case refText == query:
		return 100
	case strings.Contains(refText, query):
		return 80
	case strings.Contains(haystack, query):
		return 60
	case fuzzySubsequence(query, haystack):
		return 20
	default:
		return 0
	}
}

func fuzzySubsequence(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	j := 0
	for i := 0; i < len(haystack) && j < len(needle); i++ {
		if haystack[i] == needle[j] {
			j++
		}
	}
	return j == len(needle)
}

func imageSearchLabels(ref imagestore.Ref) []string {
	seen := map[string]bool{}
	var labels []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		labels = append(labels, s)
	}
	data, err := os.ReadFile(filepath.Join(ref.Path(), "LABELS"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			add(line)
		}
	}
	raw, err := readImageManifestMap(ref)
	if err == nil {
		switch v := raw["labels"].(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					add(s)
				}
			}
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(fmt.Sprintf("%s=%v", k, v[k]))
			}
		}
	}
	sort.Strings(labels)
	return labels
}

func runImageSearch(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image search", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printImageSearchUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := parseFlagsOrHelp(fs, moveImageSearchFlagsFirst(args)); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: cove image search [-json] [query]")
	}
	query := ""
	if fs.NArg() == 1 {
		query = fs.Arg(0)
	}
	results, err := SearchImages(query)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeImageSearchJSON(env.Stdout, results)
	}
	return writeImageSearchText(env.Stdout, results)
}

func printImageSearchUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image search [-json] [query]

Fuzzy-search local image refs and labels. With no query, lists all local
images ordered by ref.

Flags:
  -json    emit machine-readable JSON`)
}

func moveImageSearchFlagsFirst(args []string) []string {
	var flags, rest []string
	for _, arg := range args {
		switch arg {
		case "-json", "--json":
			flags = append(flags, arg)
		default:
			rest = append(rest, arg)
		}
	}
	return append(flags, rest...)
}

func writeImageSearchJSON(w io.Writer, results []ImageSearchResult) error {
	if results == nil {
		results = []ImageSearchResult{}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image search: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeImageSearchText(w io.Writer, results []ImageSearchResult) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No images found.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tSIZE\tCREATED\tLABELS")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", r.Ref, r.Size, r.Created, strings.Join(r.Labels, ", "))
	}
	return tw.Flush()
}
