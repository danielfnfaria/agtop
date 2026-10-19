package ui

import (
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/closer"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// collect runs cmd (and the commands of a batch) and returns the messages that arrive within
// wait; blocking commands (store wait, clock) are left behind.
func collect(cmd tea.Cmd, wait time.Duration) []tea.Msg {
	if cmd == nil {
		return nil
	}
	out := make(chan tea.Msg, 64)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, b := range batch {
					if b != nil {
						run(b)
					}
				}
				return
			}
			out <- msg
		}()
	}
	run(cmd)
	var msgs []tea.Msg
	deadline := time.After(wait)
	for {
		select {
		case m := <-out:
			msgs = append(msgs, m)
		case <-deadline:
			return msgs
		}
	}
}

func hasMsg[T any](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func TestQuitKeys(t *testing.T) {
	for _, keys := range [][]string{{"q"}, {"ctrl+c"}, {"right", "enter", "q"}, {"?", "q"}} {
		a := newApp(fiveStore(), 100, 30)
		cmd := press(a, keys...)
		if _, ok := hasMsg[tea.QuitMsg](collect(cmd, 50*time.Millisecond)); !ok {
			t.Errorf("%v does not quit", keys)
		}
	}
	for _, k := range []string{"esc", "x", "backspace"} {
		a := newApp(fiveStore(), 100, 30)
		if _, ok := hasMsg[tea.QuitMsg](collect(press(a, k), 50*time.Millisecond)); ok {
			t.Errorf("%s quits", k)
		}
	}
}

func TestRedrawSuspendAndSignals(t *testing.T) {
	a := newApp(fiveStore(), 100, 30)
	if reflect.TypeOf(press(a, "ctrl+l")()) != reflect.TypeOf(tea.ClearScreen()) {
		t.Error("ctrl+l does not clear the screen")
	}
	if _, ok := press(a, "ctrl+z")().(tea.SuspendMsg); !ok {
		t.Error("ctrl+z does not suspend")
	}
	_, cmd := a.Update(signalMsg{143})
	if _, ok := cmd().(tea.QuitMsg); !ok || a.exitCode != 143 {
		t.Errorf("signal: exit code %d", a.exitCode)
	}
	want := map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143, syscall.SIGHUP: 129}
	for s, code := range want {
		if signalCodes[s] != code {
			t.Errorf("%v: %d, want %d", s, signalCodes[s], code)
		}
	}
}

func TestStoreUpdatesAndSessionGoneNotice(t *testing.T) {
	st := newFakeStore(snapshot([]*store.SessionView{sessionWithAgents()}, profiles))
	o := testOptions(st, profiles)
	o.NoticeMS = 5
	a := newApp(o, 100, 30)
	press(a, "right", "enter")
	mustContain(t, screen(a), "cwd /home/u/projects/detail-proj")

	pending := a.waitSnapshot()
	st.set(snapshot(fiveSessions(), profiles))
	msg, ok := hasMsg[snapshotMsg](collect(pending, 200*time.Millisecond))
	if !ok {
		t.Fatal("no snapshot message after the store emitted")
	}
	_, cmd := a.Update(msg)
	frame := screen(a)
	mustContain(t, frame, model.SessionGoneNotice, "Alpha task")
	lines := strings.Split(frame, "\n")
	if len(lines) != 29 || !strings.HasPrefix(lines[28], " agtop") {
		t.Errorf("the notice pushed the status bar:\n%s", frame)
	}
	notice, ok := hasMsg[noticeMsg](collect(cmd, 100*time.Millisecond))
	if !ok {
		t.Fatal("no notice timer")
	}
	a.Update(notice)
	mustNotContain(t, screen(a), model.SessionGoneNotice)
}

func TestResizeKeepsFrameSize(t *testing.T) {
	a := newApp(fiveStore(), 100, 30)
	for _, size := range [][2]int{{80, 24}, {40, 14}, {39, 14}, {120, 40}, {100, 30}} {
		a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(screen(a), "\n")
		if len(lines) != size[1]-1 {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > size[0] {
				t.Errorf("%v: line wider than the terminal: %q", size, l)
			}
		}
	}
}

