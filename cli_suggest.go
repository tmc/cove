package main

import "strings"

// suggestCommand returns the closest known command to cmd, or "" if none is
// close enough. The threshold scales with the input length so short typos
// still match but long unrelated strings do not.
func suggestCommand(cmd string) string {
	if cmd == "" {
		return ""
	}
	best := ""
	bestDist := -1
	for _, k := range commandNames() {
		d := levenshtein(cmd, k)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = k
		}
	}
	limit := max(len(cmd)/2, 2)
	if bestDist <= limit {
		return best
	}
	// Fall back to a substring hit so "sharedfolder" → "shared-folder".
	lc := strings.ToLower(cmd)
	for _, k := range commandNames() {
		if strings.Contains(k, lc) || strings.Contains(lc, k) {
			return k
		}
	}
	return ""
}

// levenshtein computes the edit distance between a and b using a single-row
// dynamic programming buffer.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
