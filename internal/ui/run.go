package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/danielfnfaria/agtop/internal/closer"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// Options configure the viewer.
type Options struct {
	Profiles   []config.ProfileDir
	Appearance style.Appearance
	// Style is how much SGR the output carries; the zero value is StyleAttributes.
	Style StyleMode
	// Links turns PR numbers into OSC 8 hyperlinks; Run sets it (a terminal is required).
	Links bool
	// Store is the snapshot source; Run creates a store over Profiles when nil.
	Store Source
	// Closer closes a session (validates and sends SIGTERM); Run creates a real one when nil.
	Closer Closer
	// Now is the clock in epoch ms (tests); default time.Now.
	Now func() int64
	// NoticeMS is how long a notice stays; default 4000.
	NoticeMS int
	// Location is the zone of clock times; nil is local time.
	Location *time.Location
	// Input and Output are the terminal; default os.Stdin and os.Stdout.
	Input  io.Reader
	Output io.Writer
	// Stderr receives, after the screen is restored, the messages held during the full screen;
	// default os.Stderr.
	Stderr io.Writer

	done <-chan struct{}
	// storeDeps are the collaborators of the store Run creates (tests).
	storeDeps store.Deps
}

// signalCodes are the signals that end the viewer, with the conventional exit codes.
var signalCodes = map[os.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143, syscall.SIGHUP: 129}

// StyleMode is how much SGR styling the output carries.
type StyleMode int

const (
	// StyleAttributes drops the colors and keeps bold, dim and inverse (colors disabled by the
	// user on a terminal, so the selection and stale boxes stay visible).
	StyleAttributes StyleMode = iota
	// StyleFull paints colors and attributes.
	StyleFull
	// StyleNone emits no SGR at all (output that is not a terminal).
	StyleNone
)

// ColorEnabled reports whether the environment allows colors: NO_COLOR (non-empty) or
// FORCE_COLOR=0 turn them off.
func ColorEnabled(env map[string]string) bool {
	if env["NO_COLOR"] != "" {
		return false
	}
	switch env["FORCE_COLOR"] {
	case "0", "false":
		return false
	}
	return true
}

// RenderOnce is the single frame of the "once" mode (no terminal): the watched directories at 80
// columns, ending with a newline.
func RenderOnce(o Options) string {
	o.Store = nil
	a := newApp(o, 80, 0)
	return a.painter().loadingScreen(a.render, 80, a.profiles, false) + "\n"
}

// Run shows the full-screen viewer until the user quits, ctx is done or a signal arrives (exit
// code 130, 143 or 129). Messages written to os.Stderr meanwhile are held and written to
// o.Stderr once the terminal is restored. A panic in the store ends the viewer with exit code 1
// and the panic, with its stack, on o.Stderr. err is a failure of the terminal or of a render.
func Run(ctx context.Context, o Options) (exitCode int, err error) {
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	// Before the store starts: nothing of it may see the swap half done.
	held := captureStderr()
	var st *store.Store
	if o.Store == nil {
		deps := o.storeDeps
		deps.Log = held.heldOutput
		st = store.New(store.DefaultOptions(o.Profiles), deps)
		o.Store = st
	}
	if o.Closer == nil {
		o.Closer = closer.New(closer.NewDeps())
	}
	done := make(chan struct{})
	o.done = done
	o.Links = true

	w, h := terminalSize(o.Output)
	app := newApp(o, w, h)
	opts := []tea.ProgramOption{tea.WithoutSignalHandler(), tea.WithWindowSize(w, h)}
	if o.Input != nil {
		opts = append(opts, tea.WithInput(o.Input))
	}
	if o.Output != nil {
		opts = append(opts, tea.WithOutput(o.Output))
	}
	p := tea.NewProgram(app, opts...)
	held.onCount(func(n int) { p.Send(retainedMsg{n}) })

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case s := <-sig:
			p.Send(signalMsg{signalCodes[s]})
		case <-ctx.Done():
			p.Quit()
		case <-done:
		}
	}()
	if st != nil {
		go st.Start()
		go func() {
			select {
			case <-st.Done():
				if st.Err() != nil {
					p.Quit()
				}
			case <-done:
			}
		}()
	}

	_, runErr := p.Run()
	close(done)
	signal.Stop(sig)
	quiet := true
	if st != nil {
		st.Stop()
		select {
		case <-st.Done():
		case <-time.After(2 * time.Second):
			quiet = false
		}
	}
	if text := held.release(quiet); text != "" {
		_, _ = io.WriteString(stderr, text)
	}
	if st != nil && st.Err() != nil {
		_, _ = fmt.Fprintf(stderr, "agtop: %v\n", st.Err())
		return 1, nil
	}
	if runErr != nil {
		return 1, runErr
	}
	return app.exitCode, nil
}

// terminalSize is the size of the output terminal, 80x24 when it is not one.
func terminalSize(out io.Writer) (width, height int) {
	f, ok := out.(*os.File)
	if out == nil {
		f, ok = os.Stdout, true
	}
	if ok {
		if w, h, err := term.GetSize(f.Fd()); err == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 80, 24
}
