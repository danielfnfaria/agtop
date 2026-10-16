package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
)

var testNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

type result struct {
	code           int
	stdout, stderr string
	asked          []string
}

type runOpts struct {
	Options
	tty    bool
	answer bool
	hook   string
	cwd    string
}

// run uses Yes=true unless noYes is set, like most cases of the TS suite.
func run(t *testing.T, profiles []config.ProfileDir, o runOpts) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var asked []string
	if o.hook == "" {
		o.hook = hook
	}
	if o.cwd == "" {
		o.cwd = t.TempDir()
	}
	code := Run(profiles, o.Options, IO{
		Stdout: &stdout, Stderr: &stderr, IsTTY: o.tty,
		Confirm: func(q string) bool { asked = append(asked, q); return o.answer },
		Cwd:     o.cwd, Now: func() time.Time { return testNow },
		Hook: Hook{Command: o.hook},
	})
	return result{code, stdout.String(), stderr.String(), asked}
}

func yes() runOpts { return runOpts{Options: Options{Yes: true}} }

func profile(t *testing.T, label string) config.ProfileDir {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "."+label)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return config.ProfileDir{Label: label, Dir: dir, Source: config.SourceConfig}
}

func settingsOf(p config.ProfileDir) string { return filepath.Join(p.Dir, "settings.json") }
func agtopOf(p config.ProfileDir) string    { return filepath.Join(p.Dir, "agtop") }

func read(t *testing.T, p config.ProfileDir) string {
	t.Helper()
	b, err := os.ReadFile(settingsOf(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func statusLine(t *testing.T, p config.ProfileDir) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, p)), &m); err != nil {
		t.Fatal(err)
	}
	sl, _ := m["statusLine"].(map[string]any)
	return sl
}

