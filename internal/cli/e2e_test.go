package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/cli/setup"
)

// binary is the agtop executable, compiled once for the end-to-end tests.
var binary string

func TestMain(m *testing.M) {
	code := func() int {
		dir, err := os.MkdirTemp("", "agtop-e2e-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		binary = filepath.Join(dir, "agtop")
		build := exec.Command("go", "build", "-o", binary, "github.com/danielfnfaria/agtop")
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building the binary: %v\n%s", err, out)
			return 1
		}
		return m.Run()
	}()
	os.Exit(code)
}

type binResult struct {
	code           int
	stdout, stderr string
}

// runBin runs the binary with a clean environment (temporary HOME) and the given stdin.
func runBin(t *testing.T, bin string, args []string, stdin string, env ...string) binResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	home := t.TempDir()
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + home}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return binResult{code, stdout.String(), stderr.String()}
}

func TestE2EHook(t *testing.T) {
	body := `{"session_id":"SECRET","cwd":"/SECRET","rate_limits":{"five_hour":{"used_percentage":41,"resets_at":1900000000}}}`
	hookRun := func(t *testing.T, cfgDir, input string, args ...string) binResult {
		t.Helper()
		return runBin(t, binary, append([]string{"statusline-hook"}, args...), input, "CLAUDE_CONFIG_DIR="+cfgDir)
	}

	t.Run("stdin to stdout unchanged, usage recorded, exit 0 (also with bad input)", func(t *testing.T) {
		cfgDir := filepath.Join(t.TempDir(), "work-profile")
		ok := hookRun(t, cfgDir, body)
		if ok.code != 0 || ok.stdout != body || ok.stderr != "" {
			t.Fatalf("%+v", ok)
		}
		raw, err := os.ReadFile(filepath.Join(cfgDir, "agtop", "usage.json"))
		if err != nil {
			t.Fatal(err)
		}
		var usage struct {
			Version  int
			FiveHour struct{ Percent, ResetsAt float64 }
		}
		if err := json.Unmarshal(raw, &usage); err != nil || usage.Version != 1 || usage.FiveHour.Percent != 41 || usage.FiveHour.ResetsAt != 1_900_000_000_000 {
			t.Errorf("usage.json = %s", raw)
		}
		if strings.Contains(string(raw), "SECRET") {
			t.Error("payload leaked into usage.json")
		}
		for _, tt := range []struct {
			input string
			args  []string
		}{{"not json at all", nil}, {"", nil}, {body, []string{"--whatever", "x"}}} {
			r := hookRun(t, cfgDir, tt.input, tt.args...)
			if r.code != 0 || r.stdout != tt.input || r.stderr != "" {
				t.Errorf("%q %v: %+v", tt.input, tt.args, r)
			}
		}
	})

	t.Run("--print: the short line, never the payload", func(t *testing.T) {
		in := `{"rate_limits":{"five_hour":{"used_percentage":12}}}`
		r := hookRun(t, filepath.Join(t.TempDir(), "p"), in, "--print")
		if r.code != 0 || r.stdout != "agtop \u00b7 5h 12%\n" {
			t.Errorf("%+v", r)
		}
	})

	t.Run("a closed stdout (EPIPE) still exits 0 and records", func(t *testing.T) {
		cfgDir := filepath.Join(t.TempDir(), "p")
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		pr.Close()
		cmd := exec.Command(binary, "statusline-hook")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_CONFIG_DIR=" + cfgDir}
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = pw
		err = cmd.Run()
		pw.Close()
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
		if _, err := os.Stat(filepath.Join(cfgDir, "agtop", "usage.json")); err != nil {
			t.Error("usage not recorded")
		}
	})

	t.Run("AGTOP_DEBUG: errors on stderr only", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "afile")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		r := runBin(t, binary, []string{"statusline-hook"}, body, "CLAUDE_CONFIG_DIR="+file, "AGTOP_DEBUG=1")
		if r.code != 0 || r.stdout != body || r.stderr != "agtop statusline-hook: failed: ENOTDIR\n" {
			t.Errorf("%+v", r)
		}
	})
}

// TestE2EHookSpeed runs the hook as a status line would, over and over. Locally the p50 is ~3 ms
// (the Node build: ~45 ms); the bound leaves room for slow CI machines but catches a heavy init.
func TestE2EHookSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	body := `{"session_id":"s1","model":{"id":"claude-opus-5-5"},"context_window":{"context_window_size":200000},"rate_limits":{"five_hour":{"used_percentage":6.4,"resets_at":1900000000}}}`
	cfgDir := t.TempDir()
	run := func() {
		cmd := exec.Command(binary, "statusline-hook")
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + cfgDir, "CLAUDE_CONFIG_DIR=" + cfgDir}
		cmd.Stdin = strings.NewReader(body)
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
	}
	run() // warm up (first exec of a fresh binary)
	const n = 20
	var durations []time.Duration
	for range n {
		start := time.Now()
		run()
		durations = append(durations, time.Since(start))
	}
	slices.Sort(durations)
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	mean := sum / n
	t.Logf("hook: mean %v, p50 %v, max %v", mean, durations[n/2], durations[n-1])
	if mean >= 20*time.Millisecond {
		t.Errorf("mean %v >= 20ms", mean)
	}
}

