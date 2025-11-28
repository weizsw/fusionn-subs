package util

import "strings"

// MaskSecret masks a secret value, showing only first 4 chars.
func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	const keep = 4
	if len(value) <= keep {
		return strings.Repeat("*", len(value))
	}
	return value[:keep] + strings.Repeat("*", len(value)-keep)
}
