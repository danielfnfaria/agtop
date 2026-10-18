package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// FindSiblingProfiles lists <homedir>/.claude and <homedir>/.claude-* directories that look
// like Claude Code config directories (valid, no error), other than current (compared by
// realpath), in alphabetical order.
func FindSiblingProfiles(homedir, current string, fsys FS) []ProfileDir {
	real := func(p string) string {
		if r, err := fsys.Realpath(p); err == nil {
			return r
		}
		return p
	}
	currentReal := real(current)
	names := fsys.ListDir(homedir)
	sort.Strings(names)
	var found []ProfileDir
	for _, name := range names {
		if name != ".claude" && !strings.HasPrefix(name, ".claude-") {
			continue
		}
		dir := filepath.Join(homedir, name)
		if !fsys.IsDirectory(dir) || ValidateClaudeDir(dir, fsys).Error != "" || real(dir) == currentReal {
			continue
		}
		found = append(found, ProfileDir{Label: DefaultLabel(dir), Dir: dir, Source: SourceConfig})
	}
	return found
}

// InitialConfigInput is the input of RenderInitialConfig.
type InitialConfigInput struct {
	// Profile is the directory agtop would watch without a file.
	Profile ProfileDir
	// Hints are other directories worth suggesting; written as comments.
	Hints []ProfileDir
	// Opencode suggests an opencode profile (its database exists).
	Opencode bool
	Homedir  string
}

// RenderInitialConfig returns the text of the config file created on the first run.
func RenderInitialConfig(in InitialConfigInput) string {
	lines := []string{
		"# agtop config file, created on the first run.",
		"# [profiles] maps a label (shown in the viewer) to a Claude Code config directory.",
		"# This file takes precedence over CLAUDE_CONFIG_DIR.",
	}
	if in.Profile.Source == SourceClaudeConfigDir {
		lines = append(lines, "# The profile below came from CLAUDE_CONFIG_DIR, which no longer applies.")
	}
	lines = append(lines,
		"# See the Configuration section of the README (or agtop --help).",
		"",
		"[profiles]",
		tomlKey(in.Profile.Label)+" = "+tomlValue(in.Profile.Dir, in.Homedir),
	)
	if len(in.Hints) > 0 {
		lines = append(lines, "", "# Other directories found; uncomment to watch them too:")
		for _, h := range in.Hints {
			lines = append(lines, "# "+tomlKey(h.Label)+" = "+tomlValue(h.Dir, in.Homedir))
		}
	}
	if in.Opencode {
		lines = append(lines, "", "# opencode data found; uncomment to watch it too:",
			"# "+tomlKey("oc")+` = { provider = "opencode" }`)
	}
	return strings.Join(lines, "\n") + "\n"
}

func tomlKey(label string) string {
	if bareKey.MatchString(label) {
		return label
	}
	return quote(label)
}

// tomlValue is ~/... when dir is under homedir, else the absolute path; always a quoted string.
func tomlValue(dir, homedir string) string {
	rel, err := filepath.Rel(homedir, dir)
	under := err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
	if under {
		return quote("~/" + rel)
	}
	return quote(dir)
}

// quote renders a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// InitResult is the outcome of CreateConfigFileIfMissing.
type InitResult int

const (
	Created InitResult = iota
	Exists
)

// CreateConfigFileIfMissing writes text to file (directory 0700, file 0600) unless it already
// exists; it never overwrites. Any failure other than "already exists" is returned as an error:
// the viewer works without the file.
func CreateConfigFileIfMissing(fsys FS, file, text string) (InitResult, error) {
	if err := fsys.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return 0, err
	}
	err := fsys.CreateExcl(file, text, 0o600)
	switch {
	case err == nil:
		return Created, nil
	case errors.Is(err, fs.ErrExist):
		if fsys.IsRegularFile(file) {
			return Exists, nil
		}
		return 0, errors.New("the path exists but is not a regular file")
	}
	return 0, err
}

// InitConfigFile handles the first run without a config file: it writes one with the directory
// being watched (plus commented hints for sibling profiles) and says so on stderr. Failures
// only warn: the viewer keeps going.
func InitConfigFile(fsys FS, env Env, configPath string, profiles []ProfileDir, homedir string, stderr io.Writer) {
	if len(profiles) == 0 {
		return
	}
	profile := profiles[0]
	hints := FindSiblingProfiles(homedir, profile.Dir, fsys)
	opencode := fsys.IsRegularFile(filepath.Join(DefaultOpencodeDir(env, homedir), OpencodeDBName))
	text := RenderInitialConfig(InitialConfigInput{Profile: profile, Hints: hints, Opencode: opencode, Homedir: homedir})
	result, err := CreateConfigFileIfMissing(fsys, configPath, text)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "agtop: warning: could not create %s: %v\n", configPath, err)
	case result == Created && profile.Source == SourceClaudeConfigDir:
		fmt.Fprintf(stderr, "agtop: created %s with 1 profile (CLAUDE_CONFIG_DIR=%s); "+
			"CLAUDE_CONFIG_DIR no longer applies, edit the file to watch other directories (see --help)\n", configPath, profile.Dir)
	case result == Created:
		fmt.Fprintf(stderr, "agtop: created %s with 1 profile; edit it to watch other directories (see --help)\n", configPath)
	}
}
