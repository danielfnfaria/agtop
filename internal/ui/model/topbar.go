package model

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// The top bar has one line per profile with its plan usage and its activity:
//
//	✳ claude-x   5h ██████░░  79% ↻17:30   7d ██░░░░░░  23% ↻Tue     ▁▁▂▅▃▁▁▇▆▂
//
// The provider symbol (in its color), then the label (in the profile color), padded so the
// columns line up; the 5h and 7d windows with bar and % in the usage color and the reset time
// (local HH:MM for 5h; for 7d HH:MM within a day, else the short weekday); stale data in dim with
// `(stale)`; no usage file: `5h —  7d —` in dim and, on the first such line of a Claude Code
// profile only, a short setup hint (other providers have no status line to wire); and the
// activity over the last hour (one cell per 30 s bucket), aligned on all lines.
//
// On a narrow terminal, in this order: the setup hint goes, the sparkline shrinks (down to
// TopSparkMin) and then goes, then the reset times, then the bars (only the % stays). Lines
// never pass Columns: as a last resort they are cut.

// TopBarInput is what the top bar reads. Zero Glyphs and Theme mean unicode and dark.
type TopBarInput struct {
	Profiles []config.ProfileDir
	// Usage by profile label (absent: no usage file).
	Usage map[string]domain.UsageView
	// ActivityByProfile is the activity histogram by profile label.
	ActivityByProfile map[string]domain.ActivityHistogram
	// Tones maps a profile label to its color; missing: the first of the palette.
	Tones map[string]style.Color
	// Providers maps a profile label to its provider; missing: Claude Code.
	Providers map[string]domain.ProviderID
	// Now is epoch ms: the newest activity bucket and the reference for the reset times.
	Now     int64
	Columns int
	// Loc is the zone of the reset times; nil is local time.
	Loc    *time.Location
	Glyphs style.Glyphs
	Theme  style.Theme
	// NoSparkline leaves the activity sparkline out: only the usage.
	NoSparkline bool
}

// TopBarModel is the content of the top bar.
type TopBarModel struct {
	// Lines is one line per profile, in Profiles order; each fits in Columns.
	Lines [][]Segment
}

// TopBarHeight is the rows the top bar takes with its divider below.
func TopBarHeight(m TopBarModel) int {
	if len(m.Lines) == 0 {
		return 0
	}
	return len(m.Lines) + DividerRows
}

const (
	// TopLabelMax is the widest label column (longer labels are cut).
	TopLabelMax = 20
	// UsageBarWidth is the cells of a usage bar.
	UsageBarWidth = 8
	// TopSparkMin is the narrowest sparkline: below it it is left out.
	TopSparkMin = 10
	// TopSparkMax is the widest sparkline (the activity window has 120 buckets).
	TopSparkMax = 120
	// SetupHint is the hint on the first Claude Code profile line without usage data.
	SetupHint = "(run: agtop setup)"

	topGap      = "   "
	staleText   = "(stale)"
	resetWidth  = 5 // `HH:MM`, or a weekday padded to it
	dayMs       = int64(24 * 60 * 60_000)
	percentCols = 4 // `100%`
)

var weekdays = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// level is how much of a window shows: 0 bar, % and reset; 1 bar and %; 2 % only.
type level int

// FmtResetTime is the HH:MM of epochMs in loc (local time when nil).
func FmtResetTime(epochMs int64, loc *time.Location) string {
	return time.UnixMilli(epochMs).In(orLocal(loc)).Format("15:04")
}

// FmtResetDay is the 7-day reset: HH:MM within a day of now, else the short weekday (Tue).
func FmtResetDay(epochMs, now int64, loc *time.Location) string {
	if epochMs-now < dayMs {
		return FmtResetTime(epochMs, loc)
	}
	return weekdays[time.UnixMilli(epochMs).In(orLocal(loc)).Weekday()]
}

// windowWidth is the width of a window group at lvl (always the same, data or not, so lines
// align): `5h ` + bar + ` ` + `100%` + ` ↻` + reset.
func windowWidth(lvl level) int {
	switch lvl {
	case 2:
		return 3 + percentCols
	case 1:
		return 3 + UsageBarWidth + 1 + percentCols
	}
	return 3 + UsageBarWidth + 1 + percentCols + 2 + resetWidth
}

