package utils

import "strings"

// ContainsParentPathComponent reports whether a slash-separated archive path
// walks upwards through "..", which would let an extracted entry escape its
// destination directory.
func ContainsParentPathComponent(value string) bool {
	for component := range strings.SplitSeq(value, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}
