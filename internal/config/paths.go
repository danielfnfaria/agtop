package config

import (
	"path/filepath"
	"strings"
)

const (
	// EnvXDGConfigHome and EnvClaudeConfigDir are the environment variables the config reads.
	EnvXDGConfigHome   = "XDG_CONFIG_HOME"
	EnvClaudeConfigDir = "CLAUDE_CONFIG_DIR"
)

// DefaultConfigPath is $XDG_CONFIG_HOME/agtop/config.toml, else <homedir>/.config/agtop/config.toml
// (also on macOS). A relative XDG_CONFIG_HOME is invalid per the spec and ignored.
func DefaultConfigPath(env Env, homedir string) string {
	base := filepath.Join(homedir, ".config")
	if xdg := strings.TrimSpace(env[EnvXDGConfigHome]); xdg != "" && filepath.IsAbs(xdg) {
		base = xdg
	}
	return filepath.Join(base, "agtop", "config.toml")
}

// ExpandHome expands a leading ~ (alone or followed by a separator).
func ExpandHome(p, homedir string) string {
	if p == "~" {
		return homedir
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homedir, p[2:])
	}
	return p
}

// DefaultLabel turns .claude-work into claude-work and .claude into claude.
func DefaultLabel(dir string) string {
	base := strings.TrimLeft(filepath.Base(dir), ".")
	if base == "" {
		return dir
	}
	return base
}

// resolvePath makes p absolute against base and cleans it.
func resolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}
