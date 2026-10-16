package domain

import "strings"

// LastPathSegment returns the last non-empty segment of a POSIX or Windows path. Transcripts
// from either platform can be read on either platform, so it does not use path/filepath.
func LastPathSegment(p string) (string, bool) {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return "", false
	}
	return parts[len(parts)-1], true
}
