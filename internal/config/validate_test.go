package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
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

func TestValidateOpencodeDir(t *testing.T) {
	t.Run("ok with opencode.db", func(t *testing.T) {
		r := ValidateOpencodeDir("/oc", newFakeFS([]string{"/oc"}, nil, map[string]string{"/oc/opencode.db": ""}))
		if r.Error != "" || len(r.Warnings) != 0 {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("error without opencode.db", func(t *testing.T) {
		r := ValidateOpencodeDir("/oc", newFakeFS([]string{"/oc"}, nil, nil))
		if r.Error != "/oc does not look like an opencode data directory: missing opencode.db" {
			t.Errorf("got %q", r.Error)
		}
	})
	t.Run("a directory named opencode.db is not the database", func(t *testing.T) {
		if r := ValidateOpencodeDir("/oc", newFakeFS([]string{"/oc/opencode.db"}, nil, nil)); r.Error == "" {
			t.Error("accepted")
		}
	})
	t.Run("error when the directory does not exist", func(t *testing.T) {
		r := ValidateOpencodeDir("/nope", newFakeFS(nil, nil, nil))
		if r.Error != "/nope does not exist or is not a directory" {
			t.Errorf("got %q", r.Error)
		}
	})
}

func TestValidateProfileByProvider(t *testing.T) {
	fake := newFakeFS([]string{"/c/projects", "/oc"}, nil, map[string]string{"/oc/opencode.db": ""})
	if r := ValidateProfile(ProfileDir{Dir: "/c"}, fake); r.Error != "" {
		t.Errorf("claude: %q", r.Error)
	}
	if r := ValidateProfile(ProfileDir{Dir: "/oc", Provider: domain.Opencode}, fake); r.Error != "" {
		t.Errorf("opencode: %q", r.Error)
	}
	if r := ValidateProfile(ProfileDir{Dir: "/c", Provider: domain.Opencode}, fake); r.Error == "" {
		t.Error("a claude dir passed as opencode")
	}
}
