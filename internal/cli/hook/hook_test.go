package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

const (
	now     int64 = 1792065600000 // 2026-10-15T12:00:00Z
	resetMs int64 = 1792085400000 // 2026-10-15T17:30:00Z
	sevenMs int64 = 1792486800000 // 2026-10-20T09:00:00Z
)

// payload is a status line payload with secrets everywhere but the two windows.
func payload(over map[string]any) map[string]any {
	p := map[string]any{
		"session_id":      "SECRET-session",
		"transcript_path": "/SECRET/transcript.jsonl",
		"cwd":             "/SECRET/cwd",
		"model":           map[string]any{"id": "SECRET-model", "display_name": "SECRET"},
		"workspace":       map[string]any{"current_dir": "/SECRET/dir"},
		"cost":            map[string]any{"total_cost_usd": 1.23},
		"rate_limits": map[string]any{
			"five_hour": map[string]any{"used_percentage": 79, "resets_at": resetMs / 1000},
			"seven_day": map[string]any{"used_percentage": 23, "resets_at": "2026-10-20T09:00:00Z"},
		},
	}
	for k, v := range over {
		if v == nil {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	return p
}

// ctxPayload is a payload with a context window (and no rate limits).
func ctxPayload(over map[string]any) map[string]any {
	p := map[string]any{
		"session_id":     "sess-1",
		"model":          map[string]any{"id": "claude-opus-5-5[1m]", "display_name": "SECRET"},
		"context_window": map[string]any{"context_window_size": 1_000_000, "used_percentage": 42},
		"rate_limits":    nil,
	}
	for k, v := range over {
		p[k] = v
	}
	return payload(p)
}

func enc(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

type runOpts struct {
	configDir string
	home      string
	now       int64
	stdout    io.Writer
	debug     bool
	print     bool
}

type result struct {
	code   int
	stdout []byte
	stderr string
}

func run(t *testing.T, input []byte, o runOpts) result {
	t.Helper()
	var out, errb bytes.Buffer
	var stdout io.Writer = &out
	if o.stdout != nil {
		stdout = o.stdout
	}
	if o.home == "" {
		o.home = t.TempDir()
	}
	if o.now == 0 {
		o.now = now
	}
	code := Run(IO{
		Stdin: bytes.NewReader(input), Stdout: stdout, Stderr: &errb,
		ClaudeConfigDir: o.configDir, Homedir: o.home, Debug: o.debug,
		NowMs: func() int64 { return o.now },
	}, o.print)
	return result{code, out.Bytes(), errb.String()}
}

func usageFile(dir string) string   { return filepath.Join(dir, "agtop", "usage.json") }
func contextFile(dir string) string { return filepath.Join(dir, "agtop", "context.json") }

func readJSON(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
}

func TestPassthrough(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input []byte
	}{
		{"JSON payload", enc(t, payload(nil))},
		{"empty stdin", nil},
		{"not JSON", []byte("hello {not json\n")},
		{"binary and invalid UTF-8", []byte{0x00, 0xff, 0xfe, 0x1b, 0x5b, 0x0a, 0xc3}},
		{"JSON without rate limits", []byte("{\"model\":{\"id\":\"x\"}}\n")},
		{"JSON array", []byte("[1,2,3]")},
	} {
		t.Run(tt.name+": stdout is stdin byte for byte, exit 0", func(t *testing.T) {
			dir := t.TempDir()
			r := run(t, tt.input, runOpts{configDir: dir})
			if r.code != 0 || !bytes.Equal(r.stdout, tt.input) || r.stderr != "" {
				t.Errorf("%+v", r)
			}
		})
	}

	t.Run("large input passes through unchanged and is not parsed above the cap", func(t *testing.T) {
		dir := t.TempDir()
		big := enc(t, payload(map[string]any{"pad": strings.Repeat("x", 300_000)}))
		r := run(t, big, runOpts{configDir: dir})
		if !bytes.Equal(r.stdout, big) || !exists(usageFile(dir)) {
			t.Error("300 KB payload: passthrough or usage failed")
		}
		huge := enc(t, payload(map[string]any{"pad": strings.Repeat("x", MaxPayloadBytes)}))
		dir2 := t.TempDir()
		r = run(t, huge, runOpts{configDir: dir2})
		if r.code != 0 || !bytes.Equal(r.stdout, huge) || exists(filepath.Join(dir2, "agtop")) {
			t.Error("payload over the cap: must pass through unparsed")
		}
	})

	t.Run("a stdout write error still exits 0 and records the usage", func(t *testing.T) {
		dir := t.TempDir()
		r := run(t, enc(t, payload(nil)), runOpts{configDir: dir, stdout: failWriter{syscall.EPIPE}})
		if r.code != 0 || !exists(usageFile(dir)) {
			t.Errorf("%+v", r)
		}
	})
}

func TestUsageFile(t *testing.T) {
	t.Run("only the allowed fields; nothing else of the payload", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, payload(nil)), runOpts{configDir: dir})
		raw, _ := os.ReadFile(usageFile(dir))
		if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "1.23") {
			t.Errorf("leaked: %s", raw)
		}
		want := map[string]any{
			"version": 1.0, "updatedAt": float64(now),
			"fiveHour": map[string]any{"percent": 79.0, "resetsAt": float64(resetMs)},
			"sevenDay": map[string]any{"percent": 23.0, "resetsAt": float64(sevenMs)},
		}
		if got := readJSON(t, usageFile(dir)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
		if _, ok := domain.UnmarshalUsageFile(raw); !ok {
			t.Error("not accepted by the viewer's parser")
		}
		if got := names(t, filepath.Join(dir, "agtop")); !slices.Equal(got, []string{"usage.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	for _, tt := range []struct {
		name     string
		resetsAt any
	}{
		{"seconds", resetMs / 1000},
		{"milliseconds", resetMs},
		{"ISO 8601", "2026-10-15T17:30:00.000Z"},
		{"numeric string", "1792085400"},
	} {
		t.Run("resets_at in "+tt.name+" is stored as epoch ms", func(t *testing.T) {
			dir := t.TempDir()
			rl := map[string]any{"five_hour": map[string]any{"used_percentage": 5, "resets_at": tt.resetsAt}}
			run(t, enc(t, payload(map[string]any{"rate_limits": rl})), runOpts{configDir: dir})
			f := readJSON(t, usageFile(dir))
			if !reflect.DeepEqual(f["fiveHour"], map[string]any{"percent": 5.0, "resetsAt": float64(resetMs)}) || f["sevenDay"] != nil {
				t.Errorf("got %v", f)
			}
		})
	}

	t.Run("no rate limits in the payload: no file at all", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, payload(map[string]any{"rate_limits": nil})), runOpts{configDir: dir})
		if exists(filepath.Join(dir, "agtop")) {
			t.Error("agtop/ created")
		}
	})

	t.Run("permissions: directory 0700, file 0600", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, payload(nil)), runOpts{configDir: dir})
		for p, want := range map[string]os.FileMode{filepath.Join(dir, "agtop"): 0o700, usageFile(dir): 0o600} {
			if st, _ := os.Stat(p); st.Mode().Perm() != want {
				t.Errorf("%s: %v, want %v", p, st.Mode().Perm(), want)
			}
		}
	})

	t.Run("atomic: the file is replaced by rename (new inode), never rewritten in place", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, payload(nil)), runOpts{configDir: dir})
		before, _ := os.Stat(usageFile(dir))
		changed := map[string]any{"five_hour": map[string]any{"used_percentage": 80}}
		run(t, enc(t, payload(map[string]any{"rate_limits": changed})), runOpts{configDir: dir, now: now + 1000})
		after, _ := os.Stat(usageFile(dir))
		if os.SameFile(before, after) {
			t.Error("same inode")
		}
		if got := names(t, filepath.Join(dir, "agtop")); !slices.Equal(got, []string{"usage.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	t.Run("concurrent runs: the file is always one complete, valid write", func(t *testing.T) {
		dir := t.TempDir()
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rl := map[string]any{"five_hour": map[string]any{"used_percentage": i}}
				run(t, enc(t, payload(map[string]any{"rate_limits": rl})), runOpts{configDir: dir, now: now + int64(i)*120_000})
			}()
		}
		wg.Wait()
		raw, _ := os.ReadFile(usageFile(dir))
		if _, ok := domain.UnmarshalUsageFile(raw); !ok {
			t.Errorf("invalid file: %s", raw)
		}
		if got := names(t, filepath.Join(dir, "agtop")); !slices.Equal(got, []string{"usage.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	t.Run("rewrites only when the numbers changed or after 60 s", func(t *testing.T) {
		dir := t.TempDir()
		body := enc(t, payload(nil))
		readAt := func() int64 { return int64(readJSON(t, usageFile(dir))["updatedAt"].(float64)) }
		run(t, body, runOpts{configDir: dir, now: now})
		for _, step := range []struct {
			at   int64
			want int64
		}{{now + 30_000, now}, {now + RewriteAfterMs, now}, {now + RewriteAfterMs + 1, now + RewriteAfterMs + 1}} {
			run(t, body, runOpts{configDir: dir, now: step.at})
			if got := readAt(); got != step.want {
				t.Fatalf("at +%d: updatedAt %d, want %d", step.at-now, got, step.want)
			}
		}
		rl := map[string]any{"five_hour": map[string]any{"used_percentage": 80, "resets_at": resetMs / 1000}}
		changed := enc(t, payload(map[string]any{"rate_limits": rl}))
		run(t, changed, runOpts{configDir: dir, now: now + RewriteAfterMs + 2})
		if got := readAt(); got != now+RewriteAfterMs+2 {
			t.Errorf("changed numbers not rewritten: %d", got)
		}
		// A broken existing file is replaced.
		if err := os.WriteFile(usageFile(dir), []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		run(t, changed, runOpts{configDir: dir, now: now + RewriteAfterMs + 3})
		if got := readAt(); got != now+RewriteAfterMs+3 {
			t.Errorf("broken file not replaced: %d", got)
		}
	})

	t.Run("exit 0 and passthrough in a directory without write permission", func(t *testing.T) {
		skipIfRoot(t)
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "agtop"), 0o500); err != nil {
			t.Fatal(err)
		}
		locked := t.TempDir()
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "agtop"), 0o700); _ = os.Chmod(locked, 0o700) })
		body := enc(t, payload(nil))
		for _, d := range []string{dir, locked} {
			r := run(t, body, runOpts{configDir: d})
			if r.code != 0 || !bytes.Equal(r.stdout, body) || r.stderr != "" {
				t.Errorf("%s: %+v", d, r)
			}
		}
		if exists(usageFile(dir)) {
			t.Error("usage file written")
		}
		// With AGTOP_DEBUG, a short code on stderr (never on stdout, never content).
		r := run(t, body, runOpts{configDir: dir, debug: true})
		if !bytes.Equal(r.stdout, body) || !regexp.MustCompile(`^agtop statusline-hook: failed: EACCES\n$`).MatchString(r.stderr) {
			t.Errorf("%+v", r)
		}
	})

	t.Run("RecordUsage returns I/O errors (the hook catches them)", func(t *testing.T) {
		skipIfRoot(t)
		locked := t.TempDir()
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if _, err := RecordUsage(locked, domain.RateLimits{FiveHour: &domain.UsageWindow{Percent: 1}}, now); err == nil {
			t.Error("want an error")
		}
	})
}