func TestFramesFitTheTerminal(t *testing.T) {
	for _, sc := range scenarios() {
		a := newApp(sc.opts(), sc.w, sc.h)
		press(a, sc.keys...)
		lines := strings.Split(a.Render(), "\n")
		if len(lines) != sc.h-1 {
			t.Errorf("%s: %d lines, want %d", sc.name, len(lines), sc.h-1)
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > sc.w {
				t.Errorf("%s: line of %d columns: %q", sc.name, w, l)
			}
		}
	}
}

// sgrParams are the parameters of every SGR sequence in s.
func sgrParams(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindAllStringSubmatch(s, -1) {
		out = append(out, strings.Split(m[1], ";")...)
	}
	return out
}

func hasColorSGR(s string) bool {
	for _, p := range sgrParams(s) {
		n, err := strconv.Atoi(p)
		if err == nil && (n >= 30 && n <= 39 || n >= 90 && n <= 97 || n >= 40 && n <= 49) {
			return true
		}
	}
	return false
}

func TestNoColorKeepsBoldDimInverse(t *testing.T) {
	o := fiveStore()
	o.Appearance = style.ResolveAppearance(&style.AppearanceConfig{
		Theme: &style.ThemeConfig{Colors: map[string]string{"busy": "#ff0000"}}})
	a := newApp(o, 120, 40)
	press(a, "right")
	frame := a.Render()
	if hasColorSGR(frame) {
		t.Error("color SGR without color")
	}
	params := strings.Join(sgrParams(frame), " ")
	for _, want := range []string{"1", "2", "7"} {
		if !slices.Contains(sgrParams(frame), want) {
			t.Errorf("no SGR %s (bold, dim, inverse) without color: %s", want, params)
		}
	}
	o.Style = StyleFull
	a = newApp(o, 120, 40)
	frame = a.Render()
	if !strings.Contains(frame, "38;2;255;0;0") {
		t.Error("hex busy color not painted as truecolor")
	}
	if strings.Contains(frame, "\x1b[m") || !strings.Contains(frame, "\x1b[39m") || !strings.Contains(frame, "\x1b[22m") {
		t.Error("styles are not closed by their own resets")
	}
}

func TestNoColorStatusBarStaysReadable(t *testing.T) {
	snap := snapshot(fiveSessions(), profiles)
	snap.Errors = []string{"e1"}
	a := newApp(testOptions(newFakeStore(snap), profiles), 180, 30)
	lines := strings.Split(a.Render(), "\n")
	bar := lines[len(lines)-1]
	if hasColorSGR(bar) {
		t.Errorf("color SGR in the status bar: %q", bar)
	}
	bar = stripANSI(bar)
	sep := " \u2502 "
	if !strings.HasPrefix(bar, " agtop "+sep+"5 sessions \u25cf 1 busy") {
		t.Errorf("bar: %q", bar)
	}
	if !regexp.MustCompile(sep + "\u2717 1 error {2,}\u21b5 open").MatchString(bar) {
		t.Errorf("bar lacks the error count before the hints: %q", bar)
	}
	if !strings.HasSuffix(strings.TrimRight(bar, " "), "? help  q quit") {
		t.Errorf("bar: %q", bar)
	}
}

func TestColorEnabled(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, true},
		{map[string]string{"NO_COLOR": "1"}, false},
		{map[string]string{"FORCE_COLOR": "0"}, false},
		{map[string]string{"FORCE_COLOR": "1"}, true},
	}
	for _, c := range cases {
		if got := ColorEnabled(c.env); got != c.want {
			t.Errorf("%v: %v", c.env, got)
		}
	}
}

