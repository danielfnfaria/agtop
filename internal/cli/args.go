package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielfnfaria/agtop/internal/cli/setup"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// Mode is what the CLI does with the given arguments.
type Mode int

const (
	// ModeHelp prints the help.
	ModeHelp Mode = iota
	// ModeVersion prints the version.
	ModeVersion
	// ModeInvalid: some directory failed validation (already reported on stderr).
	ModeInvalid
	// ModeDump runs one scan and prints a JSON summary.
	ModeDump
	// ModeHook is `statusline-hook`.
	ModeHook
	// ModeSetup is `setup`.
	ModeSetup
	// ModeOnce renders a single frame (no terminal on stdin or stdout).
	ModeOnce
	// ModeInteractive is the full-screen viewer.
	ModeInteractive
)

// Command is the resolved command line.
type Command struct {
	Mode     Mode
	Profiles []config.ProfileDir
	// Appearance is the theme, glyphs and layout, fully resolved (viewer modes).
	Appearance style.Appearance
	// NoColor: a non-empty NO_COLOR turns every foreground and background color off.
	NoColor bool
	// Print is `statusline-hook --print`.
	Print bool
	// Setup holds the flags of `setup`.
	Setup setup.Options
}

// System is what resolving the command line reads from the environment.
type System struct {
	Env     config.Env
	Homedir string
	Cwd     string
	// InitErr is why Homedir or Cwd could not be found, if it failed.
	InitErr error
	FS      config.FS
	Stdin   io.Reader
	Stderr  io.Writer
	// StdinTTY and StdoutTTY say whether the process has a terminal on each.
	StdinTTY  bool
	StdoutTTY bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (s System) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// OSSystem is the System of the running process. A failure to find the home directory or the
// cwd is kept in InitErr, for Resolve to report.
func OSSystem() System {
	home, homeErr := os.UserHomeDir()
	cwd, cwdErr := os.Getwd()
	return System{
		Env: config.EnvFromOS(), Homedir: home, Cwd: cwd, InitErr: errors.Join(homeErr, cwdErr), FS: config.OSFS{},
		Stdin: os.Stdin, Stderr: os.Stderr,
		StdinTTY: isTerminal(os.Stdin), StdoutTTY: isTerminal(os.Stdout),
	}
}

// Flags removed in 0.2 (replaced by the config file) and the envs that went with them.
var removedFlags = []string{"--claude-dir", "--context-window"}

const (
	envClaudeDirs     = "AGTOP_CLAUDE_DIRS"
	envContextWindows = "AGTOP_CONTEXT_WINDOWS"
)

type flags struct {
	dump    bool
	version bool
	config  *string
	remove  bool
	yes     bool
	dryRun  bool
}

// Subcommands.
const (
	cmdSetup = "setup"
	cmdHook  = "statusline-hook"
	printArg = "--print"
)

// Resolve parses argv and resolves the configuration. Directory warnings and errors go to
// stderr. It fails on unknown flags or invalid values (the caller prints the error and exits 1).
func Resolve(argv []string, sys System) (Command, error) {
	if len(argv) > 0 && argv[0] == cmdHook {
		// The hook never fails: any other argument is ignored.
		return Command{Mode: ModeHook, Print: slices.Contains(argv[1:], printArg)}, nil
	}
	setupMode := len(argv) > 0 && argv[0] == cmdSetup
	if setupMode {
		argv = argv[1:]
	}
	if wantsHelp(argv) {
		return Command{Mode: ModeHelp}, nil
	}
	if err := rejectRemovedFlags(argv, sys); err != nil {
		return Command{}, err
	}
	f, err := parseFlags(argv, setupMode)
	if err != nil {
		return Command{}, err
	}
	if f.version {
		return Command{Mode: ModeVersion}, nil
	}
	// Without an absolute home, the default config path would be relative to the cwd.
	if sys.InitErr != nil {
		return Command{}, &config.Error{Msg: sys.InitErr.Error()}
	}
	if !filepath.IsAbs(sys.Homedir) {
		return Command{}, &config.Error{Msg: fmt.Sprintf("cannot find the home directory (got %q): set $HOME to an absolute path", sys.Homedir)}
	}
	if setupMode {
		// The directories are not validated like the viewer's: a fresh profile may have no
		// projects/ yet; setup skips the ones that do not exist.
		profiles, _, _, _, err := loadRunConfig(f.config, sys)
		if err != nil {
			return Command{}, err
		}
		return Command{Mode: ModeSetup, Profiles: profiles, Setup: setup.Options{Remove: f.remove, Yes: f.yes, DryRun: f.dryRun}}, nil
	}
	profiles, appearance, configPath, fileLoaded, err := loadRunConfig(f.config, sys)
	if err != nil {
		return Command{}, err
	}
	if !validateProfiles(profiles, sys) {
		return Command{Mode: ModeInvalid}, nil
	}
	if f.config == nil && !fileLoaded {
		config.InitConfigFile(sys.FS, sys.Env, configPath, profiles, sys.Homedir, sys.Stderr)
	}
	cmd := Command{Profiles: profiles, Appearance: appearance, NoColor: sys.Env["NO_COLOR"] != ""}
	switch {
	case f.dump:
		cmd.Mode = ModeDump
	case sys.StdinTTY && sys.StdoutTTY:
		cmd.Mode = ModeInteractive
	default:
		cmd.Mode = ModeOnce
	}
	return cmd, nil
}

var errEmptyConfig = &config.Error{Msg: "option '--config' needs a non-empty path"}

func parseFlags(argv []string, setupMode bool) (flags, error) {
	var f flags
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--dump" && !setupMode:
			f.dump = true
		case arg == "--version" && !setupMode:
			f.version = true
		case arg == "--remove" && setupMode:
			f.remove = true
		case (arg == "--yes" || arg == "-y") && setupMode:
			f.yes = true
		case arg == "--dry-run" && setupMode:
			f.dryRun = true
		case arg == "--config":
			if i+1 >= len(argv) || strings.HasPrefix(argv[i+1], "-") {
				return f, &config.Error{Msg: "option '--config <value>' argument missing"}
			}
			i++
			v := argv[i]
			if v == "" {
				return f, errEmptyConfig
			}
			f.config = &v
		case strings.HasPrefix(arg, "--config="):
			v := strings.TrimPrefix(arg, "--config=")
			if v == "" {
				return f, errEmptyConfig
			}
			f.config = &v
		case strings.HasPrefix(arg, "-"):
			return f, &config.Error{Msg: fmt.Sprintf("unknown option '%s'", arg)}
		default:
			return f, &config.Error{Msg: fmt.Sprintf("unexpected argument '%s'", arg)}
		}
	}
	return f, nil
}

