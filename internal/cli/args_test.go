package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/cli/setup"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
)

func resolve(t *testing.T, argv []string, home string, mut func(*System)) (Command, string, error) {
	t.Helper()
	var stderr bytes.Buffer
	sys := System{Env: config.Env{}, Homedir: home, Cwd: home, FS: config.OSFS{}, Stderr: &stderr}
	if mut != nil {
		mut(&sys)
	}
	cmd, err := Resolve(argv, sys)
	return cmd, stderr.String(), err
}

func TestResolveSubcommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		argv []string
		want Command
	}{
		{"hook", []string{"statusline-hook"}, Command{Mode: ModeHook}},
		{"hook --print", []string{"statusline-hook", "--print"}, Command{Mode: ModeHook, Print: true}},
		{"hook ignores other arguments", []string{"statusline-hook", "--whatever", "x"}, Command{Mode: ModeHook}},
		{"hook --print among others", []string{"statusline-hook", "--x", "--print"}, Command{Mode: ModeHook, Print: true}},
		{"hook ignores --help", []string{"statusline-hook", "--help"}, Command{Mode: ModeHook}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The hook does not even look at the configuration: it must work with a broken one.
			home := mkHome(t)
			writeConfig(t, home, "[nope]\n", "")
			cmd, stderr, err := resolve(t, tt.argv, home, nil)
			if err != nil || !reflect.DeepEqual(cmd, tt.want) || stderr != "" {
				t.Errorf("%+v, %q, %v", cmd, stderr, err)
			}
			if got, _ := os.ReadFile(configOf(home)); string(got) != "[nope]\n" {
				t.Error("config rewritten")
			}
		})
	}

	for _, tt := range []struct {
		name string
		argv []string
		want setup.Options
	}{
		{"plain", []string{"setup"}, setup.Options{}},
		{"--remove", []string{"setup", "--remove"}, setup.Options{Remove: true}},
		{"--yes", []string{"setup", "--yes"}, setup.Options{Yes: true}},
		{"-y", []string{"setup", "-y"}, setup.Options{Yes: true}},
		{"--dry-run", []string{"setup", "--dry-run"}, setup.Options{DryRun: true}},
		{"all", []string{"setup", "--remove", "-y", "--dry-run"}, setup.Options{Remove: true, Yes: true, DryRun: true}},
	} {
		t.Run("setup "+tt.name, func(t *testing.T) {
			home := mkHome(t)
			mkProfiles(t, home, ".claude")
			cmd, _, err := resolve(t, tt.argv, home, nil)
			if err != nil || cmd.Mode != ModeSetup || cmd.Setup != tt.want || len(cmd.Profiles) != 1 || cmd.Profiles[0].Label != "claude" {
				t.Errorf("%+v, %v", cmd, err)
			}
		})
	}

	t.Run("setup does not validate the directories nor create the config file", func(t *testing.T) {
		home := mkHome(t)
		mustMkdir(t, filepath.Join(home, ".claude")) // no projects/
		cmd, stderr, err := resolve(t, []string{"setup", "--dry-run"}, home, nil)
		if err != nil || cmd.Mode != ModeSetup || stderr != "" {
			t.Errorf("%+v, %q, %v", cmd, stderr, err)
		}
		if _, err := os.Stat(configOf(home)); err == nil {
			t.Error("config created")
		}
	})

	t.Run("setup uses --config", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude-x")
		cfg := writeConfig(t, home, "[profiles]\nmine = "+lit(filepath.Join(home, ".claude-x"))+"\n", "c.toml")
		cmd, _, err := resolve(t, []string{"setup", "--config", cfg}, home, nil)
		if err != nil || len(cmd.Profiles) != 1 || cmd.Profiles[0].Label != "mine" {
			t.Errorf("%+v, %v", cmd, err)
		}
	})
}

func TestResolveHelpAndErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		argv []string
		mode Mode
		err  string
	}{
		{"--help", []string{"--help"}, ModeHelp, ""},
		{"-h", []string{"-h"}, ModeHelp, ""},
		{"setup --help", []string{"setup", "--help"}, ModeHelp, ""},
		{"setup -h wins over unknown flags", []string{"setup", "--bogus", "-h"}, ModeHelp, ""},
		{"--help wins over a removed flag", []string{"--claude-dir", "x", "--help"}, ModeHelp, ""},
		{"setup --help wins over a removed flag", []string{"setup", "--context-window", "1", "-h"}, ModeHelp, ""},
		{"unknown flag", []string{"--nope"}, 0, "unknown option '--nope'"},
		{"unknown short flag", []string{"-x"}, 0, "unknown option '-x'"},
		{"positional", []string{"foo"}, 0, "unexpected argument 'foo'"},
		{"setup positional", []string{"setup", "foo"}, 0, "unexpected argument 'foo'"},
		{"setup unknown flag", []string{"setup", "--bogus"}, 0, "unknown option '--bogus'"},
		{"--remove is only for setup", []string{"--remove"}, 0, "unknown option '--remove'"},
		{"--dump is not a setup flag", []string{"setup", "--dump"}, 0, "unknown option '--dump'"},
		{"--yes is only for setup", []string{"--yes"}, 0, "unknown option '--yes'"},
		{"--config without value", []string{"--config"}, 0, "option '--config <value>' argument missing"},
		{"setup --config without value", []string{"setup", "--config", "--yes"}, 0, "option '--config <value>' argument missing"},
		{"--context-window", []string{"--context-window", "1m"}, 0, "--context-window was removed: context windows are now detected automatically"},
		{"setup --context-window=", []string{"setup", "--context-window=1m"}, 0, "--context-window was removed: context windows are now detected automatically"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := resolve(t, tt.argv, mkHome(t), nil)
			if tt.err == "" {
				if err != nil || cmd.Mode != tt.mode {
					t.Errorf("%+v, %v", cmd, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("err = %v, want %q", err, tt.err)
			} else if _, ok := err.(*config.Error); !ok {
				t.Errorf("%T, want *config.Error", err)
			}
		})
	}

	t.Run("--claude-dir points to the default config path, or to --config", func(t *testing.T) {
		home := mkHome(t)
		_, _, err := resolve(t, []string{"setup", "--claude-dir=x"}, home, nil)
		if err == nil || err.Error() != "--claude-dir was removed: list the profiles in "+configOf(home)+" under [profiles] (see --help)" {
			t.Errorf("err = %v", err)
		}
		_, _, err = resolve(t, []string{"--claude-dir", "x", "--config", "rel/c.toml"}, home, nil)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(home, "rel", "c.toml")) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestResolveModeByTTY(t *testing.T) {
	for _, tt := range []struct {
		name          string
		argv          []string
		stdin, stdout bool
		want          Mode
	}{
		{"both terminals: interactive", nil, true, true, ModeInteractive},
		{"stdout redirected: once", nil, true, false, ModeOnce},
		{"stdin piped: once", nil, false, true, ModeOnce},
		{"neither: once", nil, false, false, ModeOnce},
		{"--dump wins over a terminal", []string{"--dump"}, true, true, ModeDump},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := mkHome(t)
			mkProfiles(t, home, ".claude")
			cmd, _, err := resolve(t, tt.argv, home, func(s *System) { s.StdinTTY, s.StdoutTTY = tt.stdin, tt.stdout })
			if err != nil || cmd.Mode != tt.want {
				t.Errorf("%+v, %v", cmd, err)
			}
		})
	}

	t.Run("invalid directory: ModeInvalid", func(t *testing.T) {
		home := mkHome(t)
		mustMkdir(t, filepath.Join(home, ".claude"))
		cmd, stderr, err := resolve(t, nil, home, nil)
		if err != nil || cmd.Mode != ModeInvalid || !strings.Contains(stderr, "error [claude]") {
			t.Errorf("%+v, %q, %v", cmd, stderr, err)
		}
	})
}

func TestResolveNoColor(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  config.Env
		want bool
	}{
		{"unset", config.Env{}, false},
		{"empty is ignored", config.Env{"NO_COLOR": ""}, false},
		{"1", config.Env{"NO_COLOR": "1"}, true},
		{"any non-empty value", config.Env{"NO_COLOR": "false"}, true},
		{"FORCE_COLOR alone does not matter", config.Env{"FORCE_COLOR": "1"}, false},
		{"NO_COLOR wins over FORCE_COLOR", config.Env{"NO_COLOR": "1", "FORCE_COLOR": "1"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := mkHome(t)
			mkProfiles(t, home, ".claude")
			cmd, _, err := resolve(t, nil, home, func(s *System) { s.Env = tt.env })
			if err != nil || cmd.NoColor != tt.want {
				t.Errorf("NoColor = %v, want %v (%v)", cmd.NoColor, tt.want, err)
			}
		})
	}
}