func prSession() *store.SessionView {
	return session("claude:pr", func(s *store.SessionView) {
		s.PRs = []domain.PullRequestRef{
			{Owner: "o", Repo: "r", Number: 7, URL: "https://github.com/o/r/pull/7"},
			{Owner: "o", Repo: "r", Number: 8, URL: "https://github.com/o/r/pull/8\x1b]8;;evil"},
			{Owner: "o", Repo: "r", Number: 9, URL: "https://github.com/o/r/pull/9"},
		}
	})
}

func TestPRLinksOnlyOnTTYAndValidated(t *testing.T) {
	o := testOptions(newFakeStore(snapshot([]*store.SessionView{prSession()}, profiles)), profiles)
	a := newApp(o, 100, 30)
	frame := a.Render()
	mustContain(t, frame, "#9  #8  #7")
	mustNotContain(t, frame, "\x1b]8;")

	o.Links = true
	a = newApp(o, 100, 30)
	frame = a.Render()
	mustContain(t, frame, "\x1b]8;;https://github.com/o/r/pull/9\x07#9\x1b]8;;\x07",
		"\x1b]8;;https://github.com/o/r/pull/7\x07#7\x1b]8;;\x07")
	mustNotContain(t, frame, "evil", "pull/8")
}

func TestPRsOverflowShowPlusN(t *testing.T) {
	s := session("claude:prs", func(s *store.SessionView) {
		for i := 1000; i < 1012; i++ {
			s.PRs = append(s.PRs, domain.PullRequestRef{Number: i, URL: "https://github.com/o/r/pull/x"})
		}
	})
	a := newApp(testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles), 100, 30)
	mustContain(t, screen(a), "#1011  #1010  #1009  #1008  +8")
}

func TestSpinnerTicksOnlyWhileVisible(t *testing.T) {
	a := newApp(fiveStore(), 100, 30)
	if !a.spinning {
		t.Fatal("busy session on screen but no spinner tick")
	}
	if _, ok := hasMsg[spinMsg](collect(a.Init(), 300*time.Millisecond)); !ok {
		t.Error("Init does not start the spinner tick")
	}
	press(a, "p", "p") // only claude-x: an idle session
	if a.spinning {
		t.Error("spinner tick with no spinner on screen")
	}
	press(a, "p", "?")
	if a.spinning {
		t.Error("spinner tick behind the help")
	}
	seq := a.spinSeq
	_, cmd := a.Update(spinMsg{seq})
	if _, ok := hasMsg[spinMsg](collect(cmd, 300*time.Millisecond)); ok {
		t.Error("stale spinner tick rescheduled")
	}
}

func TestSpinnerFrameAdvances(t *testing.T) {
	clock := now
	o := fiveStore()
	o.Now = func() int64 { return clock }
	a := newApp(o, 100, 30)
	before := screen(a)
	clock += style.SpinnerFrameMS
	a.Update(spinMsg{a.spinSeq})
	after := screen(a)
	mustContain(t, before, "⠋")
	mustContain(t, after, "⠙")
}

func TestASCIIPresetDrawsOnlyASCII(t *testing.T) {
	long := sessionWithAgents()
	long.Agents["a-long"] = agent(func(a *store.AgentView) {
		a.Type, a.Description, a.StartedAt = strings.Repeat("very-long-agent-name-", 4), strings.Repeat("desc ", 30), now-1000
	})
	long.DisplayTitle = strings.Repeat("A very long title ", 6)
	o := withAppearance(testOptions(newFakeStore(snapshot([]*store.SessionView{long}, profiles)), profiles), asciiConfig())
	for _, keys := range [][]string{nil, {"right", "enter", "enter"}, {"?"}} {
		a := newApp(o, 100, 30)
		press(a, keys...)
		for _, r := range screen(a) {
			if r != '\n' && (r < 0x20 || r > 0x7e) {
				t.Errorf("%v: non-ASCII %q in the frame:\n%s", keys, r, screen(a))
				break
			}
		}
	}
}

