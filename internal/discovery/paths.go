package discovery

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Ids (sessionId, agentId) become file names: only [A-Za-z0-9_-].
	safeIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	// Device names reserved on Windows (any case), refused so that a profile stays portable.
	reservedRE = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

// IsSafeID reports whether s is usable as a file name component.
func IsSafeID(s string) bool { return safeIDRE.MatchString(s) && !reservedRE.MatchString(s) }

// IsInside reports whether candidate is strictly inside root (lexical comparison).
func IsInside(root, candidate string) bool {
	r, err1 := filepath.Abs(root)
	c, err2 := filepath.Abs(candidate)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(r, c)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

// ContainmentRoot is a root as it was built and its realpath.
type ContainmentRoot struct {
	Dir  string
	Real string
}

// NewContainmentRoot resolves dir; false if it does not exist.
func NewContainmentRoot(fs FS, dir string) (ContainmentRoot, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ContainmentRoot{}, false
	}
	resolved, err := fs.Realpath(dir)
	if err != nil {
		return ContainmentRoot{}, false
	}
	return ContainmentRoot{Dir: abs, Real: resolved}, true
}

// ResolveContained returns the canonical path of candidate if it is lexically under root.Dir,
// exists, and its realpath (symlinks resolved) stays under root.Real.
func ResolveContained(fs FS, root ContainmentRoot, candidate string) (string, bool) {
	if !IsInside(root.Dir, candidate) {
		return "", false
	}
	resolved, err := fs.Realpath(candidate)
	if err != nil || !IsInside(root.Real, resolved) {
		return "", false
	}
	return resolved, true
}