func TestMainExitCodes(t *testing.T) {
	t.Run("without a terminal: warns, renders one frame and exits 0", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		writeConfig(t, home, "", "")
		r := runCLI(t, nil, home, nil)
		// A long path wraps at 80 columns.
		if r.code != 0 || r.stderr != "agtop: stdin is not a terminal; rendering once\n" ||
			!strings.Contains(r.stdout, "agtop") || !strings.Contains(strings.ReplaceAll(r.stdout, "\n", ""), filepath.Join(home, ".claude")) {
			t.Errorf("%+v", r)
		}
	})
	t.Run("stdin terminal, stdout redirected names stdout", func(t *testing.T) {
		home := mkHome(t)
		mkProfiles(t, home, ".claude")
		var stderr bytes.Buffer
		sys := System{Env: config.Env{}, Homedir: home, Cwd: home, FS: config.OSFS{}, Stderr: &stderr, StdinTTY: true}
		Main(nil, sys, &bytes.Buffer{})
		if !strings.Contains(stderr.String(), "agtop: stdout is not a terminal; rendering once\n") {
			t.Errorf("stderr %q", stderr.String())
		}
	})
	t.Run("--help and setup --help document setup, --remove, --yes and --dry-run", func(t *testing.T) {
		for _, args := range [][]string{{"--help"}, {"setup", "--help"}} {
			r := runCLI(t, args, mkHome(t), nil)
			if r.code != 0 {
				t.Fatalf("%v: %+v", args, r)
			}
			for _, want := range []string{"agtop setup [--remove] [--yes] [--dry-run]", "agtop statusline-hook"} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("%v lacks %q", args, want)
				}
			}
			for _, re := range []string{`(?m)^  agtop setup --remove `, `(?m)^  --yes, -y `, `(?m)^  --dry-run `} {
				if !regexpMatch(re, r.stdout) {
					t.Errorf("%v lacks %s", args, re)
				}
			}
		}
	})
}

func regexpMatch(re, s string) bool { return regexp.MustCompile(re).MatchString(s) }

func TestResolveValidatesByProvider(t *testing.T) {
	home := mkHome(t)
	mustMkdir(t, filepath.Join(home, ".claude", "projects"))
	mustMkdir(t, filepath.Join(home, ".claude", "sessions"))
	oc := filepath.Join(home, "oc")
	mustMkdir(t, oc)
	writeConfig(t, home, "[profiles]\nc = \"~/.claude\"\noc = { provider = \"opencode\", dir = \"~/oc\" }\n", "")

	cmd, stderr, err := resolve(t, nil, home, nil)
	if err != nil || cmd.Mode != ModeInvalid || !strings.Contains(stderr, "agtop: error [oc]: "+oc+" does not look like an opencode data directory: missing opencode.db") {
		t.Errorf("%+v, %q, %v", cmd.Mode, stderr, err)
	}
	if strings.Contains(stderr, "[c]") {
		t.Errorf("the Claude profile was reported: %q", stderr)
	}

	if err := os.WriteFile(filepath.Join(oc, "opencode.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, stderr, err = resolve(t, nil, home, nil)
	if err != nil || cmd.Mode == ModeInvalid || stderr != "" {
		t.Errorf("%+v, %q, %v", cmd.Mode, stderr, err)
	}
	if len(cmd.Profiles) != 2 || cmd.Profiles[1].Provider != domain.Opencode {
		t.Errorf("profiles %+v", cmd.Profiles)
	}
}

func TestResolveRejectsBadSystem(t *testing.T) {
	home := mkHome(t)
	for _, tt := range []struct {
		name string
		mut  func(*System)
		want string
	}{
		{"empty home", func(s *System) { s.Homedir = "" }, "home directory"},
		{"relative home", func(s *System) { s.Homedir = "rel/home" }, "home directory"},
		{"home lookup failed", func(s *System) { s.InitErr = errors.New("$HOME is not defined") }, "$HOME is not defined"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolve(t, []string{"--dump"}, home, tt.mut)
			var ce *config.Error
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v", err)
			}
		})
	}
	for _, argv := range [][]string{{"--help"}, {"--version"}, {"statusline-hook"}} {
		t.Run("no home needed for "+argv[0], func(t *testing.T) {
			_, _, err := resolve(t, argv, home, func(s *System) { s.Homedir = "" })
			if err != nil {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestResolveRejectsEmptyConfig(t *testing.T) {
	home := mkHome(t)
	for _, argv := range [][]string{{"--config="}, {"--config", ""}, {"setup", "--config="}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			_, _, err := resolve(t, argv, home, nil)
			if err == nil || !strings.Contains(err.Error(), "'--config' needs a non-empty path") {
				t.Errorf("got %v", err)
			}
		})
	}
}
