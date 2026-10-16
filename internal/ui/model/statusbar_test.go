package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const (
	sep      = " │ "
	dot      = "●"
	gear     = "⚙"
	cross    = "✗"
	enter    = "↵"
	updown   = "↑↓"
	badge    = " agtop "
	sessFull = "5 sessions " + dot + " 1 busy"
	agents1  = gear + " 1 agent"
	errors0  = cross + " 0 errors"
	treeHint = enter + " open  p profile  ? help  q quit"
	treeOne  = enter + " open  ? help  q quit"
)

var (
	hints       = HintsFor(style.UnicodeGlyphs)
	singleHints = SingleProfileTreeHints(style.UnicodeGlyphs)
	baseLeft    = []string{badge, sessFull, agents1, errors0}
	single      = []config.ProfileDir{profiles[0]}
)

// fiveSessions are the test sessions: 1 busy with 1 active agent, 4 idle.
func fiveSessions() []*store.SessionView {
	busy := view("claude:s1", discovery.ActivityBusy, domain.StatusRunning, domain.StatusCompleted)
	return []*store.SessionView{
		busy,
		view("claude-x:s2", discovery.ActivityIdle),
		view("claude:s3", discovery.ActivityIdle),
		view("claude:s4", discovery.ActivityIdle),
		view("claude:s5", discovery.ActivityIdle),
	}
}

func withActivity(i int, a discovery.Activity) []*store.SessionView {
	s := fiveSessions()
	s[i] = view(s[i].Key, a)
	return s
}

func snapshot(errs int, statuses ...store.ProfileStatus) *store.Snapshot {
	if statuses == nil {
		statuses = []store.ProfileStatus{profileStatus("claude", "/tmp/a/.claude"), profileStatus("claude-x", "/tmp/b/.claude-x")}
	}
	snap := &store.Snapshot{Profiles: statuses}
	for i := range errs {
		snap.Errors = append(snap.Errors, fmt.Sprintf("e%d", i))
	}
	return snap
}

type barOpt func(*StatusBarInput)

func barInput(opts ...barOpt) StatusBarInput {
	in := StatusBarInput{
		Snapshot: snapshot(0), Profiles: profiles, Sessions: fiveSessions(),
		Hint: hints.Tree, Columns: 180,
	}
	for _, o := range opts {
		o(&in)
	}
	return in
}

func width(n int) barOpt        { return func(in *StatusBarInput) { in.Columns = n } }
func errorsOpt(n int) barOpt    { return func(in *StatusBarInput) { in.Snapshot = snapshot(n) } }
func retainedOpt(n int) barOpt  { return func(in *StatusBarInput) { in.Retained = n } }
func filterOpt(l string) barOpt { return func(in *StatusBarInput) { in.ProfileFilter = l } }
func hintOpt(h Hint) barOpt     { return func(in *StatusBarInput) { in.Hint = h } }
func tonesOpt() barOpt          { return func(in *StatusBarInput) { in.Tones = tones() } }
func sessionsOpt(s []*store.SessionView) barOpt {
	return func(in *StatusBarInput) { in.Sessions = s }
}
func singleOpt() barOpt {
	return func(in *StatusBarInput) {
		in.Profiles, in.Hint = single, singleHints
		in.Snapshot = snapshot(0, profileStatus("claude", "/tmp/a/.claude"))
	}
}

func join(parts ...string) string { return strings.Join(parts, sep) }

func segOf(segs []Segment, sub string) *Segment {
	for i := range segs {
		if strings.Contains(segs[i].Text, sub) {
			return &segs[i]
		}
	}
	return &Segment{Color: "<none>"}
}

