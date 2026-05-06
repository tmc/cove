package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type HostLoad struct {
	Host  string
	Count int
	Error error
}

func SelectLeastLoadedHost(ctx context.Context, entries []Entry, query QueryFunc[string]) (Entry, []HostLoad, error) {
	if len(entries) == 0 {
		return Entry{}, nil, errors.New("fleet placement: no remotes configured")
	}
	results := QueryAll(ctx, entries, query)
	loads := make([]HostLoad, 0, len(results))
	var candidates []Entry
	counts := make(map[string]int, len(results))
	for i, result := range results {
		load := HostLoad{Host: result.Host, Error: result.Error}
		if result.Error == nil {
			load.Count = CountRunningVMs(result.Value)
			candidates = append(candidates, entries[i])
			counts[result.Host] = load.Count
		}
		loads = append(loads, load)
	}
	if len(candidates) == 0 {
		return Entry{}, loads, errors.New("fleet placement: all remotes unreachable")
	}
	sort.Slice(candidates, func(i, j int) bool {
		li, lj := counts[candidates[i].Name], counts[candidates[j].Name]
		if li != lj {
			return li < lj
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates[0], loads, nil
}

func CountRunningVMs(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(strings.ToLower(line), "running") {
			count++
		}
	}
	return count
}

func FormatHostLoads(loads []HostLoad) string {
	if len(loads) == 0 {
		return ""
	}
	parts := make([]string, 0, len(loads))
	for _, load := range loads {
		if load.Error != nil {
			parts = append(parts, fmt.Sprintf("%s=unreachable", load.Host))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", load.Host, load.Count))
	}
	return strings.Join(parts, " ")
}