func TestWideTitlesKeepBoxWidth(t *testing.T) {
	s := session("claude:cjk", func(s *store.SessionView) {
		s.DisplayTitle = "título\tcom\nquebra 漢字漢字漢字漢字漢字漢字漢字 🚀🚀"
	})
	a := newApp(testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles), 100, 30)
	var widths []int
	for _, l := range strings.Split(screen(a), "\n") {
		trimmed := strings.TrimLeft(l, " ")
		if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "╰") {
			widths = append(widths, ansi.StringWidth(l))
		}
	}
	for _, w := range widths {
		if w != widths[0] {
			t.Fatalf("box lines of different widths %v:\n%s", widths, screen(a))
		}
	}
}

func TestSingleProfileIgnoresP(t *testing.T) {
	a := newApp(singleStore(), 100, 30)
	before := screen(a)
	press(a, "p")
	if screen(a) != before {
		t.Error("p changed the screen with one profile")
	}
	mustNotContain(t, before, "p profile", "profile:")
	press(a, "?")
	mustContain(t, screen(a), "config /tmp/a/.claude")
	mustNotContain(t, screen(a), "filter by profile")
}

func TestHelpRecentErrors(t *testing.T) {
	snap := snapshot(fiveSessions(), profiles)
	for i := 0; i < 12; i++ {
		snap.Errors = append(snap.Errors, "read /tmp/f"+string(rune('a'+i))+".jsonl: EACCES")
	}
	a := newApp(testOptions(newFakeStore(snap), profiles), 100, 40)
	press(a, "?")
	mustContain(t, screen(a), "Recent errors (last 5 of 12)", "· read /tmp/fl.jsonl: EACCES")
	mustNotContain(t, screen(a), "/tmp/fa.jsonl")
}

func TestTreeScrollSurvivesDetailAndHelp(t *testing.T) {
	a := newApp(manyStore(), 80, 24)
	press(a, "down", "down", "down")
	before := screen(a)
	press(a, "enter", "esc", "?", "?")
	if got := screen(a); got != before {
		t.Errorf("the tree scroll jumped:\n%s\n---\n%s", got, before)
	}
}

func TestAbsoluteTimeAndLoading(t *testing.T) {
	a := newApp(Options{Profiles: profiles, Appearance: style.DefaultAppearance(), Now: func() int64 { return now }}, 80, 24)
	mustContain(t, screen(a), "Watched directories:", "sessions not loaded (no terminal)")
}

func TestHeldOutputText(t *testing.T) {
	h := &heldOutput{}
	if h.text() != "" {
		t.Error("text without writes")
	}
	_, _ = h.Write([]byte("one\n"))
	_, _ = h.Write([]byte("two"))
	want := "agtop: 2 messages held during full screen:\none\ntwo\n"
	if got := h.text(); got != want {
		t.Errorf("got %q", got)
	}
	chunked := &heldOutput{}
	_, _ = chunked.Write([]byte("a\nb\nc"))
	if n := chunked.count(); n != 3 {
		t.Errorf("one chunk of 2 lines and a partial: %d messages, want 3", n)
	}
	_, _ = chunked.Write([]byte("c\n"))
	if !strings.HasPrefix(chunked.text(), "agtop: 3 messages held during full screen:\na\nb\ncc\n") {
		t.Errorf("got %q", chunked.text())
	}
	var after strings.Builder
	passed := &heldOutput{}
	_, _ = passed.Write([]byte("held\n"))
	if got := passed.passthrough(&after); got != "agtop: 1 message held during full screen:\nheld\n" {
		t.Errorf("got %q", got)
	}
	_, _ = passed.Write([]byte("late\n"))
	if after.String() != "late\n" {
		t.Errorf("late write: %q", after.String())
	}
	big := &heldOutput{}
	for i := 0; i < captureMaxEntries+3; i++ {
		_, _ = big.Write([]byte("x\n"))
	}
	if !strings.HasSuffix(big.text(), "agtop: 3 messages dropped (buffer limit)\n") {
		t.Errorf("no dropped line: %q", big.text()[len(big.text())-60:])
	}
}

