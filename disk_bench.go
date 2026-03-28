package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const defaultDiskBenchSizes = "8MiB,64MiB"

type diskBenchSize struct {
	Label string
	Bytes int64
}

func parseDiskBenchSizes(value string) ([]diskBenchSize, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultDiskBenchSizes
	}
	fields := strings.Split(value, ",")
	sizes := make([]diskBenchSize, 0, len(fields))
	for _, field := range fields {
		size, err := parseDiskBenchSize(field)
		if err != nil {
			return nil, err
		}
		sizes = append(sizes, size)
	}
	return sizes, nil
}

func parseDiskBenchSize(value string) (diskBenchSize, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return diskBenchSize{}, fmt.Errorf("empty size")
	}

	type suffix struct {
		name string
		mul  int64
	}
	suffixes := []suffix{
		{name: "GIB", mul: 1024 * 1024 * 1024},
		{name: "MIB", mul: 1024 * 1024},
		{name: "KIB", mul: 1024},
		{name: "GB", mul: 1000 * 1000 * 1000},
		{name: "MB", mul: 1000 * 1000},
		{name: "KB", mul: 1000},
		{name: "B", mul: 1},
	}

	upper := strings.ToUpper(value)
	for _, suffix := range suffixes {
		if !strings.HasSuffix(upper, suffix.name) {
			continue
		}
		n := strings.TrimSpace(value[:len(value)-len(suffix.name)])
		if n == "" {
			return diskBenchSize{}, fmt.Errorf("size %q missing numeric value", value)
		}
		count, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return diskBenchSize{}, fmt.Errorf("parse size %q: %w", value, err)
		}
		if count <= 0 {
			return diskBenchSize{}, fmt.Errorf("parse size %q: value must be positive", value)
		}
		size := count * suffix.mul
		return diskBenchSize{Label: formatDiskBenchSize(size), Bytes: size}, nil
	}

	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return diskBenchSize{}, fmt.Errorf("parse size %q: %w", value, err)
	}
	if count <= 0 {
		return diskBenchSize{}, fmt.Errorf("parse size %q: value must be positive", value)
	}
	return diskBenchSize{Label: formatDiskBenchSize(count), Bytes: count}, nil
}

func formatDiskBenchSize(size int64) string {
	switch {
	case size%(1024*1024*1024) == 0:
		return fmt.Sprintf("%dGiB", size/(1024*1024*1024))
	case size%(1024*1024) == 0:
		return fmt.Sprintf("%dMiB", size/(1024*1024))
	case size%1024 == 0:
		return fmt.Sprintf("%dKiB", size/1024)
	default:
		return fmt.Sprintf("%dB", size)
	}
}

func sanitizeBenchConfigValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "default"
	}
	return out
}

func diskBenchName(scope, location, tag, op string, size diskBenchSize, diskLabel, mountLabel string) string {
	return strings.Join([]string{
		"scope=" + sanitizeBenchConfigValue(scope),
		"location=" + sanitizeBenchConfigValue(location),
		"tag=" + sanitizeBenchConfigValue(tag),
		"op=" + sanitizeBenchConfigValue(op),
		"size=" + sanitizeBenchConfigValue(size.Label),
		"disk=" + sanitizeBenchConfigValue(diskLabel),
		"mount=" + sanitizeBenchConfigValue(mountLabel),
	}, "/")
}