func TestStatusBarContent(t *testing.T) {
	t.Run("two profiles, no filter", func(t *testing.T) {
		m := NewStatusBarModel(barInput())
		if m.Rows != 1 || StatusBarHeight(m) != 2 || text(m.Left) != join(baseLeft...) || text(m.Hint) != treeHint {
			t.Errorf("got %d rows, %q, %q", m.Rows, text(m.Left), text(m.Hint))
		}
		for _, bad := range []string{"profile", "claude", "idle", "sort"} {
			if strings.Contains(text(m.Left), bad) {
				t.Errorf("left has %q", bad)
			}
		}
	})

	t.Run("an active filter shows the chip, the label in its profile color", func(t *testing.T) {
		m := NewStatusBarModel(barInput(filterOpt("claude")))
		if got := text(m.Left); got != join(append(slices.Clone(baseLeft), "profile: claude")...) {
			t.Errorf("left = %q", got)
		}
		i := slices.IndexFunc(m.Left, func(s Segment) bool { return s.Text == "profile: " })
		if m.Left[i].Color != "gray" || m.Left[i+1].Text != "claude" || m.Left[i+1].Color != "magenta" {
			t.Errorf("chip = %+v %+v", m.Left[i], m.Left[i+1])
		}
		x := NewStatusBarModel(barInput(filterOpt("claude-x"), tonesOpt()))
		j := slices.IndexFunc(x.Left, func(s Segment) bool { return s.Text == "profile: " })
		if x.Left[j+1].Text != "claude-x" || x.Left[j+1].Color != "blue" {
			t.Errorf("chip = %+v", x.Left[j+1])
		}
	})

	t.Run("one profile: same left side, hints without p", func(t *testing.T) {
		m := NewStatusBarModel(barInput(singleOpt()))
		if m.Rows != 1 || text(m.Left) != join(baseLeft...) || text(m.Hint) != treeOne {
			t.Errorf("got %d rows, %q, %q", m.Rows, text(m.Left), text(m.Hint))
		}
		full := NewStatusBarModel(barInput(singleOpt(), hintOpt(hints.Tree)))
		if strings.Contains(text(full.Left), "profile") || strings.Contains(text(full.Left), "claude") {
			t.Errorf("left = %q", text(full.Left))
		}
	})

	t.Run("colors: inverse badge, busy and agents yellow, gray separators", func(t *testing.T) {
		m := NewStatusBarModel(barInput(width(400), tonesOpt()))
		b := m.Left[0]
		if b.Text != badge || !b.Inverse || !b.Bold || b.Color != "cyan" {
			t.Errorf("badge = %+v", b)
		}
		if segOf(m.Left, "busy").Color != "yellow" || segOf(m.Left, "1 agent").Color != "yellow" || segOf(m.Left, "errors").Color != "gray" {
			t.Errorf("colors: %+v", m.Left)
		}
		seps := 0
		for _, s := range m.Left {
			if s.Text == sep {
				seps++
				if s.Color != "gray" {
					t.Errorf("separator color %q", s.Color)
				}
			}
		}
		if seps != 3 {
			t.Errorf("%d separators", seps)
		}
	})

	t.Run("errors are red and bold only when there are some", func(t *testing.T) {
		m := NewStatusBarModel(barInput(errorsOpt(3)))
		if s := segOf(m.Left, "3 errors"); s.Color != "red" || !s.Bold {
			t.Errorf("segment = %+v", s)
		}
		if got := text(NewStatusBarModel(barInput(errorsOpt(1))).Left); got != join(badge, sessFull, agents1, cross+" 1 error") {
			t.Errorf("left = %q", got)
		}
	})

	t.Run("held messages: yellow, after the errors, only when there are some", func(t *testing.T) {
		left := func(opts ...barOpt) string { return text(NewStatusBarModel(barInput(opts...)).Left) }
		if strings.Contains(left(retainedOpt(0)), "held") {
			t.Error("held with 0")
		}
		if got := left(retainedOpt(1)); got != join(append(slices.Clone(baseLeft), "! 1 held message")...) {
			t.Errorf("1 = %q", got)
		}
		if got := left(retainedOpt(3)); got != join(append(slices.Clone(baseLeft), "! 3 held messages")...) {
			t.Errorf("3 = %q", got)
		}
		if got := segOf(NewStatusBarModel(barInput(retainedOpt(3))).Left, "held").Color; got != "yellow" {
			t.Errorf("color = %q", got)
		}
		if got := left(retainedOpt(2), filterOpt("claude")); got != join(append(slices.Clone(baseLeft), "! 2 held messages", "profile: claude")...) {
			t.Errorf("with filter = %q", got)
		}
	})

	t.Run("waiting is red after busy; idle, shell and unknown never show", func(t *testing.T) {
		s := fiveSessions()
		s[1] = view(s[1].Key, discovery.ActivityShell)
		s[2] = view(s[2].Key, discovery.ActivityWaiting)
		s[3] = view(s[3].Key, discovery.ActivityUnknown)
		s[3].RawStatus = "paused"
		m := NewStatusBarModel(barInput(sessionsOpt(s), width(400)))
		want := join(badge, "5 sessions "+dot+" 1 busy "+dot+" 1 waiting", agents1, errors0)
		if got := text(m.Left); got != want {
			t.Errorf("left = %q", got)
		}
		if segOf(m.Left, "waiting").Color != "red" {
			t.Error("waiting is not red")
		}
		for _, bad := range []string{"shell", "idle", "paused", "unknown"} {
			if strings.Contains(text(m.Left), bad) {
				t.Errorf("left has %q", bad)
			}
		}
	})

	t.Run("busy only when above 0; no sessions, just the count; agents gray at 0", func(t *testing.T) {
		none := NewStatusBarModel(barInput(sessionsOpt(nil)))
		if got := text(none.Left); got != join(badge, "0 sessions", gear+" 0 agents", errors0) {
			t.Errorf("none = %q", got)
		}
		if segOf(none.Left, "agents").Color != "gray" {
			t.Error("agents are not gray")
		}
		idle := text(NewStatusBarModel(barInput(sessionsOpt(fiveSessions()[1:]))).Left)
		if !strings.Contains(idle, "4 sessions"+sep) || strings.Contains(idle, "busy") || strings.Contains(idle, "idle") {
			t.Errorf("idle only = %q", idle)
		}
	})

	t.Run("missing sessions/: a yellow warning, with the label only with 2+ profiles", func(t *testing.T) {
		missing := func(label, dir string) store.ProfileStatus {
			p := profileStatus(label, dir)
			p.SessionsDirMissing = true
			return p
		}
		one := NewStatusBarModel(barInput(singleOpt(), func(in *StatusBarInput) {
			in.Snapshot = snapshot(0, missing("claude", "/tmp/a/.claude"))
		}))
		if got := text(one.Left); got != join(append(slices.Clone(baseLeft), "no sessions/")...) {
			t.Errorf("one = %q", got)
		}
		if segOf(one.Left, "no sessions/").Color != "yellow" {
			t.Error("warning is not yellow")
		}
		multi := NewStatusBarModel(barInput(func(in *StatusBarInput) {
			in.Snapshot = snapshot(0, profileStatus("claude", "/tmp/a/.claude"), missing("claude-x", "/tmp/b/.claude-x"))
		}))
		if got := text(multi.Left); got != join(append(slices.Clone(baseLeft), "claude-x no sessions/")...) {
			t.Errorf("multi = %q", got)
		}
	})

	t.Run("hints: key bold cyan, label gray; detail and help", func(t *testing.T) {
		m := NewStatusBarModel(barInput())
		if k := m.Hint[0]; k.Text != enter || !k.Bold || k.Color != "cyan" {
			t.Errorf("key = %+v", k)
		}
		if l := m.Hint[1]; l.Text != " open" || l.Color != "gray" {
			t.Errorf("label = %+v", l)
		}
		if got := text(NewStatusBarModel(barInput(hintOpt(hints.Detail))).Hint); got != "Esc back  Tab panel  "+updown+" agent  "+enter+" expand  ? help  q quit" {
			t.Errorf("detail = %q", got)
		}
		if got := text(NewStatusBarModel(barInput(hintOpt(hints.Help))).Hint); got != "Esc close  ? close  q quit" {
			t.Errorf("help = %q", got)
		}
		for _, h := range []Hint{hints.Tree, hints.Detail, hints.Help, singleHints} {
			if slices.ContainsFunc(h, func(k KeyHint) bool { return k.Key == "s" }) {
				t.Errorf("hint with s: %+v", h)
			}
		}
	})

	t.Run("measures width in terminal columns (CJK counts 2), not characters", func(t *testing.T) {
		label := "日本語語語"
		if ansi.StringWidth(label) != 10 {
			t.Fatal("label width")
		}
		ps := []config.ProfileDir{{Label: label, Dir: "/tmp/x"}, {Label: "b", Dir: "/tmp/b"}}
		at := func(columns int) StatusBarModel {
			return NewStatusBarModel(barInput(func(in *StatusBarInput) {
				in.Profiles, in.ProfileFilter, in.Sessions, in.Snapshot, in.Columns = ps, label, nil, &store.Snapshot{}, columns
			}))
		}
		m := at(200)
		chars := 0
		for _, s := range m.Left {
			chars += len([]rune(s.Text))
		}
		if m.Rows != 1 || SegmentsWidth(m.Left)-chars != 5 {
			t.Errorf("rows %d, extra columns %d", m.Rows, SegmentsWidth(m.Left)-chars)
		}
		exact := SegmentsWidth(m.Left) + 2 + SegmentsWidth(m.Hint)
		if at(exact).Rows != 1 {
			t.Error("exact width should fit in one row")
		}
		// Counting characters it would fit; in columns it does not.
		if at(exact-1).Rows != 2 {
			t.Error("one column less should take two rows")
		}
	})
}