func TestE2EViewerWithoutTerminal(t *testing.T) {
	home := t.TempDir()
	mkProfiles(t, home, ".claude")
	writeConfig(t, home, "", "")
	r := runBin(t, binary, nil, "", "HOME="+home)
	// A long path wraps at 80 columns.
	if r.code != 0 || r.stderr != "agtop: stdin is not a terminal; rendering once\n" ||
		!strings.Contains(r.stdout, "agtop") || !strings.Contains(strings.ReplaceAll(r.stdout, "\n", ""), filepath.Join(home, ".claude")) {
		t.Errorf("%+v", r)
	}
}

// colorSGR matches foreground/background color escapes; bold, dim and inverse are not colors.
var colorSGR = regexp.MustCompile(`\x1b\[(?:3\d|9\d|4\d|10\d|[34]8;)[0-9;]*m`)

func TestE2EOnceColorEnv(t *testing.T) {
	home := t.TempDir()
	mkProfiles(t, home, ".claude")
	for _, tt := range []struct {
		env   []string
		color bool
	}{
		{nil, false},
		{[]string{"FORCE_COLOR=1"}, true},
		{[]string{"NO_COLOR=1", "FORCE_COLOR=1"}, false},
		{[]string{"FORCE_COLOR=0"}, false},
		{[]string{"NO_COLOR=", "FORCE_COLOR=1"}, true},
	} {
		r := runBin(t, binary, nil, "", append([]string{"HOME=" + home}, tt.env...)...)
		if r.code != 0 || !strings.Contains(r.stdout, "agtop") || colorSGR.MatchString(r.stdout) != tt.color {
			t.Errorf("%v: color %v, got %+v", tt.env, tt.color, r)
		}
	}
}

func TestE2EOnceOutputIdenticalAcrossColorEnv(t *testing.T) {
	home := t.TempDir()
	mkProfiles(t, home, ".claude")
	runBin(t, binary, nil, "", "HOME="+home) // the first run creates the config file
	base := runBin(t, binary, nil, "", "HOME="+home)
	if base.code != 0 || strings.Contains(base.stdout, "\x1b") {
		t.Fatalf("no SGR expected without a terminal: %q", base.stdout)
	}
	for _, env := range []string{"NO_COLOR=1", "FORCE_COLOR=0"} {
		r := runBin(t, binary, nil, "", "HOME="+home, env)
		if r.code != 0 || r.stdout != base.stdout {
			t.Errorf("%s: %q differs from %q", env, r.stdout, base.stdout)
		}
	}
}

// `script` (BSD, macOS) gives stdin a pseudo-terminal while stdout goes to a file.
func TestE2EOnceStdoutRedirected(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("BSD script(1)")
	}
	home := t.TempDir()
	mkProfiles(t, home, ".claude")
	writeConfig(t, home, "", "")
	out, errf := filepath.Join(home, "out.txt"), filepath.Join(home, "err.txt")
	cmd := exec.Command("script", "-q", "/dev/null", "sh", "-c", `"$0" > "$1" 2> "$2"; echo "exit=$?"`, binary, out, errf)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	res, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := os.ReadFile(out)
	stderr, _ := os.ReadFile(errf)
	if !strings.Contains(string(res), "exit=0") || string(stderr) != "agtop: stdout is not a terminal; rendering once\n" || len(stdout) == 0 {
		t.Errorf("result %q, stderr %q, stdout %q", res, stderr, stdout)
	}
}