func TestHeldCountShowsInStatusBar(t *testing.T) {
	a := newApp(fiveStore(), 100, 30)
	a.Update(retainedMsg{2})
	mustContain(t, screen(a), "! 2 held messages")
}

func TestActivityUnknownShowsRawStatus(t *testing.T) {
	s := session("claude:u", func(s *store.SessionView) {
		s.Activity, s.RawStatus = discovery.ActivityUnknown, "compacting"
	})
	a := newApp(testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles), 100, 30)
	mustContain(t, screen(a), "● compacting · 5s ago")
}

func TestPendingBackgroundSessionShowsNotStarted(t *testing.T) {
	s := session("claude:bg", func(s *store.SessionView) {
		s.Kind = "background"
	})
	a := newApp(testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles), 100, 30)
	f := screen(a)
	mustContain(t, f, "● bg · not started")
	mustNotContain(t, f, "ended")
}

func TestPendingPredicate(t *testing.T) {
	cases := []struct {
		name string
		f    func(*store.SessionView)
		want bool
	}{
		{"background without transcript", func(s *store.SessionView) { s.Kind = "background" }, true},
		{"interactive without transcript", func(s *store.SessionView) { s.Kind = "interactive" }, false},
		{"sdk-cli without transcript", func(s *store.SessionView) { s.Kind = "sdk-cli" }, false},
		{"empty kind", func(s *store.SessionView) {}, false},
		{"with transcript", func(s *store.SessionView) { s.Kind, s.HasTranscript = "background", true }, false},
		{"stale background", func(s *store.SessionView) { s.Kind, s.Liveness = "background", store.Stale }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pending(session("x", c.f)); got != c.want {
				t.Errorf("pending() = %v, want %v", got, c.want)
			}
		})
	}
}

// closeApp builds the app for the close flow: one session with a known title and pid, and a
// fake closer that never signals.
func closeApp() (*App, *fakeCloser) {
	s := session("claude:s1", func(s *store.SessionView) {
		s.DisplayTitle, s.PID = "My task", 1234
	})
	fc := &fakeCloser{}
	o := testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles)
	o.Closer = fc
	return newApp(o, 100, 30), fc
}

func TestClosePrompt(t *testing.T) {
	a, fc := closeApp()
	press(a, "down", "x")
	mustContain(t, screen(a), `close "My task" (pid 1234)? y/Enter confirm, Esc/n cancel`)
	if len(fc.called) != 0 {
		t.Fatalf("closer called before confirmation: %+v", fc.called)
	}
}

func TestCloseConfirm(t *testing.T) {
	for _, confirmKey := range []string{"y", "enter"} {
		t.Run(confirmKey, func(t *testing.T) {
			a, fc := closeApp()
			cmd := press(a, "down", "x", confirmKey)
			if len(fc.called) != 0 {
				t.Fatalf("closer ran inside Update: %+v", fc.called)
			}
			mustNotContain(t, screen(a), "sent SIGTERM")
			deliver(t, a, cmd)
			if len(fc.called) != 1 {
				t.Fatalf("closer called %d times", len(fc.called))
			}
			want := closer.Card{ProfileDir: "/tmp/a/.claude", PID: 1234, SessionID: "claude:s1", Provider: domain.ClaudeCode}
			if fc.called[0] != want {
				t.Errorf("card = %+v, want %+v", fc.called[0], want)
			}
			mustContain(t, screen(a), `sent SIGTERM to "My task" (pid 1234)`)
		})
	}
}

func TestCloseCancel(t *testing.T) {
	for _, cancelKey := range []string{"esc", "n"} {
		t.Run(cancelKey, func(t *testing.T) {
			a, fc := closeApp()
			press(a, "down", "x", cancelKey)
			if len(fc.called) != 0 {
				t.Fatalf("closer called on cancel: %+v", fc.called)
			}
			f := screen(a)
			mustNotContain(t, f, "close ")
			mustContain(t, f, "My task")
		})
	}
}