func TestStatusBarWidths(t *testing.T) {
	left := func(columns int, opts ...barOpt) string {
		return text(NewStatusBarModel(barInput(append([]barOpt{width(columns)}, opts...)...)).Left)
	}
	rows := func(columns int, opts ...barOpt) int {
		return NewStatusBarModel(barInput(append([]barOpt{width(columns)}, opts...)...)).Rows
	}
	withBase := func(extra ...string) string { return join(append(slices.Clone(baseLeft), extra...)...) }

	t.Run("180 and 120: everything on one line, with or without a filter", func(t *testing.T) {
		for _, columns := range []int{180, 120} {
			if rows(columns) != 1 || left(columns) != withBase() {
				t.Errorf("%d: %d rows, %q", columns, rows(columns), left(columns))
			}
			if rows(columns, filterOpt("claude")) != 1 || left(columns, filterOpt("claude")) != withBase("profile: claude") {
				t.Errorf("%d filter: %q", columns, left(columns, filterOpt("claude")))
			}
		}
		// Exactly where one line stops fitting (54 + 2 + 33 and 72 + 2 + 33).
		checks := []struct {
			columns, rows int
			opts          []barOpt
		}{{89, 1, nil}, {88, 2, nil}, {107, 1, []barOpt{filterOpt("claude")}}, {106, 2, []barOpt{filterOpt("claude")}}}
		for _, c := range checks {
			if got := rows(c.columns, c.opts...); got != c.rows {
				t.Errorf("%d columns: %d rows, want %d", c.columns, got, c.rows)
			}
		}
	})

	t.Run("80: two lines, the left side whole, full hints below", func(t *testing.T) {
		for _, opts := range [][]barOpt{nil, {filterOpt("claude")}} {
			m := NewStatusBarModel(barInput(append([]barOpt{width(80)}, opts...)...))
			if m.Rows != 2 || text(m.Hint) != treeHint {
				t.Errorf("rows %d hint %q", m.Rows, text(m.Hint))
			}
		}
		if got := left(80); got != withBase() {
			t.Errorf("left = %q", got)
		}
		if got := left(80, filterOpt("claude")); got != withBase("profile: claude") {
			t.Errorf("filter = %q", got)
		}
		want := join(badge, sessFull, cross+" 3 errors", "! 4 held messages", "profile: claude")
		if got := left(80, errorsOpt(3), retainedOpt(4), filterOpt("claude")); got != want {
			t.Errorf("many = %q", got)
		}
	})

	t.Run("60: agents, then the busy detail go first; the filter chip stays", func(t *testing.T) {
		if got := left(60); got != withBase() {
			t.Errorf("plain = %q", got)
		}
		if got := left(60, filterOpt("claude")); got != join(badge, sessFull, errors0, "profile: claude") {
			t.Errorf("filter = %q", got)
		}
		if got := left(60, errorsOpt(2), retainedOpt(2)); got != join(badge, "5 sessions", cross+" 2 errors", "! 2 held messages") {
			t.Errorf("errors = %q", got)
		}
	})

	t.Run("50: busy, held and errors shorten; waiting survives the compact form", func(t *testing.T) {
		if got := left(50); got != join(badge, sessFull, errors0) {
			t.Errorf("plain = %q", got)
		}
		if got := left(50, filterOpt("claude")); got != join(badge, "5 sessions", cross+" 0", "profile: claude") {
			t.Errorf("filter = %q", got)
		}
		if got := left(50, errorsOpt(2), retainedOpt(2)); got != join(badge, "5 sessions", cross+" 2 errors", "! 2 held") {
			t.Errorf("errors = %q", got)
		}
		waiting := withActivity(1, discovery.ActivityWaiting)
		if got := left(50, sessionsOpt(waiting), errorsOpt(2), retainedOpt(2)); got != join(badge, "5 sessions "+dot+" 1 waiting", cross+" 2", "! 2 held") {
			t.Errorf("waiting = %q", got)
		}
		if got := text(NewStatusBarModel(barInput(width(50))).Hint); got != treeHint {
			t.Errorf("hint = %q", got)
		}
	})

	t.Run("a long filtered label is clipped, then the chip goes before the errors would", func(t *testing.T) {
		long := "a-very-long-profile-label-name"
		ps := []config.ProfileDir{{Label: long, Dir: "/tmp/l"}, {Label: "b", Dir: "/tmp/b"}}
		at := func(columns int) string {
			return left(columns, errorsOpt(2), filterOpt(long), func(in *StatusBarInput) { in.Profiles = ps })
		}
		if got := at(120); !strings.Contains(got, "profile: "+long) {
			t.Errorf("120 = %q", got)
		}
		if got := at(60); got != join(badge, "5 sessions", cross+" 2 errors", "profile: a-very-long…") {
			t.Errorf("60 = %q", got)
		}
		if got := at(40); got != join(badge, "5 sessions", cross+" 2") {
			t.Errorf("40 = %q", got)
		}
	})

	t.Run("hints: full, essentials only, keys only; always with q quit", func(t *testing.T) {
		hint := func(h Hint, columns int) string {
			return text(NewStatusBarModel(barInput(hintOpt(h), width(columns))).Hint)
		}
		checks := []struct {
			h       Hint
			columns int
			want    string
		}{
			{hints.Tree, 33, treeHint},
			{hints.Tree, 32, enter + " open  ? help  q quit"},
			{singleHints, 50, treeOne},
			{hints.Detail, 50, "Esc back  Tab panel  q quit"},
			{hints.Detail, 25, "Esc Tab " + updown + " " + enter + " ? q quit"},
			{hints.Detail, 20, "q quit"},
		}
		for _, c := range checks {
			if got := hint(c.h, c.columns); got != c.want {
				t.Errorf("%d columns: %q, want %q", c.columns, got, c.want)
			}
		}
	})
}

