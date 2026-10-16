package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
)

// makeTmp is a canonical temporary directory (the macOS /var symlink resolved), matching what
// the code under test gets from Realpath.
func makeTmp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, string(b))
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func makeProfile(t *testing.T, root, label string) config.ProfileDir {
	t.Helper()
	dir := filepath.Join(root, label)
	mkdir(t, filepath.Join(dir, "projects"))
	return config.ProfileDir{Label: label, Dir: dir, Source: config.SourceConfig}
}

// call is one filesystem operation seen by recordingFS.
type call struct{ op, path string }

// recordingFS is an OSFS that records every path it touches.
type recordingFS struct {
	mu    sync.Mutex
	calls []call
}

func (r *recordingFS) rec(op, p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{op, p})
}

func (r *recordingFS) ReadDir(p string) ([]string, error) {
	r.rec("readdir", p)
	return OSFS{}.ReadDir(p)
}
func (r *recordingFS) Stat(p string) (Info, error) { r.rec("stat", p); return OSFS{}.Stat(p) }
func (r *recordingFS) Realpath(p string) (string, error) {
	r.rec("realpath", p)
	return OSFS{}.Realpath(p)
}
func (r *recordingFS) ReadFile(p string, max int) (string, error) {
	r.rec("readFile", p)
	return OSFS{}.ReadFile(p, max)
}

func (r *recordingFS) count(op, path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.op == op && c.path == path {
			n++
		}
	}
	return n
}

func (r *recordingFS) touchedUnder(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c.path, prefix) {
			n++
		}
	}
	return n
}

func (r *recordingFS) reads() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []call
	for _, c := range r.calls {
		if c.op == "readFile" {
			out = append(out, c)
		}
	}
	return out
}

func (r *recordingFS) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

// registryEntry is a minimal, synthetic sessions/<pid>.json.
func registryEntry(pid int, sessionID string, extra map[string]any) map[string]any {
	m := map[string]any{
		"pid": pid, "sessionId": sessionID, "cwd": "/synthetic/project",
		"startedAt": 1_700_000_000_000, "procStart": "synthetic", "version": "9.9.9",
		"kind": "interactive", "entrypoint": "cli", "name": "synthetic session",
		"nameSource": "auto", "status": "idle", "updatedAt": 1_700_000_100_000,
		"statusUpdatedAt": 1_700_000_100_000, "messagingSocketPath": "/synthetic/socket",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func removeAll(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}
