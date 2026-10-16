package config

import (
	"path/filepath"
	"strings"
)

// ResolveInput is the input of ResolveProfiles.
type ResolveInput struct {
	// Profiles are the [profiles] of the config file; empty uses the fallback.
	Profiles []ProfileDir
	Env      Env
	Homedir  string
	FS       FS
	// Cwd is the base for a relative CLAUDE_CONFIG_DIR; defaults to Homedir.
	Cwd string
	// ConfigPath is the default config file path, quoted in the error message.
	ConfigPath string
}

// ResolveProfiles resolves the directories to watch: the file's [profiles] (label = table
// key, in file order), else CLAUDE_CONFIG_DIR, else <homedir>/.claude. There is no automatic
// search for other profiles. Two labels of the same provider with the same realpath are an error. In the fallback a
// missing directory is an error suggesting [profiles]; for file profiles validation reports it
// later.
func ResolveProfiles(in ResolveInput) ([]ProfileDir, error) {
	if len(in.Profiles) > 0 {
		return rejectDuplicates(in.Profiles, in.FS)
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd = in.Homedir
	}
	var candidate ProfileDir
	value := strings.TrimSpace(in.Env[EnvClaudeConfigDir])
	if value != "" {
		dir := resolvePath(cwd, ExpandHome(value, in.Homedir))
		candidate = ProfileDir{Label: DefaultLabel(dir), Dir: dir, Source: SourceClaudeConfigDir}
	} else {
		dir := filepath.Join(in.Homedir, ".claude")
		candidate = ProfileDir{Label: DefaultLabel(dir), Dir: dir, Source: SourceDefault}
	}
	if !in.FS.IsDirectory(candidate.Dir) {
		where := candidate.Dir
		if value != "" {
			where = EnvClaudeConfigDir + "=" + candidate.Dir
		}
		return nil, errorf("no Claude Code config directory found at %s. List the directories under [profiles] in %s", where, in.ConfigPath)
	}
	return []ProfileDir{candidate}, nil
}

func rejectDuplicates(profiles []ProfileDir, fsys FS) ([]ProfileDir, error) {
	seen := make(map[string]ProfileDir, len(profiles))
	for _, p := range profiles {
		dir, err := fsys.Realpath(p.Dir)
		if err != nil {
			dir = p.Dir // missing: validation reports it later
		}
		key := string(p.Provider) + "\x00" + dir
		if other, ok := seen[key]; ok {
			return nil, errorf("profiles \"%s\" and \"%s\" point to the same directory (%s)", other.Label, p.Label, p.Dir)
		}
		seen[key] = p
	}
	return append([]ProfileDir(nil), profiles...), nil
}