func TestCloseIgnoresOtherKeysWhileConfirming(t *testing.T) {
	a, fc := closeApp()
	press(a, "down", "x", "down", "z", "tab")
	// Still confirming: the prompt is up and nothing was closed.
	mustContain(t, screen(a), `close "My task" (pid 1234)?`)
	if len(fc.called) != 0 {
		t.Fatalf("closer called by an unrelated key: %+v", fc.called)
	}
}

func TestCloseErrorShowsNotice(t *testing.T) {
	a, fc := closeApp()
	fc.err = errors.New("process is not alive")
	deliver(t, a, press(a, "down", "x", "y"))
	mustContain(t, screen(a), `cannot close "My task": process is not alive`)
}

// deliver runs the close command (the fake closer does no I/O, no timers) and feeds its result to
// the app, as the Bubble Tea runtime would.
func deliver(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no close command")
	}
	msg, ok := cmd().(closeResultMsg)
	if !ok {
		t.Fatalf("command did not return a closeResultMsg")
	}
	a.Update(msg)
}

func TestCloseWithoutSelectionDoesNothing(t *testing.T) {
	a, fc := closeApp()
	press(a, "x") // nothing selected yet
	if len(fc.called) != 0 {
		t.Fatalf("closer called: %+v", fc.called)
	}
	mustNotContain(t, screen(a), "close ")
}

func TestCloseUnavailableShowsNotice(t *testing.T) {
	s := session("claude:s1", func(s *store.SessionView) { s.DisplayTitle = "My task" })
	a := newApp(testOptions(newFakeStore(snapshot([]*store.SessionView{s}, profiles)), profiles), 100, 30)
	press(a, "down", "x", "y")
	mustContain(t, screen(a), `cannot close "My task": close is not available`)
}

func TestCloseCancelsWhenIdentityChanges(t *testing.T) {
	first := session("claude:s1", func(s *store.SessionView) {
		s.DisplayTitle, s.PID = "My task", 1234
	})
	st := newFakeStore(snapshot([]*store.SessionView{first}, profiles))
	fc := &fakeCloser{}
	o := testOptions(st, profiles)
	o.Closer = fc
	a := newApp(o, 100, 30)
	press(a, "down", "x")
	if a.nav.Close.Key == "" {
		t.Fatal("no pending confirmation")
	}
	// Same key, different identity under the cursor (title and pid changed).
	changed := session("claude:s1", func(s *store.SessionView) {
		s.DisplayTitle, s.PID = "Changed task", 9999
	})
	st.set(snapshot([]*store.SessionView{changed}, profiles))
	a.Update(snapshotMsg{snap: st.Snapshot()})
	if a.nav.Close.Key != "" {
		t.Fatalf("confirmation survived an identity change: %+v", a.nav.Close)
	}
	mustContain(t, screen(a), "close canceled")
	press(a, "y")
	if len(fc.called) != 0 {
		t.Fatalf("closer called after an identity change: %+v", fc.called)
	}
}

func TestCloseCancelsWhenSessionLeaves(t *testing.T) {
	s := session("claude:s1", func(s *store.SessionView) { s.DisplayTitle, s.PID = "My task", 1234 })
	st := newFakeStore(snapshot([]*store.SessionView{s}, profiles))
	fc := &fakeCloser{}
	o := testOptions(st, profiles)
	o.Closer = fc
	a := newApp(o, 100, 30)
	press(a, "down", "x")
	st.set(snapshot([]*store.SessionView{}, profiles))
	a.Update(snapshotMsg{snap: st.Snapshot()})
	if a.nav.Close.Key != "" {
		t.Fatalf("confirmation survived the session leaving: %+v", a.nav.Close)
	}
	press(a, "y")
	if len(fc.called) != 0 {
		t.Fatalf("closer called after the session left: %+v", fc.called)
	}
}