func backups(t *testing.T, p config.ProfileDir) []string {
	t.Helper()
	var out []string
	entries, _ := os.ReadDir(p.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "settings.json.bak-agtop-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const manual = `input=$(cat); printf '%s' "$input" | node /somewhere/dist/cli.js statusline-hook >/dev/null 2>&1; printf '%s' "$input" | bash ~/.claude/statusline.sh`

func contains(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

func TestSetup(t *testing.T) {
	t.Run("c) no settings.json: created with the print command; setup.json 0600 in a 0700 dir", func(t *testing.T) {
		p := profile(t, "claude")
		r := run(t, []config.ProfileDir{p}, yes())
		if r.code != 0 {
			t.Fatalf("%+v", r)
		}
		sl := statusLine(t, p)
		if sl["type"] != "command" || sl["command"] != hook+" --print" {
			t.Errorf("statusLine = %v", sl)
		}
		if !strings.HasSuffix(read(t, p), "}\n") {
			t.Error("no final newline")
		}
		var saved map[string]any
		b, _ := os.ReadFile(filepath.Join(agtopOf(p), "setup.json"))
		if err := json.Unmarshal(b, &saved); err != nil || saved["version"] != 1.0 || saved["original"] != nil || saved["command"] != hook+" --print" {
			t.Errorf("setup.json = %s", b)
		}
		if mode(t, agtopOf(p)) != 0o700 || mode(t, filepath.Join(agtopOf(p), "setup.json")) != 0o600 {
			t.Error("permissions")
		}
		if len(backups(t, p)) != 0 {
			t.Error("backup of a file that did not exist")
		}
		contains(t, "stdout", r.stdout, "[claude] set up", "next time it renders")
	})

	t.Run("b) existing command: wrapped, rest of the file kept, backup made", func(t *testing.T) {
		p := profile(t, "claude")
		original := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"my-line; other\",\n    \"padding\": 0\n  },\n  \"theme\": \"dark\"\n}\n"
		write(t, settingsOf(p), original)
		r := run(t, []config.ProfileDir{p}, yes())
		if r.code != 0 {
			t.Fatalf("%+v", r)
		}
		want := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": " + quote(WrapCommand(hook, "my-line; other")) + ",\n    \"padding\": 0\n  },\n  \"theme\": \"dark\"\n}\n"
		if got := read(t, p); got != want {
			t.Errorf("settings.json:\n%s", got)
		}
		bak := backups(t, p)
		if len(bak) != 1 || bak[0] != "settings.json.bak-agtop-20261015T120000Z" {
			t.Fatalf("backups %v", bak)
		}
		if b, _ := os.ReadFile(filepath.Join(p.Dir, bak[0])); string(b) != original {
			t.Error("backup differs from the original")
		}
		if mode(t, settingsOf(p)) != 0o644 || mode(t, filepath.Join(p.Dir, bak[0])) != 0o644 {
			t.Error("modes not kept")
		}
		contains(t, "stdout", r.stdout, "backup: "+filepath.Join(p.Dir, bak[0]))
	})

	t.Run("a) idempotent: a second run changes nothing; the hand-written form is recognized", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, settingsOf(p), `{"statusLine":{"type":"command","command":"x"}}`)
		run(t, []config.ProfileDir{p}, yes())
		text := read(t, p)
		before, _ := os.Stat(settingsOf(p))
		again := run(t, []config.ProfileDir{p}, yes())
		after, _ := os.Stat(settingsOf(p))
		contains(t, "stdout", again.stdout, "[claude] already set up")
		if strings.Contains(again.stdout, "next time") || again.code != 0 || read(t, p) != text || !os.SameFile(before, after) || len(backups(t, p)) != 1 {
			t.Errorf("not idempotent: %+v", again)
		}

		m := profile(t, "manual")
		manualText := `{"statusLine":{"type":"command","command":` + quote(strings.Replace(manual, "node /somewhere/dist/cli.js statusline-hook", hook, 1)) + `}}`
		write(t, settingsOf(m), manualText)
		r := run(t, []config.ProfileDir{m}, yes())
		contains(t, "stdout", r.stdout, "[manual] already set up")
		if read(t, m) != manualText || exists(agtopOf(m)) || len(backups(t, m)) != 0 {
			t.Error("the hand-written form was touched")
		}
	})

	t.Run("a hook that points to another agtop is updated; --remove restores the original", func(t *testing.T) {
		for name, command := range map[string]string{
			"node":     manual,
			"abs":      strings.Replace(manual, "node /somewhere/dist/cli.js", "/old/bin/agtop", 1),
			"piped":    "/old/bin/agtop statusline-hook | bash ~/.claude/statusline.sh",
			"abs-only": "/old/bin/agtop statusline-hook --print",
		} {
			t.Run(name, func(t *testing.T) {
				p := profile(t, "claude")
				text := `{"model":"opus","statusLine":{"type":"command","command":` + quote(command) + `,"padding":0},"z":1}`
				write(t, settingsOf(p), text)
				dry := run(t, []config.ProfileDir{p}, runOpts{Options: Options{DryRun: true}})
				contains(t, "stdout", dry.stdout, "[claude] would update")
				if read(t, p) != text {
					t.Fatal("dry run wrote")
				}
				r := run(t, []config.ProfileDir{p}, yes())
				contains(t, "stdout", r.stdout, "[claude] updated")
				sl := statusLine(t, p)
				if r.code != 0 || sl["padding"] != 0.0 || !strings.Contains(sl["command"].(string), hook) || strings.Contains(sl["command"].(string), "/old/bin") || strings.Contains(sl["command"].(string), "/somewhere") {
					t.Fatalf("%+v %v", r, sl)
				}
				if len(backups(t, p)) != 1 {
					t.Errorf("backups %v", backups(t, p))
				}
				again := run(t, []config.ProfileDir{p}, yes())
				contains(t, "stdout", again.stdout, "[claude] already set up")
				run(t, []config.ProfileDir{p}, runOpts{Options: Options{Remove: true, Yes: true}})
				sl = statusLine(t, p)
				if name == "abs-only" {
					if _, has := sl["command"]; has || !strings.Contains(read(t, p), `"z"`) {
						t.Errorf("not removed: %s", read(t, p))
					}
					return
				}
				if sl["command"] != "bash ~/.claude/statusline.sh" || sl["padding"] != 0.0 || exists(agtopOf(p)) {
					t.Errorf("not restored: %s", read(t, p))
				}
			})
		}
	})

	t.Run("a hooked statusLine in an unknown form is skipped, not touched", func(t *testing.T) {
		p := profile(t, "claude")
		text := `{"statusLine":{"command":"a; statusline-hook; b"}}`
		write(t, settingsOf(p), text)
		r := run(t, []config.ProfileDir{p}, yes())
		contains(t, "stdout", r.stdout, "[claude] skipped (statusLine runs statusline-hook in a form agtop does not know")
		if read(t, p) != text || len(backups(t, p)) != 0 {
			t.Error("touched")
		}
	})

	t.Run("invalid settings.json: error, exit 1, nothing touched; the other profile goes ahead", func(t *testing.T) {
		p, ok := profile(t, "claude"), profile(t, "ok")
		write(t, settingsOf(p), `{ "statusLine": `)
		r := run(t, []config.ProfileDir{p, ok}, yes())
		if r.code != 1 || !regexp.MustCompile(`\[claude\] error \(.*settings\.json is not valid JSON.*untouched\)`).MatchString(r.stdout) {
			t.Errorf("%+v", r)
		}
		if read(t, p) != `{ "statusLine": ` || len(backups(t, p)) != 0 || exists(agtopOf(p)) {
			t.Error("the invalid profile was touched")
		}
		contains(t, "stdout", r.stdout, "[ok] set up")
	})

	t.Run("not a JSON object: error", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, settingsOf(p), `[1]`)
		r := run(t, []config.ProfileDir{p}, yes())
		contains(t, "stdout", r.stdout, "is not a JSON object; left untouched")
		if r.code != 1 {
			t.Errorf("exit %d", r.code)
		}
	})

	t.Run("statusLine without a command is skipped with a reason", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, settingsOf(p), `{"statusLine":{"type":"command"}}`)
		r := run(t, []config.ProfileDir{p}, yes())
		contains(t, "stdout", r.stdout, "[claude] skipped (statusLine has no command; wire the hook by hand)")
	})

	t.Run("warns when settings.local.json or the project overrides the status line", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, filepath.Join(p.Dir, "settings.local.json"), `{"statusLine":{"type":"command","command":"y"}}`)
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(cwd, ".claude", "settings.json"), `{"statusLine":{"type":"command","command":"z"}}`)
		write(t, filepath.Join(cwd, ".claude", "settings.local.json"), `{"model":"x"}`)
		o := yes()
		o.cwd = cwd
		r := run(t, []config.ProfileDir{p}, o)
		contains(t, "stderr", r.stderr,
			"warning [claude]: "+filepath.Join(p.Dir, "settings.local.json")+" defines statusLine",
			filepath.Join(cwd, ".claude", "settings.json")+" defines statusLine")
		if strings.Contains(r.stderr, filepath.Join(cwd, ".claude", "settings.local.json")) {
			t.Error("warned about a file without statusLine")
		}
	})

	t.Run("--dry-run shows the diff and writes nothing", func(t *testing.T) {
		p, fresh := profile(t, "claude"), profile(t, "fresh")
		text := `{"statusLine":{"type":"command","command":"x"}}`
		write(t, settingsOf(p), text)
		r := run(t, []config.ProfileDir{p, fresh}, runOpts{Options: Options{DryRun: true}})
		contains(t, "stdout", r.stdout,
			"[claude] "+settingsOf(p),
			"  - \"statusLine\": {", "  -   \"command\": \"x\"", "  - (no statusLine)",
			"  +   \"command\": \""+hook+" --print\"",
			"[claude] would set up", "[fresh] would set up")
		if r.code != 0 || len(r.asked) != 0 || read(t, p) != text ||
			!slices.Equal(dirNames(t, p.Dir), []string{"settings.json"}) || len(dirNames(t, fresh.Dir)) != 0 {
			t.Errorf("dry run changed something: %+v", r)
		}
	})

	t.Run("no terminal and no --yes: writes nothing, explains, exit 1", func(t *testing.T) {
		p := profile(t, "claude")
		r := run(t, []config.ProfileDir{p}, runOpts{})
		contains(t, "stderr", r.stderr, "nothing was written; run again with --yes")
		if r.code != 1 || len(dirNames(t, p.Dir)) != 0 {
			t.Errorf("%+v", r)
		}
	})

	t.Run("terminal: asks; no writes nothing, yes applies", func(t *testing.T) {
		p := profile(t, "claude")
		no := run(t, []config.ProfileDir{p}, runOpts{tty: true, answer: false})
		if !slices.Equal(no.asked, []string{"Set up 1 profile(s)? [y/N] "}) || no.code != 0 || len(dirNames(t, p.Dir)) != 0 {
			t.Errorf("%+v", no)
		}
		contains(t, "stdout", no.stdout, "Nothing written.")
		if r := run(t, []config.ProfileDir{p}, runOpts{tty: true, answer: true}); r.code != 0 || statusLine(t, p) == nil {
			t.Errorf("%+v", r)
		}
	})

	t.Run("a missing directory is skipped", func(t *testing.T) {
		gone := filepath.Join(t.TempDir(), "nope")
		r := run(t, []config.ProfileDir{{Label: "gone", Dir: gone}}, yes())
		contains(t, "stdout", r.stdout, "[gone] skipped")
		if r.code != 0 || exists(gone) {
			t.Errorf("%+v", r)
		}
	})

	t.Run("a symlinked settings.json is written through; the link stays", func(t *testing.T) {
		p := profile(t, "claude")
		realDir := t.TempDir()
		real := filepath.Join(realDir, "settings.json")
		write(t, real, "{}\n")
		if err := os.Symlink(real, settingsOf(p)); err != nil {
			t.Fatal(err)
		}
		run(t, []config.ProfileDir{p}, yes())
		if st, _ := os.Lstat(settingsOf(p)); st.Mode()&os.ModeSymlink == 0 {
			t.Error("the link was replaced")
		}
		if b, _ := os.ReadFile(real); !strings.Contains(string(b), "statusLine") {
			t.Error("target not updated")
		}
		if got := dirNames(t, realDir); !slices.Equal(got, []string{"settings.json"}) {
			t.Errorf("leftovers: %v", got)
		}
	})

	t.Run("aborts when settings.json changed between read and write", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, settingsOf(p), `{"a":1}`)
		var stdout, stderr bytes.Buffer
		code := Run([]config.ProfileDir{p}, Options{}, IO{
			Stdout: &stdout, Stderr: &stderr, IsTTY: true, Cwd: t.TempDir(),
			Now:  func() time.Time { return testNow },
			Hook: Hook{Command: hook},
			Confirm: func(string) bool {
				write(t, settingsOf(p), `{"a":2}`)
				return true
			},
		})
		contains(t, "stdout", stdout.String(), "changed since it was read; run again")
		if code != 1 || read(t, p) != `{"a":2}` || len(backups(t, p)) != 0 {
			t.Errorf("code %d, settings %s", code, read(t, p))
		}
	})

	t.Run("warns about the hook command when the caller says so", func(t *testing.T) {
		p := profile(t, "claude")
		var stderr bytes.Buffer
		Run([]config.ProfileDir{p}, Options{DryRun: true}, IO{Stdout: &bytes.Buffer{}, Stderr: &stderr, Cwd: t.TempDir(), Now: time.Now, Hook: Hook{Command: hook, Warning: "careful"}})
		contains(t, "stderr", stderr.String(), "agtop: warning: careful")
	})
}

