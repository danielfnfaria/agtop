package model

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// Segment is a piece of a line with one style; Color is empty for the terminal default.
type Segment struct {
	Text    string
	Color   style.Color
	Bold    bool
	Inverse bool
	Dim     bool
}

// SegmentsWidth is the columns the segments take on the terminal (CJK and emoji count 2).
func SegmentsWidth(segs []Segment) int {
	w := 0
	for _, s := range segs {
		w += ansi.StringWidth(s.Text)
	}
	return w
}

// StatusBarModel is the content of the status bar.
type StatusBarModel struct {
	// Left is the badge and counts (already reduced to fit, in the 2-row form).
	Left []Segment
	// Hint is the key hints (key highlighted, description muted), at the right.
	Hint []Segment
	// Rows is 1 when counts and hints fit side by side, else 2 (hints on the lower one).
	Rows int
}

// KeyHint is a key hint. The essential ones stay in the reduced form; the last (quit) never goes.
type KeyHint struct {
	Key       string
	Label     string
	Essential bool
}

// Hint is the hints of a mode (tree, detail, help), in order; the last is always `q quit`.
type Hint []KeyHint

// Hints are the hints of each mode.
type Hints struct{ Tree, Detail, Help Hint }

// HintsFor builds the hints with the glyphs of the look in use.
func HintsFor(g style.Glyphs) Hints {
	return Hints{
		Tree: Hint{
			{g.Enter, "open", true}, {"p", "profile", false}, {"?", "help", true}, {"q", "quit", false},
		},
		Detail: Hint{
			{"Esc", "back", true}, {"Tab", "panel", true}, {g.Up + g.Down, "agent", false},
			{g.Enter, "expand", false}, {"?", "help", false}, {"q", "quit", false},
		},
		Help: Hint{{"Esc", "close", true}, {"?", "close", false}, {"q", "quit", false}},
	}
}

// SingleProfileTreeHints are the tree hints without `p`, which has no effect with one profile.
func SingleProfileTreeHints(g style.Glyphs) Hint {
	return slices.DeleteFunc(slices.Clone(HintsFor(g).Tree), func(h KeyHint) bool { return h.Key == "p" })
}

// StatusBarInput is what the status bar reads. Zero Glyphs and Theme mean unicode and dark.
type StatusBarInput struct {
	Snapshot *store.Snapshot
	Profiles []config.ProfileDir
	// ProfileFilter is the filtered profile (empty = all): the `profile:` chip only shows with an
	// active filter.
	ProfileFilter string
	// Sessions are the visible ones (the filter applied): count, busy/waiting and active agents.
	Sessions []*store.SessionView
	Hint     Hint
	Columns  int
	// Retained is the process messages held during the full screen.
	Retained int
	// Tones maps a profile label to its color (the filter chip); missing: the first of the palette.
	Tones  map[string]style.Color
	Glyphs style.Glyphs
	Theme  style.Theme
}

// DividerRows is the divider line above the bar, counted in the height the app reserves.
const DividerRows = 1

// StatusBarHeight is the rows the bar takes with its divider.
func StatusBarHeight(m StatusBarModel) int { return DividerRows + m.Rows }

// MinSpacing is the least space between the counts and the hints on the same row.
const MinSpacing = 2

const badgeText = "agtop"

type groupID int

const (
	groupBadge groupID = iota
	groupSessions
	groupAgents
	groupErrors
	groupRetained
	groupFilter
	groupMissing
)

type group struct {
	id   groupID
	full []Segment
	// compact is the short form, used only when the bar does not fit; nil when there is none.
	compact []Segment
}

// filterCompactText is the longest filtered label, in characters, in the compact chip.
const filterCompactText = 12

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func resolveGlyphs(g style.Glyphs) style.Glyphs {
	if g.BarFull == "" && len(g.Spinner) == 0 {
		return style.UnicodeGlyphs
	}
	return g
}

func resolveTheme(t style.Theme) style.Theme {
	if t.Muted == "" && t.Accent == "" {
		return style.DarkTheme
	}
	return t
}

func toneOf(tones map[string]style.Color, label string, t style.Theme) style.Color {
	if c, ok := tones[label]; ok {
		return c
	}
	if len(t.Profiles) > 0 {
		return t.Profiles[0]
	}
	return "magenta"
}

