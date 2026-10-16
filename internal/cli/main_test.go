package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
)

type cliResult struct {
	code           int
	stdout, stderr string
}

func mkHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// runCLI runs the command line with a clean environment (temporary HOME).
func runCLI(t *testing.T, args []string, home string, env config.Env) cliResult {
	t.Helper()
	if env == nil {
		env = config.Env{}
	}
	var stdout, stderr bytes.Buffer
	sys := System{Env: env, Homedir: home, Cwd: home, FS: config.OSFS{}, Stderr: &stderr}
	code := Main(args, sys, &stdout)
	return cliResult{code, stdout.String(), stderr.String()}
}

// writeConfig writes a TOML config file at <dir>/<name> (default: .config/agtop/config.toml).
func writeConfig(t *testing.T, dir, text, name string) string {
	t.Helper()
	if name == "" {
		name = filepath.Join(".config", "agtop", "config.toml")
	}
	file := filepath.Join(dir, name)
	mustMkdir(t, filepath.Dir(file))
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// lit is a TOML literal string with the path.
func lit(p string) string { return "'" + p + "'" }

func mkProfiles(t *testing.T, home string, names ...string) {
	t.Helper()
	for _, n := range names {
		mustMkdir(t, filepath.Join(home, n, "projects"))
		mustMkdir(t, filepath.Join(home, n, "sessions"))
	}
}

func configOf(home string) string { return filepath.Join(home, ".config", "agtop", "config.toml") }

// dumpLabels runs --dump and returns the label of each profile.
func dumpLabels(t *testing.T, home string, args []string, env config.Env) []string {
	t.Helper()
	r := runCLI(t, append([]string{"--dump"}, args...), home, env)
	// The first run without a file creates it and says so; nothing else may show up.
	if rest := regexp.MustCompile(`^agtop: created .*\n`).ReplaceAllString(r.stderr, ""); rest != "" {
		t.Errorf("stderr = %q", r.stderr)
	}
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	var out struct {
		Profiles []struct{ Label string } `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, p := range out.Profiles {
		labels = append(labels, p.Label)
	}
	return labels
}

func TestCLIBasics(t *testing.T) {
	t.Run("-h shows the help and exits 0", func(t *testing.T) {
		r := runCLI(t, []string{"-h"}, mkHome(t), nil)
		if r.code != 0 || !strings.Contains(r.stdout, "Usage: agtop") || !strings.Contains(r.stdout, "--config <path>") {
			t.Errorf("%+v", r)
		}
	})
	for _, flag := range []string{"--nao-existe", "--autodetect-siblings", "--no-autodetect"} {
		t.Run("unknown flag "+flag+" exits 1 with a message", func(t *testing.T) {
			r := runCLI(t, []string{flag}, mkHome(t), nil)
			if r.code != 1 || !regexp.MustCompile(`agtop: .*`+regexp.QuoteMeta(flag)).MatchString(r.stderr) {
				t.Errorf("%+v", r)
			}
		})
	}
}

func TestCLIValidation(t *testing.T) {
	t.Run("dir without projects/ exits 1 with a clear message", func(t *testing.T) {
		home := mkHome(t)
		dir := filepath.Join(home, ".claude-x")
		mustMkdir(t, dir)
		writeConfig(t, home, "[profiles]\nclaude-x = "+lit(dir)+"\n", "")
		r := runCLI(t, []string{"--dump"}, home, nil)
		want := "agtop: error [claude-x]: " + dir + " does not look like a Claude Code config directory: missing projects/"
		if r.code != 1 || !strings.Contains(r.stderr, want) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("path under a file exits 1 with the validation error (no raw ENOTDIR)", func(t *testing.T) {
		home := mkHome(t)
		file := filepath.Join(home, "arquivo")
		writeConfig(t, home, "", "arquivo")
		writeConfig(t, home, "[profiles]\nx = "+lit(filepath.Join(file, "x"))+"\n", "")
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 1 || !strings.Contains(r.stderr, "does not exist or is not a directory") || strings.Contains(r.stderr, "ENOTDIR") {
			t.Errorf("%+v", r)
		}
	})
	t.Run("dir without sessions/ warns on stderr and exits 0", func(t *testing.T) {
		home := mkHome(t)
		mustMkdir(t, filepath.Join(home, ".claude", "projects"))
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 0 || !regexp.MustCompile(`warning \[claude\]: .*has no sessions/`).MatchString(r.stderr) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("without ~/.claude: exits 1 saying where it looked and suggesting [profiles]", func(t *testing.T) {
		home := mkHome(t)
		writeConfig(t, home, "{}", ".claude.json")
		mustMkdir(t, filepath.Join(home, ".claude-work", "projects"))
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 1 || !strings.Contains(r.stderr, "no Claude Code config directory found") ||
			!strings.Contains(r.stderr, filepath.Join(home, ".claude")) ||
			!strings.Contains(r.stderr, "[profiles] in "+configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("two labels for the same directory (trailing slash) is an error", func(t *testing.T) {
		home := mkHome(t)
		writeConfig(t, home, "[profiles]\na = \"/nonexistent-agtop/x/\"\nb = \"/nonexistent-agtop/x\"\n", "")
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 1 || !strings.Contains(r.stderr, "point to the same directory (/nonexistent-agtop/x)") {
			t.Errorf("%+v", r)
		}
	})
}

func TestCLIProfilePrecedence(t *testing.T) {
	t.Run("without configuration only ~/.claude; then CLAUDE_CONFIG_DIR", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude", ".claude-work", ".claude-ccd")
		if got := dumpLabels(t, home, nil, nil); !slices.Equal(got, []string{"claude"}) {
			t.Errorf("labels = %v", got)
		}
		// The first run wrote the file, which now wins over CLAUDE_CONFIG_DIR.
		env := config.Env{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-ccd")}
		if got := dumpLabels(t, home, nil, env); !slices.Equal(got, []string{"claude"}) {
			t.Errorf("labels = %v", got)
		}
		fresh := mkHome(t)
		mkProfiles(t, fresh, ".claude", ".claude-ccd")
		env = config.Env{"CLAUDE_CONFIG_DIR": filepath.Join(fresh, ".claude-ccd")}
		if got := dumpLabels(t, fresh, nil, env); !slices.Equal(got, []string{"claude-ccd"}) {
			t.Errorf("labels = %v", got)
		}
	})
	t.Run("[profiles] of the file (in order) wins over CLAUDE_CONFIG_DIR and ~/.claude", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude", ".claude-work", ".claude-ccd")
		writeConfig(t, home, "[profiles]\nw = "+lit(filepath.Join(home, ".claude-work"))+"\np = \"~/.claude\"\n", "")
		env := config.Env{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-ccd")}
		if got := dumpLabels(t, home, nil, env); !slices.Equal(got, []string{"w", "p"}) {
			t.Errorf("labels = %v", got)
		}
	})
	t.Run("--config wins over the default file; XDG_CONFIG_HOME wins over ~/.config", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude", ".claude-work", ".claude-x")
		writeConfig(t, home, "[profiles]\nhome = "+lit(filepath.Join(home, ".claude"))+"\n", "")
		xdg := filepath.Join(home, "xdg")
		writeConfig(t, xdg, "[profiles]\nxdg = "+lit(filepath.Join(home, ".claude-work"))+"\n", "agtop/config.toml")
		other := writeConfig(t, home, "[profiles]\nflag = "+lit(filepath.Join(home, ".claude-x"))+"\n", "other.toml")
		tests := []struct {
			args []string
			env  config.Env
			want string
		}{
			{nil, nil, "home"},
			{nil, config.Env{"XDG_CONFIG_HOME": xdg}, "xdg"},
			{[]string{"--config", other}, config.Env{"XDG_CONFIG_HOME": xdg}, "flag"},
		}
		for _, tt := range tests {
			if got := dumpLabels(t, home, tt.args, tt.env); !slices.Equal(got, []string{tt.want}) {
				t.Errorf("labels = %v, want %s", got, tt.want)
			}
		}
	})
	t.Run("missing --config or invalid file: exits 1 quoting the path", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		missing := runCLI(t, []string{"--config", filepath.Join(home, "nada.toml")}, home, nil)
		if missing.code != 1 || !strings.Contains(missing.stderr, "config file not found: "+filepath.Join(home, "nada.toml")) {
			t.Errorf("%+v", missing)
		}
		file := writeConfig(t, home, "[profiles\n", "")
		if bad := runCLI(t, []string{"--dump"}, home, nil); bad.code != 1 || !strings.Contains(bad.stderr, file) {
			t.Errorf("%+v", bad)
		}
		unknown := writeConfig(t, home, "colors = \"dark\"\n", "")
		if r := runCLI(t, []string{"--dump"}, home, nil); r.code != 1 || !strings.Contains(r.stderr, unknown+`: unknown key "colors"`) {
			t.Errorf("%+v", r)
		}
	})
}

func TestCLIConfigFileCreatedOnFirstRun(t *testing.T) {
	t.Run("creates it with the default profile and hints, says so on stderr, mode 0600", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude", ".claude-work", ".claude-b")
		r := runCLI(t, []string{"--dump"}, home, nil)
		want := "agtop: created " + configOf(home) + " with 1 profile; edit it to watch other directories (see --help)\n"
		if r.code != 0 || r.stderr != want {
			t.Fatalf("%+v", r)
		}
		data, _ := os.ReadFile(configOf(home))
		text := string(data)
		if !strings.Contains(text, "\n[profiles]\nclaude = \"~/.claude\"\n") ||
			!strings.Contains(text, "# claude-b = \"~/.claude-b\"\n# claude-work = \"~/.claude-work\"\n") {
			t.Errorf("config = %q", text)
		}
		if st, err := os.Stat(configOf(home)); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", st.Mode())
		}
	})
	t.Run("does not rewrite on the second run and prints nothing", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		runCLI(t, []string{"--dump"}, home, nil)
		before, _ := os.Stat(configOf(home))
		r := runCLI(t, []string{"--dump"}, home, nil)
		after, _ := os.Stat(configOf(home))
		if r.stderr != "" || !after.ModTime().Equal(before.ModTime()) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("CLAUDE_CONFIG_DIR becomes the real key", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude", ".claude-ccd", ".claude-work")
		runCLI(t, []string{"--dump"}, home, config.Env{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-ccd")})
		data, _ := os.ReadFile(configOf(home))
		text := string(data)
		for _, want := range []string{"\n[profiles]\nclaude-ccd = \"~/.claude-ccd\"\n", "# claude = \"~/.claude\"",
			"# claude-work = \"~/.claude-work\"", "CLAUDE_CONFIG_DIR, which no longer applies"} {
			if !strings.Contains(text, want) {
				t.Errorf("config lacks %q", want)
			}
		}
	})
	t.Run("says CLAUDE_CONFIG_DIR is pinned when the profile came from it", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude-ccd")
		ccd := filepath.Join(home, ".claude-ccd")
		r := runCLI(t, []string{"--dump"}, home, config.Env{"CLAUDE_CONFIG_DIR": ccd})
		want := "agtop: created " + configOf(home) + " with 1 profile (CLAUDE_CONFIG_DIR=" + ccd + "); " +
			"CLAUDE_CONFIG_DIR no longer applies, edit the file to watch other directories (see --help)\n"
		if r.stderr != want {
			t.Errorf("stderr = %q", r.stderr)
		}
	})
	t.Run("warns when the config path is a broken symlink", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		mustMkdir(t, filepath.Dir(configOf(home)))
		if err := os.Symlink(filepath.Join(home, "nowhere"), configOf(home)); err != nil {
			t.Fatal(err)
		}
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 0 || !strings.Contains(r.stderr, "could not create "+configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("respects XDG_CONFIG_HOME", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		xdg := filepath.Join(home, "xdg")
		r := runCLI(t, []string{"--dump"}, home, config.Env{"XDG_CONFIG_HOME": xdg})
		if r.code != 0 || !exists(filepath.Join(xdg, "agtop", "config.toml")) || exists(configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("does not create with --config", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		other := writeConfig(t, home, "", "other.toml")
		r := runCLI(t, []string{"--dump", "--config", other}, home, nil)
		if r.code != 0 || r.stderr != "" || exists(configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("leaves an existing file without [profiles] untouched", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		writeConfig(t, home, "# mine\n", "")
		r := runCLI(t, []string{"--dump"}, home, nil)
		data, _ := os.ReadFile(configOf(home))
		if r.code != 0 || r.stderr != "" || string(data) != "# mine\n" {
			t.Errorf("%+v", r)
		}
	})
	t.Run("does not create it when validation fails", func(t *testing.T) {
		home := mkHome(t)
		mustMkdir(t, filepath.Join(home, ".claude"))
		r := runCLI(t, []string{"--dump"}, home, nil)
		if r.code != 1 || exists(configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("warns and goes on when the directory is not writable", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root writes anywhere")
		}
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		if err := os.Mkdir(filepath.Join(home, ".config"), 0o500); err != nil {
			t.Fatal(err)
		}
		r := runCLI(t, []string{"--dump"}, home, nil)
		_ = os.Chmod(filepath.Join(home, ".config"), 0o700)
		if r.code != 0 || !strings.Contains(r.stderr, "agtop: warning: could not create "+configOf(home)) {
			t.Errorf("%+v", r)
		}
	})
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestCLIRemovedFlagsAndEnvs(t *testing.T) {
	home := mkHome(t)
	file := configOf(home)
	given := filepath.Join(home, "my.toml")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"--claude-dir", []string{"--claude-dir", "x"}, "--claude-dir was removed: list the profiles in " + file + " under [profiles]"},
		{"--context-window", []string{"--context-window=a=1"}, "--context-window was removed: context windows are now detected automatically"},
		{"--claude-dir quotes --config", []string{"--config", given, "--claude-dir", "x"}, "list the profiles in " + given + " under [profiles]"},
		{"--claude-dir quotes --config=", []string{"--claude-dir", "x", "--config=" + given}, "list the profiles in " + given + " under [profiles]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runCLI(t, tt.args, home, nil)
			if r.code != 1 || !strings.Contains(r.stderr, tt.want) {
				t.Errorf("%+v", r)
			}
		})
	}
	for _, args := range [][]string{{"--claude-dir", "x", "--help"}, {"-h", "--context-window=1"}} {
		t.Run("--help wins over "+strings.Join(args, " "), func(t *testing.T) {
			r := runCLI(t, args, home, nil)
			if r.code != 0 || !strings.Contains(r.stdout, "Config file") {
				t.Errorf("%+v", r)
			}
		})
	}
	t.Run("old envs warn on stderr and go on", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		r := runCLI(t, []string{"--dump"}, home, config.Env{"AGTOP_CLAUDE_DIRS": "/x", "AGTOP_CONTEXT_WINDOWS": "m=1"})
		if r.code != 0 ||
			!strings.Contains(r.stderr, "AGTOP_CLAUDE_DIRS is ignored since agtop 0.2: use "+configOf(home)) ||
			!strings.Contains(r.stderr, "AGTOP_CONTEXT_WINDOWS is ignored since agtop 0.2: context windows are now detected automatically") {
			t.Errorf("%+v", r)
		}
	})
}

func TestCLIDumpPrintsOnlyMetadata(t *testing.T) {
	home := mkHome(t)
	dir := filepath.Join(home, ".claude")
	project := filepath.Join(dir, "projects", "x")
	mustMkdir(t, project)
	mustMkdir(t, filepath.Join(dir, "sessions"))
	pid := os.Getpid()
	reg, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": "s1", "status": "idle", "startedAt": time.Now().UnixMilli()})
	writeConfig(t, dir, string(reg), filepath.Join("sessions", strconv.Itoa(pid)+".json"))
	assistant, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{
		"id": "m1", "model": "claude-opus-4-7[1m]",
		"content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": "Agent", "input": map[string]any{"prompt": "SECRET"}}},
		"usage":   map[string]any{"input_tokens": 100_000, "output_tokens": 1},
	}})
	writeConfig(t, project, string(assistant)+"\n"+`{"type":"ai-title","aiTitle":"Titulo"}`+"\n", "s1.jsonl")
	writeConfig(t, home, "", "")
	r := runCLI(t, []string{"--dump"}, home, nil)
	if r.stderr != "" || r.code != 0 || strings.Contains(r.stdout, "SECRET") {
		t.Fatalf("%+v", r)
	}
	var out DumpSummary
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Profiles) != 1 || out.Profiles[0].Label != "claude" || out.Profiles[0].LiveCount != 1 {
		t.Errorf("profiles = %+v", out.Profiles)
	}
	if out.Totals.Sessions != 1 || len(out.Totals.AgentsByStatus) != 1 || out.Totals.AgentsByStatus["launched"] != 1 {
		t.Errorf("totals = %+v", out.Totals)
	}
	s := out.Sessions[0]
	if s.Title != "Titulo" || s.ContextWindow.Size != 1_000_000 || s.ContextWindow.Source != "suffix" || s.ContextPercent != 10 {
		t.Errorf("session = %+v", s)
	}
}