func padRight(s string, n int) string {
	if c := utf8.RuneCountInString(s); c < n {
		return s + strings.Repeat(" ", n-c)
	}
	return s
}

func padLeft(s string, n int) string {
	if c := utf8.RuneCountInString(s); c < n {
		return strings.Repeat(" ", n-c) + s
	}
	return s
}

// windowSegments is one window (5h/7d), exactly windowWidth(lvl) columns wide.
func windowSegments(name string, w *domain.UsageWindowView, lvl level, dim bool, resetText func(int64) string, g style.Glyphs, t style.Theme) []Segment {
	width := windowWidth(lvl)
	if w == nil || w.Percent == nil {
		text := name + " " + g.Dash
		return []Segment{{Text: text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text))), Dim: true}}
	}
	percent := *w.Percent
	color := style.UsageColor(percent, t)
	out := []Segment{{Text: name + " ", Dim: dim}}
	if lvl < 2 {
		n := int(jsRound(math.Min(100, math.Max(0, percent)) / 100 * UsageBarWidth))
		out = append(out,
			Segment{Text: strings.Repeat(g.BarFull, n), Color: color, Dim: dim},
			Segment{Text: strings.Repeat(g.BarEmpty, UsageBarWidth-n), Color: t.Muted, Dim: dim},
			Segment{Text: " "},
		)
	}
	out = append(out, Segment{Text: padLeft(fmt.Sprintf("%.0f%%", jsRound(percent)), percentCols), Color: color, Dim: dim})
	if lvl == 0 {
		reset := ""
		if w.ResetsAt != 0 {
			reset = g.Reset + resetText(w.ResetsAt)
		}
		out = append(out, Segment{Text: " " + padRight(reset, resetWidth+1), Color: t.Muted, Dim: dim})
	}
	return out
}

type lineParts struct {
	label   []Segment
	windows []Segment
	// note is `(stale)` or the setup hint, after the windows.
	note []Segment
}

func (l lineParts) core() []Segment { return slices.Concat(l.label, l.windows, l.note) }

// sparkSegments is the sparkline of one profile: the newest width buckets, same-color cells
// grouped.
func sparkSegments(h domain.ActivityHistogram, tone style.Color, now int64, width int, g style.Glyphs, t style.Theme) []Segment {
	cells := domain.ActivityCells([]domain.ProfileHistogram{{Label: "p", Histogram: h}}, now, width)
	var out []Segment
	for _, cell := range cells {
		color := t.Muted
		if cell.Level > 0 {
			color = tone
		}
		glyph := ""
		if len(g.Spark) > 0 {
			glyph = g.Spark[min(cell.Level, len(g.Spark)-1)]
		}
		if n := len(out); n > 0 && out[n-1].Color == color {
			out[n-1].Text += glyph
		} else {
			out = append(out, Segment{Text: glyph, Color: color})
		}
	}
	return out
}

// providerOf is the provider of a profile label (Claude Code when it has no entry).
func providerOf(providers map[string]domain.ProviderID, label string) domain.ProviderID {
	if pid, ok := providers[label]; ok {
		return pid
	}
	return domain.ClaudeCode
}

// providerSegments is the provider symbol (width 1, provider color) and a space, before the
// profile label.
func providerSegments(id domain.ProviderID, g style.Glyphs) []Segment {
	p := domain.ProviderInfo(id)
	return []Segment{{Text: g.ProviderMark(p.Symbol), Color: p.Color}, {Text: " "}}
}

// clipSegments cuts segs to columns (by FitWidth, so wide characters never pass the edge).
func clipSegments(segs []Segment, columns int, ellipsis string) []Segment {
	var out []Segment
	left := columns
	for _, s := range segs {
		if left <= 0 {
			break
		}
		if w := ansi.StringWidth(s.Text); w <= left {
			out = append(out, s)
			left -= w
		} else {
			s.Text = FitWidth(s.Text, left, ellipsis)
			out = append(out, s)
			left = 0
		}
	}
	return out
}