func TestStatusBarInvariants(t *testing.T) {
	type variant struct {
		name string
		opts []barOpt
	}
	variants := []variant{
		{"clean", nil},
		{"errors", []barOpt{errorsOpt(3)}},
		{"held", []barOpt{retainedOpt(4)}},
		{"errors and held", []barOpt{errorsOpt(30), retainedOpt(40)}},
		{"filter", []barOpt{filterOpt("claude-x")}},
		{"filter, waiting, errors, held", []barOpt{filterOpt("claude-x"), sessionsOpt(withActivity(1, discovery.ActivityWaiting)), errorsOpt(3), retainedOpt(4)}},
		{"1 profile", []barOpt{singleOpt()}},
	}
	errCount := map[string]int{"errors": 3, "errors and held": 30, "filter, waiting, errors, held": 3}
	heldCount := map[string]int{"held": 4, "errors and held": 40, "filter, waiting, errors, held": 4}
	modes := map[string]Hint{"tree": hints.Tree, "detail": hints.Detail, "help": hints.Help}

	for _, columns := range []int{400, 180, 120, 100, 80, 70, 60, 50, 40} {
		for mode, hint := range modes {
			t.Run(fmt.Sprintf("%d columns, %s", columns, mode), func(t *testing.T) {
				for _, v := range variants {
					for _, withTones := range []bool{false, true} {
						opts := append([]barOpt{hintOpt(hint)}, v.opts...)
						opts = append(opts, width(columns))
						plain := NewStatusBarModel(barInput(opts...))
						m := plain
						if withTones {
							m = NewStatusBarModel(barInput(append(opts, tonesOpt())...))
						}
						where := fmt.Sprintf("%s tones=%v", v.name, withTones)
						l, h := SegmentsWidth(m.Left), SegmentsWidth(m.Hint)
						if m.Rows == 1 && l+2+h > columns || m.Rows == 2 && (l > columns || h > columns) {
							t.Errorf("%s: does not fit (%d + %d, %d rows)", where, l, h, m.Rows)
						}
						left := text(m.Left)
						if !strings.HasSuffix(text(m.Hint), "q quit") || !strings.Contains(left, "5 sessions") ||
							!strings.Contains(left, fmt.Sprintf("%s %d", cross, errCount[v.name])) || !strings.Contains(left, sep) {
							t.Errorf("%s: left %q hint %q", where, left, text(m.Hint))
						}
						if n := heldCount[v.name]; n > 0 && !strings.Contains(left, fmt.Sprintf("! %d ", n)) {
							t.Errorf("%s: lost the held messages: %q", where, left)
						}
						if strings.Contains(left+text(m.Hint), "sort") {
							t.Errorf("%s: mentions sort", where)
						}
						for _, g := range style.UnicodeGlyphs.Spark {
							if strings.Contains(left, g) {
								t.Errorf("%s: sparkline in the left side", where)
							}
						}
						// Colors change nothing but the colors.
						if withTones && left != text(plain.Left) {
							t.Errorf("%s: colors changed the text", where)
						}
					}
				}
			})
		}
	}
}
