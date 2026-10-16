package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOSFSIsDirectory(t *testing.T) {
	t.Run("false (no panic) for a path under a file (ENOTDIR)", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "arquivo")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if (OSFS{}).IsDirectory(filepath.Join(file, "x")) {
			t.Error("want false")
		}
		target := filepath.Join(file, "x")
		if got := ValidateClaudeDir(target, OSFS{}).Error; got != target+" does not exist or is not a directory" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("false for an entry in an unreadable directory (EACCES)", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores permissions")
		}
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, "locked", "projects"))
		if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })
		if (OSFS{}).IsDirectory(filepath.Join(dir, "locked", "projects")) {
			t.Error("want false")
		}
	})
}

func TestOSFSRealpath(t *testing.T) {
	t.Run("resolves symlinks", func(t *testing.T) {
		dir, _ := filepath.EvalSymlinks(t.TempDir())
		mustMkdir(t, filepath.Join(dir, "real"))
		if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
			t.Skip("no symlinks")
		}
		got, err := (OSFS{}).Realpath(filepath.Join(dir, "link"))
		if err != nil || got != filepath.Join(dir, "real") {
			t.Errorf("got %q, %v", got, err)
		}
		if _, err := (OSFS{}).Realpath(filepath.Join(dir, "nope")); err == nil {
			t.Error("missing path: want error")
		}
	})

	t.Run("on a case-insensitive fs, .claude and .CLAUDE in two labels are the same directory", func(t *testing.T) {
		h := t.TempDir()
		mustMkdir(t, filepath.Join(h, ".claude", "projects"))
		if _, err := os.Stat(filepath.Join(h, ".CLAUDE")); err != nil {
			t.Skip("case-sensitive filesystem")
		}
		a, _ := (OSFS{}).Realpath(filepath.Join(h, ".CLAUDE"))
		b, _ := (OSFS{}).Realpath(filepath.Join(h, ".claude"))
		if a != b {
			t.Errorf("%q != %q", a, b)
		}
		_, err := ResolveProfiles(ResolveInput{
			Profiles: []ProfileDir{prof("a", filepath.Join(h, ".claude")), prof("b", filepath.Join(h, ".CLAUDE"))},
			Env:      Env{}, Homedir: h, FS: OSFS{}, Cwd: h, ConfigPath: filepath.Join(h, "config.toml"),
		})
		if err == nil || !strings.Contains(err.Error(), "point to the same directory") {
			t.Errorf("got %v", err)
		}
	})

	t.Run("a trailing separator resolves to the same directory", func(t *testing.T) {
		d := t.TempDir()
		a, _ := (OSFS{}).Realpath(d + "/")
		b, _ := (OSFS{}).Realpath(d)
		if a != b || a == "" {
			t.Errorf("%q != %q", a, b)
		}
	})
}

func TestOSFSReadFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.toml")
	if err := os.WriteFile(file, []byte("x = 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, text string
		found            bool
	}{
		{"reads the file", file, "x = 1", true},
		{"missing", filepath.Join(dir, "nope"), "", false},
		{"directory", dir, "", false},
		{"path under a file", filepath.Join(file, "x"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, found, err := (OSFS{}).ReadFile(tt.path)
			if err != nil || found != tt.found || text != tt.text {
				t.Errorf("got %q, %v, %v", text, found, err)
			}
		})
	}

	t.Run("EACCES is an error", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores permissions")
		}
		locked := filepath.Join(dir, "locked.toml")
		if err := os.WriteFile(locked, []byte("x"), 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := (OSFS{}).ReadFile(locked); err == nil {
			t.Error("want error")
		}
	})
}
