package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"unicode"
)

func hostOf(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func padRight(value string, width int) string {
	display := 0
	for _, r := range value {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
			r >= 0x2e80 && r <= 0xa4cf && r != 0x303f ||
			r >= 0xac00 && r <= 0xd7a3 ||
			r >= 0xf900 && r <= 0xfaff ||
			r >= 0xfe10 && r <= 0xfe19 ||
			r >= 0xfe30 && r <= 0xfe6f ||
			r >= 0xff00 && r <= 0xff60 ||
			r >= 0xffe0 && r <= 0xffe6):
			display += 2
		default:
			display++
		}
	}
	if display >= width {
		return value
	}
	return value + strings.Repeat(" ", width-display)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}