func TestContextFile(t *testing.T) {
	sessions := func(t *testing.T, dir string) map[string]any {
		return readJSON(t, contextFile(dir))["sessions"].(map[string]any)
	}

	t.Run("records the session, the model and the size; nothing else", func(t *testing.T) {
		dir := t.TempDir()
		body := enc(t, ctxPayload(nil))
		r := run(t, body, runOpts{configDir: dir})
		if r.code != 0 || !bytes.Equal(r.stdout, body) {
			t.Errorf("%+v", r)
		}
		raw, _ := os.ReadFile(contextFile(dir))
		if strings.Contains(string(raw), "SECRET") {
			t.Errorf("leaked: %s", raw)
		}
		want := map[string]any{"version": 1.0, "updatedAt": float64(now), "sessions": map[string]any{
			"sess-1": map[string]any{"model": "claude-opus-5-5[1m]", "size": 1_000_000.0, "at": float64(now)},
		}}
		if got := readJSON(t, contextFile(dir)); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
		if exists(usageFile(dir)) {
			t.Error("usage file created without rate limits")
		}
	})

	t.Run("usage.json still has no session or model data when both are recorded", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, ctxPayload(map[string]any{"rate_limits": payload(nil)["rate_limits"]})), runOpts{configDir: dir})
		raw, _ := os.ReadFile(usageFile(dir))
		if strings.Contains(string(raw), "sess-1") || strings.Contains(string(raw), "claude-opus") || !exists(contextFile(dir)) {
			t.Errorf("usage: %s", raw)
		}
	})

	t.Run("permissions: directory 0700, file 0600; no temporary leftovers", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, ctxPayload(nil)), runOpts{configDir: dir})
		for p, want := range map[string]os.FileMode{filepath.Join(dir, "agtop"): 0o700, contextFile(dir): 0o600} {
			if st, _ := os.Stat(p); st.Mode().Perm() != want {
				t.Errorf("%s: %v, want %v", p, st.Mode().Perm(), want)
			}
		}
		if got := names(t, filepath.Join(dir, "agtop")); !slices.Equal(got, []string{"context.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	t.Run("no context_window (or an invalid one): nothing written", func(t *testing.T) {
		dir := t.TempDir()
		for _, over := range []map[string]any{
			{"context_window": nil},
			{"context_window": map[string]any{"context_window_size": 5}},
			{"session_id": "../../x"},
		} {
			run(t, enc(t, ctxPayload(over)), runOpts{configDir: dir})
		}
		if exists(filepath.Join(dir, "agtop")) {
			t.Error("agtop/ created")
		}
	})

	t.Run("skips the write when the entry is unchanged and recent; rewrites when it changes", func(t *testing.T) {
		dir := t.TempDir()
		body := enc(t, ctxPayload(nil))
		run(t, body, runOpts{configDir: dir, now: now})
		before, _ := os.Stat(contextFile(dir))
		run(t, body, runOpts{configDir: dir, now: now + domain.ContextRewriteAfterMs - 1})
		after, _ := os.Stat(contextFile(dir))
		if !os.SameFile(before, after) || sessions(t, dir)["sess-1"].(map[string]any)["at"] != float64(now) {
			t.Error("rewritten too early")
		}
		run(t, body, runOpts{configDir: dir, now: now + domain.ContextRewriteAfterMs})
		if sessions(t, dir)["sess-1"].(map[string]any)["at"] != float64(now+domain.ContextRewriteAfterMs) {
			t.Error("not refreshed after 10 minutes")
		}
		smaller := enc(t, ctxPayload(map[string]any{"context_window": map[string]any{"context_window_size": 200_000}}))
		run(t, smaller, runOpts{configDir: dir, now: now + domain.ContextRewriteAfterMs + 1})
		if sessions(t, dir)["sess-1"].(map[string]any)["size"] != 200_000.0 {
			t.Error("changed size not recorded")
		}
	})

	t.Run("keeps other sessions and replaces a broken file", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, ctxPayload(map[string]any{"session_id": "a"})), runOpts{configDir: dir, now: now})
		run(t, enc(t, ctxPayload(map[string]any{"session_id": "b"})), runOpts{configDir: dir, now: now + 1})
		keys := func() []string {
			var k []string
			for id := range sessions(t, dir) {
				k = append(k, id)
			}
			slices.Sort(k)
			return k
		}
		if got := keys(); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("got %v", got)
		}
		if err := os.WriteFile(contextFile(dir), []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		run(t, enc(t, ctxPayload(map[string]any{"session_id": "c"})), runOpts{configDir: dir, now: now + 2})
		if got := keys(); !slices.Equal(got, []string{"c"}) {
			t.Errorf("got %v", got)
		}
	})

	for _, tt := range []struct {
		name    string
		recent  int
		withOld bool
	}{
		{"drops entries older than 30 days", 63, true},
		{"keeps at most 64 sessions, the oldest go first", 64, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			seed := map[string]any{}
			id := func(i int) string { return "s" + string(rune('A'+i/26)) + string(rune('a'+i%26)) }
			for i := range tt.recent {
				seed[id(i)] = map[string]any{"model": "m", "size": 200_000, "at": now - int64(i+1)*1000}
			}
			if tt.withOld {
				seed["old"] = map[string]any{"model": "m", "size": 200_000, "at": now - domain.ContextEntryMaxAgeMs - 1}
			}
			if err := os.MkdirAll(filepath.Join(dir, "agtop"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(contextFile(dir), enc(t, map[string]any{"version": 1, "updatedAt": now - 1, "sessions": seed}), 0o600); err != nil {
				t.Fatal(err)
			}
			run(t, enc(t, ctxPayload(map[string]any{"session_id": "new"})), runOpts{configDir: dir})
			got := sessions(t, dir)
			_, hasOld := got["old"]
			_, hasOldest := got[id(63)]
			_, hasNewest := got[id(0)]
			if len(got) != 64 || got["new"] == nil || hasOld || !hasNewest || hasOldest {
				t.Errorf("%d sessions; old=%v newest=%v oldest=%v", len(got), hasOld, hasNewest, hasOldest)
			}
		})
	}

	t.Run("concurrent runs: the file is always one complete, valid write", func(t *testing.T) {
		dir := t.TempDir()
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				run(t, enc(t, ctxPayload(map[string]any{"session_id": "s" + string(rune('a'+i))})), runOpts{configDir: dir, now: now + int64(i)})
			}()
		}
		wg.Wait()
		raw, _ := os.ReadFile(contextFile(dir))
		if _, ok := domain.UnmarshalContextFile(raw); !ok {
			t.Errorf("invalid file: %s", raw)
		}
		if got := names(t, filepath.Join(dir, "agtop")); !slices.Equal(got, []string{"context.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	t.Run("exit 0 and identical stdout in a read-only directory; error code only with AGTOP_DEBUG", func(t *testing.T) {
		skipIfRoot(t)
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "agtop"), 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "agtop"), 0o700) })
		body := enc(t, ctxPayload(nil))
		quiet := run(t, body, runOpts{configDir: dir})
		if quiet.code != 0 || !bytes.Equal(quiet.stdout, body) || quiet.stderr != "" {
			t.Errorf("%+v", quiet)
		}
		debug := run(t, body, runOpts{configDir: dir, debug: true})
		if !bytes.Equal(debug.stdout, body) || debug.stderr != "agtop statusline-hook: context failed: EACCES\n" {
			t.Errorf("%+v", debug)
		}
	})

	t.Run("a failure on one file does not stop the other", func(t *testing.T) {
		skipIfRoot(t)
		dir := t.TempDir()
		// A directory where usage.json should go: its rename fails, context.json is still written.
		if err := os.MkdirAll(filepath.Join(usageFile(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(usageFile(dir), "x"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r := run(t, enc(t, ctxPayload(map[string]any{"rate_limits": payload(nil)["rate_limits"]})), runOpts{configDir: dir})
		if r.code != 0 || !exists(contextFile(dir)) {
			t.Errorf("%+v", r)
		}
	})
}

func TestConfigDir(t *testing.T) {
	abs, _ := filepath.Abs("/opt/work-claude")
	for _, tt := range []struct{ name, env, want string }{
		{"unset: ~/.claude", "", filepath.Join("/home/u", ".claude")},
		{"blank: ~/.claude", "  ", filepath.Join("/home/u", ".claude")},
		{"absolute", "/opt/work-claude", abs},
		{"tilde expanded", "~/.claude-work", filepath.Join("/home/u", ".claude-work")},
		{"bare tilde", "~", "/home/u"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigDir(tt.env, "/home/u"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("without CLAUDE_CONFIG_DIR the hook writes under <home>/.claude", func(t *testing.T) {
		home := t.TempDir()
		run(t, enc(t, payload(nil)), runOpts{home: home})
		if !exists(usageFile(filepath.Join(home, ".claude"))) {
			t.Error("usage file not under <home>/.claude")
		}
	})
}

func TestPrint(t *testing.T) {
	rl := func(five, seven any) map[string]any {
		m := map[string]any{}
		if five != nil {
			m["five_hour"] = map[string]any{"used_percentage": five, "resets_at": resetMs / 1000}
		}
		if seven != nil {
			m["seven_day"] = map[string]any{"used_percentage": seven}
		}
		return map[string]any{"rate_limits": m}
	}
	for _, tt := range []struct {
		name  string
		input []byte
		want  string
	}{
		{"both windows, rounded", enc(t, payload(rl(6.4, 29.6))), "agtop · 5h 6% · 7d 30%\n"},
		{"one window only", enc(t, payload(map[string]any{"rate_limits": map[string]any{"seven_day": map[string]any{"used_percentage": 3}}})), "agtop · 7d 3%\n"},
		{"no rate limits", enc(t, payload(map[string]any{"rate_limits": nil})), "agtop\n"},
		{"not JSON", []byte("oops {"), "agtop\n"},
		{"empty", nil, "agtop\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, tt.input, runOpts{configDir: t.TempDir(), print: true})
			if r.code != 0 || string(r.stdout) != tt.want {
				t.Errorf("%+v", r)
			}
		})
	}

	t.Run("prints the short line instead of the payload and still records the usage", func(t *testing.T) {
		dir := t.TempDir()
		run(t, enc(t, payload(rl(6.4, 29.6))), runOpts{configDir: dir, print: true})
		if !exists(usageFile(dir)) {
			t.Error("usage not recorded")
		}
	})

	t.Run("exit 0 and the line even when the usage file cannot be written", func(t *testing.T) {
		skipIfRoot(t)
		locked := t.TempDir()
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		r := run(t, enc(t, payload(nil)), runOpts{configDir: locked, print: true})
		if r.code != 0 || string(r.stdout) != "agtop · 5h 79% · 7d 23%\n" {
			t.Errorf("%+v", r)
		}
	})

	t.Run("a stdout write error still exits 0", func(t *testing.T) {
		r := run(t, enc(t, payload(nil)), runOpts{configDir: t.TempDir(), print: true, stdout: failWriter{errors.New("boom")}})
		if r.code != 0 {
			t.Errorf("%+v", r)
		}
	})
}
