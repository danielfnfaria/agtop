package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSafeID(t *testing.T) {
	for _, ok := range []string{"a", "aaaa-1111", "A_b-9", strings.Repeat("x", 128), "console", "com0", "lpt10"} {
		if !IsSafeID(ok) {
			t.Errorf("IsSafeID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 129), "..", "a.b", "a/b", `a\b`, "C:x"} {
		if IsSafeID(bad) {
			t.Errorf("IsSafeID(%q) = true", bad)
		}
	}
	for _, name := range []string{"con", "CON", "Prn", "aux", "NUL", "com1", "COM9", "lpt1", "LpT9"} {
		if IsSafeID(name) {
			t.Errorf("reserved name %q accepted", name)
		}
	}
}

func TestIsInside(t *testing.T) {
	tests := []struct {
		name, root, candidate string
		want                  bool
	}{
		{"inside", "/a/b", "/a/b/c", true},
		{"name that only starts with ..", "/a/b", "/a/b/..c", true},
		{"equal", "/a/b", "/a/b", false},
		{"parent", "/a/b", "/a", false},
		{"escape by ..", "/a/b", "/a/b/../x", false},
		{"sibling with prefix", "/a/b", "/a/bc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInside(tt.root, tt.candidate); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveContained(t *testing.T) {
	base := makeTmp(t)
	root := filepath.Join(base, "root")
	writeFile(t, filepath.Join(root, "in", "f"), "x")
	writeFile(t, filepath.Join(base, "outside", "f"), "x")
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "in"), filepath.Join(root, "ok")); err != nil {
		t.Fatal(err)
	}
	cr, ok := NewContainmentRoot(OSFS{}, root)
	if !ok {
		t.Fatal("no root")
	}
	tests := []struct {
		name, candidate, want string
	}{
		{"plain", filepath.Join(root, "in", "f"), filepath.Join(root, "in", "f")},
		{"symlink inside", filepath.Join(root, "ok", "f"), filepath.Join(root, "in", "f")},
		{"symlink escape", filepath.Join(root, "escape", "f"), ""},
		{"lexically outside", filepath.Join(base, "outside", "f"), ""},
		{"missing", filepath.Join(root, "nope"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveContained(OSFS{}, cr, tt.candidate)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("got %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
}
