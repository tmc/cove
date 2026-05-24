package covecli

import "strings"

// Suggest returns the closest choice to input, or "" if none is close enough.
func Suggest(input string, choices []string) string {
	if input == "" {
		return ""
	}
	best := ""
	bestDist := -1
	for _, choice := range choices {
		d := levenshtein(input, choice)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = choice
		}
	}
	limit := max(len(input)/2, 2)
	if bestDist <= limit {
		return best
	}
	lc := strings.ToLower(input)
	for _, choice := range choices {
		lowerChoice := strings.ToLower(choice)
		if strings.Contains(lowerChoice, lc) || strings.Contains(lc, lowerChoice) {
			return choice
		}
	}
	return ""
}

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