func TestRemove(t *testing.T) {
	remove := runOpts{Options: Options{Yes: true, Remove: true}}

	t.Run("b) restores the original byte for byte and deletes agtop/; idempotent", func(t *testing.T) {
		p := profile(t, "claude")
		original := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"a && b\",\n    \"refreshInterval\": 2\n  },\n  \"z\": [\n    1,\n    2\n  ]\n}\n"
		write(t, settingsOf(p), original)
		run(t, []config.ProfileDir{p}, yes())
		write(t, filepath.Join(agtopOf(p), "usage.json"), "{}")
		write(t, filepath.Join(agtopOf(p), "context.json"), "{}")
		write(t, filepath.Join(agtopOf(p), ".usage.json.123.abc.tmp"), "")
		r := run(t, []config.ProfileDir{p}, remove)
		if r.code != 0 || read(t, p) != original || exists(agtopOf(p)) {
			t.Fatalf("%+v\n%s", r, read(t, p))
		}
		contains(t, "stdout", r.stdout, "[claude] removed")
		// Same timestamp: the second backup gets a suffix.
		got := backups(t, p)
		slices.Sort(got)
		if !slices.Equal(got, []string{"settings.json.bak-agtop-20261015T120000Z", "settings.json.bak-agtop-20261015T120000Z-2"}) {
			t.Errorf("backups %v", got)
		}
		again := run(t, []config.ProfileDir{p}, remove)
		contains(t, "stdout", again.stdout, "[claude] not set up")
		if again.code != 0 || read(t, p) != original || len(backups(t, p)) != 2 {
			t.Errorf("%+v", again)
		}
	})

	t.Run("c) removes the statusLine it added", func(t *testing.T) {
		p := profile(t, "claude")
		original := "{\n  \"model\": \"opus\"\n}\n"
		write(t, settingsOf(p), original)
		run(t, []config.ProfileDir{p}, yes())
		run(t, []config.ProfileDir{p}, remove)
		if read(t, p) != original || exists(agtopOf(p)) {
			t.Errorf("settings:\n%s", read(t, p))
		}
	})

	t.Run("without setup.json: unwraps the hand-written form", func(t *testing.T) {
		p := profile(t, "claude")
		write(t, settingsOf(p), `{"statusLine":{"type":"command","command":`+quote(manual)+`,"padding":0}}`)
		if err := os.Mkdir(agtopOf(p), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(agtopOf(p), "usage.json"), "{}")
		r := run(t, []config.ProfileDir{p}, remove)
		sl := statusLine(t, p)
		if r.code != 0 || sl["command"] != "bash ~/.claude/statusline.sh" || sl["padding"] != 0.0 || exists(agtopOf(p)) {
			t.Errorf("%+v %v", r, sl)
		}
	})

	t.Run("an unknown form is skipped, not touched", func(t *testing.T) {
		p := profile(t, "claude")
		text := `{"statusLine":{"command":"a; statusline-hook; b"}}`
		write(t, settingsOf(p), text)
		r := run(t, []config.ProfileDir{p}, remove)
		contains(t, "stdout", r.stdout, "[claude] skipped (statusLine runs statusline-hook in a form agtop does not know; edit it by hand)")
		if read(t, p) != text {
			t.Error("touched")
		}
	})

	t.Run("leaves agtop/ when it has files agtop does not own", func(t *testing.T) {
		p := profile(t, "claude")
		run(t, []config.ProfileDir{p}, yes())
		write(t, filepath.Join(agtopOf(p), "mine.txt"), "x")
		r := run(t, []config.ProfileDir{p}, remove)
		if got := dirNames(t, agtopOf(p)); !slices.Equal(got, []string{"mine.txt"}) {
			t.Errorf("agtop/: %v", got)
		}
		contains(t, "stderr", r.stderr, "has other files; left in place")
	})

	t.Run("--dry-run lists what would go and changes nothing", func(t *testing.T) {
		p := profile(t, "claude")
		run(t, []config.ProfileDir{p}, yes())
		before := read(t, p)
		r := run(t, []config.ProfileDir{p}, runOpts{Options: Options{Remove: true, DryRun: true}})
		contains(t, "stdout", r.stdout, "delete "+filepath.Join(agtopOf(p), "setup.json"), "[claude] would remove")
		if read(t, p) != before || !exists(filepath.Join(agtopOf(p), "setup.json")) {
			t.Error("dry run changed something")
		}
	})
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestResolveHook(t *testing.T) {
	exe := func(t *testing.T, dir, name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		write(t, p, "#!/bin/sh\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("agtop on the PATH with the same real path: plain agtop", func(t *testing.T) {
		install, bin := t.TempDir(), t.TempDir()
		self := exe(t, install, "agtop-1.0")
		if err := os.Symlink(self, filepath.Join(bin, "agtop")); err != nil {
			t.Fatal(err)
		}
		got := ResolveHook(self, "/nonexistent:"+bin)
		if got.Command != "agtop statusline-hook" {
			t.Errorf("got %q", got.Command)
		}
	})

	t.Run("another agtop first on the PATH, or none: absolute path, quoted", func(t *testing.T) {
		install := filepath.Join(t.TempDir(), "my install")
		if err := os.Mkdir(install, 0o755); err != nil {
			t.Fatal(err)
		}
		self := exe(t, install, "agtop")
		other, bin := t.TempDir(), t.TempDir()
		if err := os.Symlink(exe(t, other, "agtop"), filepath.Join(bin, "agtop")); err != nil {
			t.Fatal(err)
		}
		real, _ := filepath.EvalSymlinks(self)
		want := "'" + real + "' statusline-hook"
		for name, pathEnv := range map[string]string{"other first": bin + ":" + filepath.Dir(self), "none": ""} {
			if got := ResolveHook(self, pathEnv); got.Command != want {
				t.Errorf("%s: got %q, want %q", name, got.Command, want)
			}
		}
	})

	t.Run("a non-executable agtop on the PATH is skipped", func(t *testing.T) {
		install, bin := t.TempDir(), t.TempDir()
		self := exe(t, install, "agtop")
		write(t, filepath.Join(bin, "agtop"), "x") // 0644
		if got := ResolveHook(self, bin+":"+install); got.Command != "agtop statusline-hook" {
			t.Errorf("got %q", got.Command)
		}
	})
}