func activitySegment(n int, kind style.Activity, g style.Glyphs, t style.Theme) Segment {
	return Segment{Text: fmt.Sprintf(" %s %d %s", g.Dot, n, kind), Color: style.ActivityColor(kind, t)}
}

func buildGroups(in StatusBarInput, g style.Glyphs, t style.Theme) []group {
	busy, waiting, agents := 0, 0, 0
	for _, s := range in.Sessions {
		switch s.Activity {
		case discovery.ActivityBusy:
			busy++
		case discovery.ActivityWaiting:
			waiting++
		}
		for _, a := range s.Agents {
			if domain.IsActive(a.Status) {
				agents++
			}
		}
	}
	count := Segment{Text: plural(len(in.Sessions), "session", "sessions")}
	// Only busy and waiting are counted: waiting asks for the user's action, so it survives the
	// compact form; idle, shell and unknown are left to each session's own label.
	var busySegs, waitingSegs []Segment
	if busy > 0 {
		busySegs = []Segment{activitySegment(busy, style.ActivityBusy, g, t)}
	}
	if waiting > 0 {
		waitingSegs = []Segment{activitySegment(waiting, style.ActivityWaiting, g, t)}
	}

	errs := 0
	if in.Snapshot != nil {
		errs = len(in.Snapshot.Errors)
	}
	errTone := t.Muted
	if errs > 0 {
		errTone = t.Failed
	}
	agentTone := t.Muted
	if agents > 0 {
		agentTone = t.Busy
	}

	groups := []group{
		{id: groupBadge, full: []Segment{{Text: " " + badgeText + " ", Inverse: true, Bold: true, Color: t.Accent}}},
		{
			id:      groupSessions,
			full:    slices.Concat([]Segment{count}, busySegs, waitingSegs),
			compact: slices.Concat([]Segment{count}, waitingSegs),
		},
		{id: groupAgents, full: []Segment{{Text: g.Gear + " " + plural(agents, "agent", "agents"), Color: agentTone}}},
		{
			id:      groupErrors,
			full:    []Segment{{Text: g.Cross + " " + plural(errs, "error", "errors"), Color: errTone, Bold: errs > 0}},
			compact: []Segment{{Text: fmt.Sprintf("%s %d", g.Cross, errs), Color: errTone, Bold: errs > 0}},
		},
	}
	if in.Retained > 0 {
		groups = append(groups, group{
			id:      groupRetained,
			full:    []Segment{{Text: "! " + plural(in.Retained, "held message", "held messages"), Color: t.Warn}},
			compact: []Segment{{Text: fmt.Sprintf("! %d held", in.Retained), Color: t.Warn}},
		})
	}
	if in.ProfileFilter != "" {
		label := Segment{Text: "profile: ", Color: t.Muted}
		tone := toneOf(in.Tones, in.ProfileFilter, t)
		groups = append(groups, group{
			id:      groupFilter,
			full:    []Segment{label, {Text: Clip(in.ProfileFilter, MaxText, g.Ellipsis), Color: tone}},
			compact: []Segment{label, {Text: Clip(in.ProfileFilter, filterCompactText, g.Ellipsis), Color: tone}},
		})
	}

	// A watched directory without sessions/ stays a warning (no per-profile counts, though).
	var missing []config.ProfileDir
	if in.Snapshot != nil {
		for _, p := range in.Profiles {
			for _, st := range in.Snapshot.Profiles {
				if st.Label == p.Label && st.SessionsDirMissing {
					missing = append(missing, p)
					break
				}
			}
		}
	}
	if len(missing) > 0 {
		warn := Segment{Text: "no sessions/", Color: t.Warn}
		full := []Segment{warn}
		// With one profile the label says nothing; with more, it names who lacks the directory.
		if len(in.Profiles) > 1 {
			full = nil
			for i, p := range missing {
				if i > 0 {
					full = append(full, Segment{Text: ", "})
				}
				full = append(full, Segment{Text: Clip(p.Label, MaxText, g.Ellipsis), Color: toneOf(in.Tones, p.Label, t)})
			}
			full = append(full, Segment{Text: " "}, warn)
		}
		groups = append(groups, group{id: groupMissing, full: full, compact: []Segment{warn}})
	}
	return groups
}