func TestE2ESetup(t *testing.T) {
	setupRun := func(t *testing.T, home string, args ...string) binResult {
		return runBin(t, binary, append([]string{"setup"}, args...), "", "HOME="+home)
	}
	newHome := func(t *testing.T) (home, dir string) {
		home = t.TempDir()
		dir = filepath.Join(home, ".claude")
		mustMkdir(t, dir)
		return home, dir
	}
	// The binary is not on the PATH of the test: the hook is its absolute path, quoted.
	hookCmd := setup.ShellQuote(mustReal(t, binary)) + " statusline-hook"

	t.Run("--dry-run shows the change; without a terminal and --yes it writes nothing; then apply and remove", func(t *testing.T) {
		home, dir := newHome(t)
		dry := setupRun(t, home, "--dry-run")
		for _, want := range []string{"[claude] " + filepath.Join(dir, "settings.json"), "statusline-hook --print", "[claude] would set up"} {
			if !strings.Contains(dry.stdout, want) {
				t.Errorf("dry-run stdout lacks %q:\n%s", want, dry.stdout)
			}
		}
		if dry.code != 0 {
			t.Errorf("dry-run exit %d", dry.code)
		}
		noTTY := setupRun(t, home)
		if noTTY.code != 1 || !strings.Contains(noTTY.stderr, "run again with --yes") {
			t.Errorf("%+v", noTTY)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("wrote %d entries", len(entries))
		}
		cfg := writeConfig(t, home, "[profiles]\nmine = "+lit(dir)+"\n", "c.toml")
		yes := setupRun(t, home, "--yes", "--config", cfg)
		if yes.code != 0 || !strings.Contains(yes.stdout, "[mine] set up") {
			t.Fatalf("%+v", yes)
		}
		var settings map[string]map[string]string
		b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err := json.Unmarshal(b, &settings); err != nil || settings["statusLine"]["command"] != hookCmd+" --print" {
			t.Errorf("settings.json = %s, want command %q", b, hookCmd+" --print")
		}
		removed := setupRun(t, home, "--remove", "-y", "--config", cfg)
		if removed.code != 0 || !strings.Contains(removed.stdout, "[mine] removed") {
			t.Errorf("%+v", removed)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "settings.json")); strings.TrimSpace(string(b)) != "{}" {
			t.Errorf("after remove: %s", b)
		}
	})

	t.Run("the first agtop on the PATH being this binary gives the plain command", func(t *testing.T) {
		home, dir := newHome(t)
		bin := t.TempDir()
		if err := os.Symlink(binary, filepath.Join(bin, "agtop")); err != nil {
			t.Fatal(err)
		}
		r := runBin(t, binary, []string{"setup", "--yes"}, "", "HOME="+home, "PATH="+bin+":/usr/bin:/bin")
		if r.code != 0 {
			t.Fatalf("%+v", r)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
		if !strings.Contains(string(b), `"command": "agtop statusline-hook --print"`) {
			t.Errorf("settings.json = %s", b)
		}
	})

	// The generated command, run by a shell the way Claude Code does.
	t.Run("the generated command works under sh", func(t *testing.T) {
		body := `{"session_id":"S","rate_limits":{"five_hour":{"used_percentage":6,"resets_at":1900000000},"seven_day":{"used_percentage":30}}}`
		spaced := filepath.Join(t.TempDir(), "with space")
		mustMkdir(t, spaced)
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(spaced, "agtop")
		if err := os.WriteFile(bin, data, 0o755); err != nil {
			t.Fatal(err)
		}
		runSh := func(command, cfgDir string) binResult {
			return runBin(t, "/bin/sh", []string{"-c", command}, body, "CLAUDE_CONFIG_DIR="+cfgDir)
		}
		commandOf := func(dir string) string {
			var s map[string]map[string]string
			b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
			if err := json.Unmarshal(b, &s); err != nil {
				t.Fatal(err)
			}
			return s["statusLine"]["command"]
		}

		t.Run("b) an original with ; and && still gets stdin; the usage is recorded", func(t *testing.T) {
			home, dir := newHome(t)
			fake := filepath.Join(t.TempDir(), "fake line.sh")
			if err := os.WriteFile(fake, []byte("printf \"<\"; cat; printf \">\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			original := "printf 'A['; cat; printf ']' && sh " + setup.ShellQuote(fake)
			b, _ := json.Marshal(map[string]any{"statusLine": map[string]string{"type": "command", "command": original}})
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), b, 0o644); err != nil {
				t.Fatal(err)
			}
			if r := runBin(t, bin, []string{"setup", "--yes"}, "", "HOME="+home); r.code != 0 {
				t.Fatalf("%+v", r)
			}
			command := commandOf(dir)
			if !strings.Contains(command, "'") {
				t.Errorf("path with a space not quoted: %s", command)
			}
			r := runSh(command, dir)
			// `cat` in the fake line gets nothing: the first `cat` already read the payload.
			if r.code != 0 || r.stdout != "A["+body+"]<>" {
				t.Errorf("%+v", r)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "agtop", "usage.json"))
			if err != nil || !strings.Contains(string(raw), `"fiveHour":{"percent":6`) || !strings.Contains(string(raw), `"sevenDay":{"percent":30`) {
				t.Errorf("usage.json = %s (%v)", raw, err)
			}
		})

		t.Run("c) the print command shows the short line, not the JSON", func(t *testing.T) {
			home, dir := newHome(t)
			if r := runBin(t, bin, []string{"setup", "--yes"}, "", "HOME="+home); r.code != 0 {
				t.Fatalf("%+v", r)
			}
			r := runSh(commandOf(dir), dir)
			if r.code != 0 || r.stdout != "agtop \u00b7 5h 6% \u00b7 7d 30%\n" {
				t.Errorf("%+v", r)
			}
		})
	})
}

func mustReal(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