// loadRunConfig reads the config file (--config, else the default path) and resolves the
// profiles.
func loadRunConfig(configFlag *string, sys System) (profiles []config.ProfileDir, appearance style.Appearance, configPath string, fileLoaded bool, err error) {
	loaded, err := config.LoadFile(configFlag, sys.FS, config.LoadOptions{Env: sys.Env, Homedir: sys.Homedir, Cwd: sys.Cwd})
	if err != nil {
		return nil, style.Appearance{}, "", false, err
	}
	configPath = loaded.File
	if configPath == "" {
		configPath = config.DefaultConfigPath(sys.Env, sys.Homedir)
	}
	warnRemovedEnvs(configPath, sys)
	profiles, err = config.ResolveProfiles(config.ResolveInput{
		Profiles:   loaded.Profiles,
		Env:        sys.Env,
		Homedir:    sys.Homedir,
		FS:         sys.FS,
		Cwd:        sys.Cwd,
		ConfigPath: configPath,
	})
	return profiles, style.ResolveAppearance(loaded.Appearance), configPath, loaded.File != "", err
}

// wantsHelp: --help/-h anywhere wins, even over a removed or unknown flag.
func wantsHelp(argv []string) bool {
	for _, a := range argv {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// configFlagValue is the value of --config in argv (--config x or --config=x), if given.
func configFlagValue(argv []string) (string, bool) {
	for i, a := range argv {
		if a == "--config" {
			if i+1 < len(argv) {
				return argv[i+1], true
			}
			return "", false
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok {
			return v, true
		}
	}
	return "", false
}

// rejectRemovedFlags fails with a pointer to the config file, before the generic unknown-flag
// error.
func rejectRemovedFlags(argv []string, sys System) error {
	for _, arg := range argv {
		name, _, _ := strings.Cut(arg, "=")
		for _, flag := range removedFlags {
			if name != flag {
				continue
			}
			if flag == "--context-window" {
				return &config.Error{Msg: flag + " was removed: context windows are now detected automatically"}
			}
			configPath := config.DefaultConfigPath(sys.Env, sys.Homedir)
			if given, ok := configFlagValue(argv); ok && given != "" {
				configPath = config.ExpandHome(given, sys.Homedir)
				if !filepath.IsAbs(configPath) {
					configPath = filepath.Join(sys.Cwd, configPath)
				}
			}
			return &config.Error{Msg: flag + " was removed: list the profiles in " + configPath + " under [profiles] (see --help)"}
		}
	}
	return nil
}

func warnRemovedEnvs(configPath string, sys System) {
	warn := func(name, hint string) {
		if _, ok := sys.Env[name]; ok {
			fmt.Fprintf(sys.Stderr, "agtop: warning: %s is ignored since agtop 0.2: %s\n", name, hint)
		}
	}
	warn(envClaudeDirs, "use "+configPath)
	warn(envContextWindows, "context windows are now detected automatically")
}

// validateProfiles reports each directory's warnings and errors on stderr; false if any has an
// error.
func validateProfiles(profiles []config.ProfileDir, sys System) bool {
	ok := true
	for _, p := range profiles {
		res := config.ValidateProfile(p, sys.FS)
		for _, w := range res.Warnings {
			fmt.Fprintf(sys.Stderr, "agtop: warning [%s]: %s\n", p.Label, w)
		}
		if res.Error != "" {
			fmt.Fprintf(sys.Stderr, "agtop: error [%s]: %s\n", p.Label, res.Error)
			ok = false
		}
	}
	return ok
}
