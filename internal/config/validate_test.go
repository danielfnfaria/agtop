package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateClaudeDir(t *testing.T) {
	t.Run("ok with projects/ and sessions/", func(t *testing.T) {
		r := ValidateClaudeDir("/c", newFakeFS([]string{"/c/projects", "/c/sessions"}, nil, nil))
		if r.Error != "" || len(r.Warnings) != 0 {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("error without projects/", func(t *testing.T) {
		r := ValidateClaudeDir("/c", newFakeFS([]string{"/c/sessions"}, nil, nil))
		if r.Error != "/c does not look like a Claude Code config directory: missing projects/" {
			t.Errorf("got %q", r.Error)
		}
	})
	t.Run("error when the directory does not exist", func(t *testing.T) {
		r := ValidateClaudeDir("/nope", newFakeFS(nil, nil, nil))
		if r.Error != "/nope does not exist or is not a directory" {
			t.Errorf("got %q", r.Error)
		}
	})
	t.Run("warning (no error) without sessions/", func(t *testing.T) {
		r := ValidateClaudeDir("/c", newFakeFS([]string{"/c/projects"}, nil, nil))
		if r.Error != "" || len(r.Warnings) != 1 ||
			!strings.Contains(r.Warnings[0], "sessions/") || !strings.Contains(r.Warnings[0], "recent Claude Code versions") {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("real fs tells error, warning and ok apart", func(t *testing.T) {
		dir := t.TempDir()
		if r := ValidateClaudeDir(dir, OSFS{}); !strings.Contains(r.Error, "missing projects/") {
			t.Errorf("got %+v", r)
		}
		mustMkdir(t, filepath.Join(dir, "projects"))
		if r := ValidateClaudeDir(dir, OSFS{}); r.Error != "" || len(r.Warnings) != 1 {
			t.Errorf("got %+v", r)
		}
		mustMkdir(t, filepath.Join(dir, "sessions"))
		if r := ValidateClaudeDir(dir, OSFS{}); r.Error != "" || len(r.Warnings) != 0 {
			t.Errorf("got %+v", r)
		}
	})
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