// NewTopBarModel builds the top bar.
func NewTopBarModel(in TopBarInput) TopBarModel {
	g, t := resolveGlyphs(in.Glyphs), resolveTheme(in.Theme)
	columns, now := in.Columns, in.Now
	if len(in.Profiles) == 0 {
		return TopBarModel{}
	}
	labels := make([]string, len(in.Profiles))
	labelWidth := 0
	hintAt := -1
	for i, p := range in.Profiles {
		labels[i] = FitWidth(p.Label, TopLabelMax, g.Ellipsis)
		labelWidth = max(labelWidth, ansi.StringWidth(labels[i]))
		// The setup hint only makes sense for Claude Code profiles: agtop setup wires the
		// status line for those only. Skip any other provider here, or a non-Claude profile
		// without usage that comes first would swallow the hint for every line.
		if providerOf(in.Providers, p.Label) == domain.ClaudeCode {
			if _, ok := in.Usage[p.Label]; !ok && hintAt < 0 {
				hintAt = i
			}
		}
	}

	build := func(lvl level, withHint bool) []lineParts {
		parts := make([]lineParts, len(in.Profiles))
		for i, p := range in.Profiles {
			u, has := in.Usage[p.Label]
			dim := !has || u.Stale
			var five, seven *domain.UsageWindowView
			if has {
				five, seven = u.FiveHour, u.SevenDay
			}
			windows := slices.Concat(
				windowSegments("5h", five, lvl, dim, func(at int64) string { return FmtResetTime(at, in.Loc) }, g, t),
				[]Segment{{Text: topGap}},
				windowSegments("7d", seven, lvl, dim, func(at int64) string { return FmtResetDay(at, now, in.Loc) }, g, t),
			)
			id := providerOf(in.Providers, p.Label)
			var note []Segment
			switch {
			case has && u.Stale:
				note = []Segment{{Text: " " + staleText, Dim: true}}
			case withHint && i == hintAt && id == domain.ClaudeCode:
				note = []Segment{{Text: " " + SetupHint, Color: t.Muted, Dim: true}}
			}
			parts[i] = lineParts{
				label: slices.Concat(
					providerSegments(id, g),
					[]Segment{
						{Text: labels[i], Color: toneOf(in.Tones, p.Label, t), Bold: true},
						{Text: strings.Repeat(" ", labelWidth-ansi.StringWidth(labels[i])) + topGap},
					},
				),
				windows: windows,
				note:    note,
			}
		}
		return parts
	}

	coreWidth := func(parts []lineParts) int {
		w := 0
		for _, l := range parts {
			w = max(w, SegmentsWidth(l.core()))
		}
		return w
	}
	sparkWidth := func(parts []lineParts) int {
		if in.NoSparkline {
			return 0
		}
		return min(TopSparkMax, columns-coreWidth(parts)-len(topGap))
	}

	assemble := func(parts []lineParts, spark int) TopBarModel {
		column := coreWidth(parts)
		lines := make([][]Segment, len(parts))
		for i, l := range parts {
			core := l.core()
			if spark < TopSparkMin {
				lines[i] = clipSegments(core, columns, g.Ellipsis)
				continue
			}
			label := in.Profiles[i].Label
			fill := column - SegmentsWidth(core) + len(topGap)
			lines[i] = clipSegments(slices.Concat(
				core,
				[]Segment{{Text: strings.Repeat(" ", fill)}},
				sparkSegments(in.ActivityByProfile[label], toneOf(in.Tones, label, t), now, spark, g, t),
			), columns, g.Ellipsis)
		}
		return TopBarModel{Lines: lines}
	}

	// Full form with the hint and a sparkline; then without the hint; then each level without the
	// sparkline, the first whose lines fit.
	if hintAt >= 0 {
		withHint := build(0, true)
		if sw := sparkWidth(withHint); sw >= TopSparkMin {
			return assemble(withHint, sw)
		}
		if in.NoSparkline && coreWidth(withHint) <= columns {
			return assemble(withHint, 0)
		}
	}
	full := build(0, false)
	if sw := sparkWidth(full); sw >= TopSparkMin {
		return assemble(full, sw)
	}
	for _, lvl := range []level{0, 1, 2} {
		parts := build(lvl, false)
		if coreWidth(parts) <= columns {
			return assemble(parts, 0)
		}
	}
	return assemble(build(2, false), 0)
}
