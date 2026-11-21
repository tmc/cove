package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type HostLoad struct {
	Host     string
	Count    int
	Error    error
	Cordoned bool
	Leases   int
}

func SelectLeastLoadedHost(ctx context.Context, entries []Entry, query QueryFunc[string]) (Entry, []HostLoad, error) {
	return SelectLeastLoadedHostWithLeases(ctx, entries, nil, query)
}

func SelectLeastLoadedHostWithLeases(ctx context.Context, entries []Entry, leases map[string]int, query QueryFunc[string]) (Entry, []HostLoad, error) {
	if len(entries) == 0 {
		return Entry{}, nil, errors.New("fleet placement: no remotes configured")
	}
	active, loads := ActivePlacementEntries(entries)
	if len(active) == 0 {
		return Entry{}, loads, errors.New("fleet placement: all remotes cordoned")
	}
	results := QueryAll(ctx, active, query)
	var candidates []Entry
	counts := make(map[string]int, len(results))
	for i, result := range results {
		load := HostLoad{Host: result.Host, Error: result.Error}
		if result.Error == nil {
			load.Count = CountRunningVMs(result.Value)
			load.Leases = leases[result.Host]
			candidates = append(candidates, active[i])
			counts[result.Host] = load.Count + load.Leases
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

func ActivePlacementEntries(entries []Entry) ([]Entry, []HostLoad) {
	active := make([]Entry, 0, len(entries))
	loads := make([]HostLoad, 0, len(entries))
	for _, entry := range entries {
		if entry.Cordoned {
			loads = append(loads, HostLoad{Host: entry.Name, Cordoned: true})
			continue
		}
		active = append(active, entry)
	}
	return active, loads
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
		if load.Cordoned {
			parts = append(parts, fmt.Sprintf("%s=cordoned", load.Host))
			continue
		}
		if load.Error != nil {
			parts = append(parts, fmt.Sprintf("%s=unreachable", load.Host))
			continue
		}
		if load.Leases > 0 {
			suffix := "lease"
			if load.Leases != 1 {
				suffix = "leases"
			}
			parts = append(parts, fmt.Sprintf("%s=%d+%d%s", load.Host, load.Count, load.Leases, suffix))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", load.Host, load.Count))
	}
	return strings.Join(parts, " ")
}
