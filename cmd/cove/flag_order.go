package main

import "strings"

func moveKnownFlagsFirst(args []string, takesValue map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			rest = append(rest, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		needsValue, ok := takesValue[name]
		if !ok {
			rest = append(rest, arg)
			continue
		}
		flags = append(flags, arg)
		if needsValue && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, rest...)
}
