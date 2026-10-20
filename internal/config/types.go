package config

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// ProfileSource says where a profile directory came from.
type ProfileSource string

const (
	// SourceConfig means the directory was set in the config file.
	SourceConfig ProfileSource = "config"
	// SourceClaudeConfigDir means the directory came from CLAUDE_CONFIG_DIR.
	SourceClaudeConfigDir ProfileSource = "claude-config-dir"
	// SourceDefault means the built-in default directory.
	SourceDefault ProfileSource = "default"
)

// ProfileDir is a Claude Code config directory to watch.
type ProfileDir struct {
	// Label is the short display name (e.g. claude, claude-work).
	Label string
	// Dir is the absolute path, with ~ already expanded.
	Dir    string
	Source ProfileSource
	// Provider is the provider whose sessions live in the directory; empty means Claude Code.
	Provider domain.ProviderID
}

// ProviderOf is the provider of a profile (Claude Code when it does not say).
func ProviderOf(p ProfileDir) domain.ProviderID {
	if p.Provider == "" {
		return domain.ClaudeCode
	}
	return p.Provider
}

// FS is the subset of filesystem operations the config layer uses, injectable for tests.
type FS interface {
	// IsDirectory reports whether p exists and is a directory (following symlinks).
	IsDirectory(p string) bool
	// IsRegularFile reports whether p is a regular file or a symlink to one.
	IsRegularFile(p string) bool
	// Realpath returns the canonical path (symlinks resolved, on-disk case); it fails if p does
	// not exist.
	Realpath(p string) (string, error)
	// ReadFile returns the UTF-8 content of p; found is false when it does not exist or is not
	// a file.
	ReadFile(p string) (text string, found bool, err error)
	// ListDir returns the entry names of a directory; empty on any error.
	ListDir(p string) []string
	// MkdirAll creates a directory and its parents.
	MkdirAll(p string, perm fs.FileMode) error
	// CreateExcl creates p with data and perm; it fails with fs.ErrExist if p exists.
	CreateExcl(p, data string, perm fs.FileMode) error
}

// Env is a snapshot of the environment variables.
type Env map[string]string

// EnvFromOS reads the process environment.
func EnvFromOS() Env {
	env := Env{}
	for _, kv := range os.Environ() {
		for i := range len(kv) {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return env
}

// ValidationResult is the outcome of validating a directory.
type ValidationResult struct {
	// Error is the fatal message, empty if none.
	Error string
	// Warnings do not prevent running.
	Warnings []string
}

// Error is a problem with the user's configuration; its text is shown as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errorf(format string, args ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}
