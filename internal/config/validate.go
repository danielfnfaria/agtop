package config

import "path/filepath"

// ValidateClaudeDir checks that dir looks like a Claude Code config directory. A missing
// directory or projects/ is an error; a missing sessions/ is only a warning (the viewer needs
// it to detect live sessions).
func ValidateClaudeDir(dir string, fsys FS) ValidationResult {
	if !fsys.IsDirectory(dir) {
		return ValidationResult{Error: dir + " does not exist or is not a directory"}
	}
	if !fsys.IsDirectory(filepath.Join(dir, "projects")) {
		return ValidationResult{Error: dir + " does not look like a Claude Code config directory: missing projects/"}
	}
	var res ValidationResult
	if !fsys.IsDirectory(filepath.Join(dir, "sessions")) {
		res.Warnings = append(res.Warnings, dir+" has no sessions/: agtop uses sessions/<pid>.json to detect live sessions, "+
			"which only exists in recent Claude Code versions; live sessions will not be detected")
	}
	return res
}
