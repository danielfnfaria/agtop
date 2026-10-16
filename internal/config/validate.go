package config

import (
	"path/filepath"

	"github.com/danielfnfaria/agtop/internal/domain"
)

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

// ValidateOpencodeDir checks that dir is an opencode data directory: it exists and holds
// opencode.db.
func ValidateOpencodeDir(dir string, fsys FS) ValidationResult {
	if !fsys.IsDirectory(dir) {
		return ValidationResult{Error: dir + " does not exist or is not a directory"}
	}
	if !fsys.IsRegularFile(filepath.Join(dir, OpencodeDBName)) {
		return ValidationResult{Error: dir + " does not look like an opencode data directory: missing " + OpencodeDBName}
	}
	return ValidationResult{}
}

// ValidateProfile validates dir as the directory of a profile of the given provider.
func ValidateProfile(p ProfileDir, fsys FS) ValidationResult {
	if p.Provider == domain.Opencode {
		return ValidateOpencodeDir(p.Dir, fsys)
	}
	return ValidateClaudeDir(p.Dir, fsys)
}