// hintSegments shows the key highlighted and the description muted.
func hintSegments(hints Hint, withLabel func(h KeyHint, i int) bool, gap string, t style.Theme) []Segment {
	var out []Segment
	for i, h := range hints {
		if len(out) > 0 {
			out = append(out, Segment{Text: gap})
		}
		out = append(out, Segment{Text: h.Key, Bold: true, Color: t.Accent})
		if withLabel(h, i) {
			out = append(out, Segment{Text: " " + h.Label, Color: t.Muted})
		}
	}
	return out
}

// hintLevels are the forms of the hints, from the fullest to the shortest: all with
// description; only the essentials; only the keys (with quit described); only `q quit`.
func hintLevels(hints Hint, t style.Theme) [][]Segment {
	if len(hints) == 0 {
		return [][]Segment{nil, nil, nil, nil}
	}
	last := len(hints) - 1
	var essentials Hint
	for i, h := range hints {
		if h.Essential || i == last {
			essentials = append(essentials, h)
		}
	}
	return [][]Segment{
		hintSegments(hints, func(KeyHint, int) bool { return true }, "  ", t),
		hintSegments(essentials, func(KeyHint, int) bool { return true }, "  ", t),
		hintSegments(hints, func(_ KeyHint, i int) bool { return i == last }, " ", t),
		hintSegments(hints[last:], func(KeyHint, int) bool { return true }, " ", t),
	}
}

func joinGroups(groups []group, compacted map[groupID]bool, sep Segment) []Segment {
	var out []Segment
	for _, g := range groups {
		if len(out) > 0 {
			out = append(out, sep)
		}
		if compacted[g.id] && g.compact != nil {
			out = append(out, g.compact...)
		} else {
			out = append(out, g.full...)
		}
	}
	return out
}

type step struct {
	drop bool
	id   groupID
}

// fitLeft reduces the counts until they fit in columns: it drops the sessions/ warning and the
// agents, shortens the sessions (waiting stays), the filter, the held messages and the errors;
// last it drops the filter and the badge. Sessions, errors and held messages never go.
func fitLeft(groups []group, columns int, sep Segment) []Segment {
	steps := []step{
		{false, groupMissing}, {true, groupMissing}, {true, groupAgents},
		{false, groupSessions}, {false, groupFilter}, {false, groupRetained}, {false, groupErrors},
		{true, groupFilter}, {true, groupBadge},
	}
	kept := slices.Clone(groups)
	compacted := map[groupID]bool{}
	left := joinGroups(kept, compacted, sep)
	for _, s := range steps {
		if SegmentsWidth(left) <= columns {
			break
		}
		if s.drop {
			kept = slices.DeleteFunc(kept, func(g group) bool { return g.id == s.id })
		} else {
			compacted[s.id] = true
		}
		left = joinGroups(kept, compacted, sep)
	}
	return left
}

// NewStatusBarModel builds the content of the status bar: badge, visible sessions (with busy,
// and waiting when any), active agents, errors, held messages (only if any), the profile filter
// (only when active) and the sessions/ warning at the left; hints at the right. It decides,
// measuring in columns, whether everything fits in one row; else it uses 2, with the counts
// reduced and the hints in the fullest form that fits.
func NewStatusBarModel(in StatusBarInput) StatusBarModel {
	g, t := resolveGlyphs(in.Glyphs), resolveTheme(in.Theme)
	groups := buildGroups(in, g, t)
	sep := Segment{Text: " " + g.Line.Vertical + " ", Color: t.Muted}
	full := joinGroups(groups, nil, sep)
	levels := hintLevels(in.Hint, t)
	if SegmentsWidth(full)+MinSpacing+SegmentsWidth(levels[0]) <= in.Columns {
		return StatusBarModel{Left: full, Hint: levels[0], Rows: 1}
	}
	hint := levels[len(levels)-1]
	for _, h := range levels {
		if SegmentsWidth(h) <= in.Columns {
			hint = h
			break
		}
	}
	return StatusBarModel{Left: fitLeft(groups, in.Columns, sep), Hint: hint, Rows: 2}
}
