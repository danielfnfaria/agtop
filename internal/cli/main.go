package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/danielfnfaria/agtop/internal/cli/hook"
	"github.com/danielfnfaria/agtop/internal/cli/setup"
	"github.com/danielfnfaria/agtop/internal/ui"
	"github.com/danielfnfaria/agtop/internal/version"
)

// runUI runs the viewer (once or interactive) and returns the exit code.
var runUI = func(cmd Command, sys System, stdout io.Writer) int {
	opts := ui.Options{Profiles: cmd.Profiles, Appearance: cmd.Appearance, Style: styleMode(cmd, sys)}
	if cmd.Mode == ModeOnce {
		return renderOnce(ui.RenderOnce, opts, sys.Stderr, stdout)
	}
	code, err := ui.Run(context.Background(), opts)
	if err != nil {
		return renderFailed(sys.Stderr, err)
	}
	return code
}

func renderOnce(render func(ui.Options) string, opts ui.Options, stderr, stdout io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = renderFailed(stderr, fmt.Errorf("%v\n%s", r, debug.Stack()))
		}
	}()
	fmt.Fprint(stdout, render(opts))
	return 0
}

// renderFailed reports a render error (the terminal is already restored).
func renderFailed(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "agtop: error while rendering: %v\n", err)
	return 1
}

// styleMode follows chalk for the colors, as the Node build did: NO_COLOR (non-empty) and
// FORCE_COLOR=0 turn them off, any other FORCE_COLOR forces them, and otherwise stdout must be a
// terminal that is not TERM=dumb. Without colors, a terminal keeps bold, dim and inverse; output
// that is not a terminal (or TERM=dumb) gets no SGR at all.
func styleMode(cmd Command, sys System) ui.StyleMode {
	_, forced := sys.Env["FORCE_COLOR"]
	if !cmd.NoColor && ui.ColorEnabled(sys.Env) && forced {
		return ui.StyleFull
	}
	if !sys.StdoutTTY || sys.Env["TERM"] == "dumb" {
		return ui.StyleNone
	}
	if cmd.NoColor || !ui.ColorEnabled(sys.Env) {
		return ui.StyleAttributes
	}
	return ui.StyleFull
}

// Main runs the command line and returns the exit code.
func Main(argv []string, sys System, stdout io.Writer) int {
	cmd, err := Resolve(argv, sys)
	if err != nil {
		fmt.Fprintf(sys.Stderr, "agtop: %v\n", err)
		return 1
	}
	switch cmd.Mode {
	case ModeHelp:
		fmt.Fprint(stdout, Help)
		return 0
	case ModeVersion:
		fmt.Fprintln(stdout, version.Version)
		return 0
	case ModeInvalid:
		return 1
	case ModeHook:
		return runHook(cmd, sys, stdout)
	case ModeSetup:
		return runSetup(cmd, sys, stdout)
	case ModeDump:
		return Dump(cmd.Profiles, stdout)
	case ModeOnce:
		which := "stdout"
		if !sys.StdinTTY {
			which = "stdin"
		}
		fmt.Fprintf(sys.Stderr, "agtop: %s is not a terminal; rendering once\n", which)
	}
	return runUI(cmd, sys, stdout)
}

func runHook(cmd Command, sys System, stdout io.Writer) int {
	// A write to a closed pipe must fail with EPIPE, not kill the hook with SIGPIPE.
	signal.Ignore(syscall.SIGPIPE)
	stdin := sys.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	return hook.Run(hook.IO{
		Stdin:           stdin,
		Stdout:          stdout,
		Stderr:          sys.Stderr,
		ClaudeConfigDir: sys.Env["CLAUDE_CONFIG_DIR"],
		Homedir:         sys.Homedir,
		Debug:           sys.Env["AGTOP_DEBUG"] != "",
		NowMs:           func() int64 { return sys.now().UnixMilli() },
	}, cmd.Print)
}

func runSetup(cmd Command, sys System, stdout io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(sys.Stderr, "agtop: cannot locate the running executable: %v\n", err)
		return 1
	}
	stdin := sys.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	return setup.Run(cmd.Profiles, cmd.Setup, setup.IO{
		Stdout: stdout,
		Stderr: sys.Stderr,
		IsTTY:  sys.StdinTTY,
		Confirm: func(question string) bool {
			fmt.Fprint(stdout, question)
			line, _ := bufio.NewReader(stdin).ReadString('\n')
			return setup.IsYes(line)
		},
		Cwd:  sys.Cwd,
		Now:  sys.now,
		Hook: setup.ResolveHook(exe, sys.Env["PATH"]),
	})
}
