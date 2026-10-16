package discovery

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

func usageFixture() map[string]any {
	return map[string]any{
		"version": 1, "updatedAt": 1_760_000_000_000,
		"fiveHour": map[string]any{"percent": 79, "resetsAt": 1_760_000_900_000},
		"sevenDay": map[string]any{"percent": 23},
	}
}

func with(base map[string]any, k string, v any) map[string]any {
	m := map[string]any{}
	for key, val := range base {
		m[key] = val
	}
	m[k] = v
	return m
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadUsageFile(t *testing.T) {
	file := func(dir string) string { return filepath.Join(dir, "agtop", "usage.json") }

	t.Run("valid file: the usage and its signature", func(t *testing.T) {
		dir := makeTmp(t)
		writeJSON(t, file(dir), usageFixture())
		r := ReadUsageFile(OSFS{}, dir)
		if r.Kind != ReadOK || r.Sig == "" || r.Value.FiveHour == nil || r.Value.FiveHour.Percent != 79 ||
			r.Value.SevenDay == nil || r.Value.SevenDay.Percent != 23 {
			t.Fatalf("r = %+v", r)
		}
		if sig, ok := UsageSignature(OSFS{}, dir); !ok || sig != r.Sig {
			t.Errorf("signature %q, %v", sig, ok)
		}
	})

	t.Run("no agtop/ or no file: missing", func(t *testing.T) {
		dir := makeTmp(t)
		if r := ReadUsageFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("r = %+v", r)
		}
		mkdir(t, filepath.Join(dir, "agtop"))
		if r := ReadUsageFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("r = %+v", r)
		}
		if _, ok := UsageSignature(OSFS{}, dir); ok {
			t.Error("want no signature")
		}
	})

	t.Run("invalid content: a short code, never the content", func(t *testing.T) {
		dir := makeTmp(t)
		tests := []struct{ content, code string }{
			{`{"version":1,"SECRET`, "INVALID_JSON"},
			{jsonOf(t, with(usageFixture(), "cwd", "SECRET")), "INVALID"},
			{jsonOf(t, with(usageFixture(), "fiveHour", map[string]any{"percent": 150})), "INVALID"},
			{jsonOf(t, []string{"SECRET"}), "INVALID"},
			{jsonOf(t, with(usageFixture(), "pad", strings.Repeat("x", domain.MaxUsageFileBytes))), "TOO_LARGE"},
		}
		for _, tt := range tests {
			writeFile(t, file(dir), tt.content)
			r := ReadUsageFile(OSFS{}, dir)
			if r.Kind != ReadError || r.Code != tt.code || strings.Contains(jsonOf(t, r.Code), "SECRET") {
				t.Errorf("%.30s: r = %+v, want %s", tt.content, r, tt.code)
			}
		}
	})

	t.Run("a symlink that leaves the profile directory is not followed", func(t *testing.T) {
		root := makeTmp(t)
		dir := filepath.Join(root, "profile")
		outside := filepath.Join(root, "outside")
		writeJSON(t, filepath.Join(outside, "usage.json"), usageFixture())
		mkdir(t, dir)
		// The whole agtop/ directory pointing outside, then just the file.
		symlink(t, outside, filepath.Join(dir, "agtop"))
		if r := ReadUsageFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("dir link: r = %+v", r)
		}
		removeAll(t, filepath.Join(dir, "agtop"))
		mkdir(t, filepath.Join(dir, "agtop"))
		symlink(t, filepath.Join(outside, "usage.json"), file(dir))
		if r := ReadUsageFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("file link: r = %+v", r)
		}
		if _, ok := UsageSignature(OSFS{}, dir); ok {
			t.Error("want no signature")
		}
	})

	t.Run("a symlink inside the profile directory is fine", func(t *testing.T) {
		dir := makeTmp(t)
		writeJSON(t, filepath.Join(dir, "elsewhere", "u.json"), usageFixture())
		mkdir(t, filepath.Join(dir, "agtop"))
		symlink(t, filepath.Join(dir, "elsewhere", "u.json"), file(dir))
		if r := ReadUsageFile(OSFS{}, dir); r.Kind != ReadOK {
			t.Errorf("r = %+v", r)
		}
	})
}

func TestReadContextFile(t *testing.T) {
	file := func(dir string) string { return filepath.Join(dir, "agtop", "context.json") }
	fixture := map[string]any{
		"version": 1, "updatedAt": 1_760_000_000_000,
		"sessions": map[string]any{"s1": map[string]any{"model": "claude-opus-5-5", "size": 1_000_000, "at": 1_760_000_000_000}},
	}

	t.Run("valid file: the sessions and its signature", func(t *testing.T) {
		dir := makeTmp(t)
		writeJSON(t, file(dir), fixture)
		r := ReadContextFile(OSFS{}, dir)
		if r.Kind != ReadOK || r.Value.Sessions["s1"].Size != 1_000_000 {
			t.Fatalf("r = %+v", r)
		}
		if sig, _ := ContextSignature(OSFS{}, dir); sig != r.Sig {
			t.Errorf("signature %q, want %q", sig, r.Sig)
		}
	})

	t.Run("missing, and independent of usage.json", func(t *testing.T) {
		dir := makeTmp(t)
		writeJSON(t, filepath.Join(dir, "agtop", "usage.json"), usageFixture())
		if r := ReadContextFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("r = %+v", r)
		}
		if _, ok := ContextSignature(OSFS{}, dir); ok {
			t.Error("want no signature")
		}
	})

	t.Run("invalid content: a short code, never the content", func(t *testing.T) {
		dir := makeTmp(t)
		tests := []struct{ content, code string }{
			{`{"version":1,"SECRET`, "INVALID_JSON"},
			{jsonOf(t, with(fixture, "cwd", "SECRET")), "INVALID"},
			{jsonOf(t, with(fixture, "pad", strings.Repeat("x", domain.MaxContextFileBytes))), "TOO_LARGE"},
		}
		for _, tt := range tests {
			writeFile(t, file(dir), tt.content)
			r := ReadContextFile(OSFS{}, dir)
			if r.Kind != ReadError || r.Code != tt.code {
				t.Errorf("%.30s: r = %+v, want %s", tt.content, r, tt.code)
			}
		}
	})

	t.Run("a symlink that leaves the profile directory is not followed", func(t *testing.T) {
		root := makeTmp(t)
		dir := filepath.Join(root, "profile")
		writeJSON(t, filepath.Join(root, "outside", "context.json"), fixture)
		mkdir(t, filepath.Join(dir, "agtop"))
		symlink(t, filepath.Join(root, "outside", "context.json"), file(dir))
		if r := ReadContextFile(OSFS{}, dir); r.Kind != ReadMissing {
			t.Errorf("r = %+v", r)
		}
	})
}