func TestCloseBlocksOnSmallTerminal(t *testing.T) {
	a, fc := closeApp()
	press(a, "down", "x")
	if a.nav.Close.Key == "" {
		t.Fatal("no pending confirmation")
	}
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	if a.nav.Close.Key != "" {
		t.Fatalf("confirmation survived a small terminal: %+v", a.nav.Close)
	}
	press(a, "y")
	if len(fc.called) != 0 {
		t.Fatalf("closer called on a small terminal: %+v", fc.called)
	}
}

func TestCloseNotStartedOnSmallTerminal(t *testing.T) {
	a, fc := closeApp()
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	press(a, "down", "x")
	if a.nav.Close.Key != "" {
		t.Fatalf("confirmation started on a small terminal: %+v", a.nav.Close)
	}
	if len(fc.called) != 0 {
		t.Fatalf("closer called: %+v", fc.called)
	}
}

func TestTreeIsMemoized(t *testing.T) {
	st := newFakeStore(snapshot([]*store.SessionView{sessionWithAgents(), session("claude:s2", nil)}, profiles))
	a := newApp(testOptions(st, profiles), 100, 30)
	fresh := func() treeModel {
		return buildTreeModel(a.snap, a.profiles, a.nav, a.width, a.appearance.Glyphs.Line)
	}
	same := func(step string) {
		t.Helper()
		if got, want := a.tree(), fresh(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: cached tree differs from a fresh build", step)
		}
	}

	same("initial")
	before := a.treeBuilds
	for range 5 {
		a.tree()
		a.Render()
	}
	// Timer messages and notices change nothing the tree reads.
	a.Update(spinMsg{seq: a.spinSeq})
	a.Update(clockMsg{})
	if a.treeBuilds != before {
		t.Fatalf("tree rebuilt %d times for equal inputs", a.treeBuilds-before)
	}

	// Every input invalidates: snapshot, width, open session, profile filter.
	steps := []struct {
		name string
		do   func()
	}{
		{"snapshot", func() {
			st.set(snapshot([]*store.SessionView{session("claude:s2", nil)}, profiles))
			a.Update(snapshotMsg{snap: st.Snapshot()})
		}},
		{"width", func() { a.Update(tea.WindowSizeMsg{Width: 80, Height: 30}) }},
		{"detail", func() { a.nav.DetailKey = "claude:s2" }},
		{"filter", func() { a.nav.ProfileFilter = profiles[0].Label }},
	}
	for _, s := range steps {
		n := a.treeBuilds
		s.do()
		same(s.name)
		if a.treeBuilds == n {
			t.Fatalf("%s: tree not rebuilt after the input changed", s.name)
		}
	}
}

func TestEmptyStateNamesTheProvider(t *testing.T) {
	oc := []config.ProfileDir{{Label: "oc", Dir: "/home/u/.local/share/opencode", Provider: domain.Opencode}}
	t.Run("an unsupported profile says so", func(t *testing.T) {
		snap := snapshot(nil, oc)
		snap.Profiles[0].Provider, snap.Profiles[0].Unsupported = domain.Opencode, true
		frame := screen(newApp(testOptions(newFakeStore(snap), oc), 100, 30))
		mustContain(t, frame, "opencode support is not available yet (/home/u/.local/share/opencode)")
		mustNotContain(t, frame, "Claude Code")
	})
	t.Run("a supported one names its provider", func(t *testing.T) {
		frame := screen(newApp(testOptions(newFakeStore(snapshot(nil, oc)), oc), 100, 30))
		mustContain(t, frame, "no open opencode sessions in")
	})
	t.Run("Claude Code stays the default", func(t *testing.T) {
		frame := screen(newApp(testOptions(newFakeStore(snapshot(nil, profiles)), profiles), 100, 30))
		mustContain(t, frame, "no open Claude Code sessions in")
	})
}
