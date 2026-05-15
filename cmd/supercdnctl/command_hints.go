package main

import "strings"

func appendCommandHints(existing []string, additions ...string) []string {
	out := make([]string, 0, len(existing)+len(additions))
	seen := map[string]bool{}
	for _, value := range append(existing, additions...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cliHintArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\"`$&|;()<>[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
