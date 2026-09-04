package utils

import "strings"

// ContainsControl reports whether value holds an ASCII control character. Such
// characters are rejected in identifiers and log-bound fields so they cannot
// forge line breaks or terminal escapes downstream.
func ContainsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

// NormalizeUsername folds a submitted username to its canonical stored form so
// lookups and uniqueness checks agree on casing and surrounding whitespace.
func NormalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
