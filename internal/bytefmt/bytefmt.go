package bytefmt

import (
	"fmt"
	"strconv"
	"strings"
)

// Size formats bytes as a human-readable size.
func Size(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// Parse parses a positive byte size with optional binary unit suffix.
func Parse(value string) (uint64, error) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, fmt.Errorf("size required")
	}

	i := len(s)
	for i > 0 {
		c := s[i-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		i--
	}
	numPart := strings.TrimSpace(s[:i])
	unitPart := strings.ToLower(strings.TrimSpace(s[i:]))
	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	n, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", value, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("size must be positive")
	}

	multiplier := float64(1)
	switch unitPart {
	case "", "b":
	case "k", "kb", "kib":
		multiplier = 1024
	case "m", "mb", "mib":
		multiplier = 1024 * 1024
	case "g", "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit %q", unitPart)
	}

	return uint64(n * multiplier), nil
}
