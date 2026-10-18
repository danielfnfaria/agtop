package model

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const (
	fullBlock  = "█"
	emptyBlock = "░"
	resetGlyph = "↻"
	dashGlyph  = "—"
)

var (
	sparkGlyphs = style.UnicodeGlyphs.Spark
	fiveReset   = now + 2*3_600_000
	sevenReset  = now + 4*86_400_000
)

func text(segs []Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// sparkOf is the run of spark-only segments at the end of the line (bars use █ too).
func sparkOf(segs []Segment) string {
	out := ""
	for i := len(segs) - 1; i >= 0; i-- {
		t := segs[i].Text
		if t == "" {
			break
		}
		only := true
		for _, r := range t {
			found := false
			for _, g := range sparkGlyphs {
				if string(r) == g {
					found = true
				}
			}
			only = only && found
		}
		if !only {
			break
		}
		out = t + out
	}
	return out
}

func usageView(five, seven float64) domain.UsageView {
	return domain.UsageView{
		FiveHour:  &domain.UsageWindowView{Percent: &five, ResetsAt: fiveReset},
		SevenDay:  &domain.UsageWindowView{Percent: &seven, ResetsAt: sevenReset},
		UpdatedAt: now - 1_000,
	}
}

func tones() map[string]style.Color { return style.ProfileToneMap([]string{"claude", "claude-x"}, nil) }

// topInput is two profiles with fresh usage; claude has activity in the current bucket and
// 3 events a minute ago.
func topInput(mod ...func(*TopBarInput)) TopBarInput {
	bucket := domain.ActivityBucket(now)
	in := TopBarInput{
		Profiles: profiles,
		Usage:    map[string]domain.UsageView{"claude": usageView(79, 23), "claude-x": usageView(91, 61)},
		ActivityByProfile: map[string]domain.ActivityHistogram{
			"claude":   {bucket: 1, bucket - 2*domain.ActivityBucketMs: 3},
			"claude-x": {},
		},
		Tones:   tones(),
		Now:     now,
		Columns: 180,
		Loc:     time.UTC,
	}
	for _, m := range mod {
		m(&in)
	}
	return in
}

func cols(n int) func(*TopBarInput) { return func(in *TopBarInput) { in.Columns = n } }

func TestResetTimes(t *testing.T) {
	plus9 := time.FixedZone("UTC+9", 9*3600)
	tests := []struct {
		name string
		loc  *time.Location
		five string
		day  string
		week string
	}{
		{"utc", time.UTC, "00:13", "00:13", "Sat"},
		{"fixed zone moves the clock and the weekday", plus9, "09:13", "09:13", "Sun"},
		{"nil is local time", nil, time.UnixMilli(fiveReset).Local().Format("15:04"), time.UnixMilli(fiveReset).Local().Format("15:04"), time.UnixMilli(sevenReset).Local().Weekday().String()[:3]},
	}
	for _, tt := range tests {
		if got := FmtResetTime(fiveReset, tt.loc); got != tt.five {
			t.Errorf("%s: FmtResetTime = %q, want %q", tt.name, got, tt.five)
		}
		if got := FmtResetDay(fiveReset, now, tt.loc); got != tt.day {
			t.Errorf("%s: FmtResetDay(5h) = %q, want %q", tt.name, got, tt.day)
		}
		if got := FmtResetDay(sevenReset, now, tt.loc); got != tt.week {
			t.Errorf("%s: FmtResetDay(7d) = %q, want %q", tt.name, got, tt.week)
		}
	}
	// A weekday that depends on the zone: Sat 22:30 UTC is already Sun in UTC+9.
	late := time.Date(2023, 11, 18, 22, 30, 0, 0, time.UTC).UnixMilli()
	if got := FmtResetDay(late, now, time.UTC); got != "Sat" {
		t.Errorf("utc weekday = %q", got)
	}
	if got := FmtResetDay(late, now, plus9); got != "Sun" {
		t.Errorf("+9 weekday = %q", got)
	}
}

func TestTopBarFull(t *testing.T) {
	m := NewTopBarModel(topInput())
	if len(m.Lines) != 2 || TopBarHeight(m) != 3 {
		t.Fatalf("lines %d height %d", len(m.Lines), TopBarHeight(m))
	}
	claude, other := text(m.Lines[0]), text(m.Lines[1])
	sym := domain.Providers[domain.ClaudeCode].Symbol
	// Labels padded to the widest (claude-x), 3 spaces between groups.
	reClaude := fmt.Sprintf("^%s claude {5}5h %s{6}%s{2}  79%% %s00:13 {3}7d %s{2}%s{6}  23%% %sSat", sym, fullBlock, emptyBlock, resetGlyph, fullBlock, emptyBlock, resetGlyph)
	if !regexp.MustCompile(reClaude).MatchString(claude) {
		t.Errorf("claude line = %q", claude)
	}
	reOther := fmt.Sprintf("^%s claude-x {3}5h %s{7}%s  91%% ", sym, fullBlock, emptyBlock)
	if !regexp.MustCompile(reOther).MatchString(other) {
		t.Errorf("claude-x line = %q", other)
	}
	// Sparklines start on the same column and end at the current bucket.
	start := func(l string) int {
		for i, r := range []rune(l) {
			for _, g := range sparkGlyphs {
				if string(r) == g {
					return i
				}
			}
		}
		return -1
	}
	if a, b := start(claude), start(other); a != b || a < 0 {
		t.Errorf("sparkline starts at %d and %d", a, b)
	}
	if got := sparkOf(m.Lines[0]); !strings.HasSuffix(got, sparkGlyphs[2]+sparkGlyphs[0]+sparkGlyphs[1]) {
		t.Errorf("claude sparkline = %q", got)
	}
	flat := sparkOf(m.Lines[1])
	if flat != strings.Repeat(sparkGlyphs[0], len([]rune(flat))) || flat == "" {
		t.Errorf("claude-x sparkline = %q", flat)
	}
	for _, l := range m.Lines {
		if SegmentsWidth(l) > 180 {
			t.Errorf("line is %d columns", SegmentsWidth(l))
		}
	}
	if n := len([]rune(sparkOf(m.Lines[0]))); n > TopSparkMax {
		t.Errorf("sparkline has %d cells", n)
	}
}

func colorOf(line []Segment, text string) style.Color {
	for _, s := range line {
		if s.Text == text {
			return s.Color
		}
	}
	return "?"
}

func TestTopBarColors(t *testing.T) {
	m := NewTopBarModel(topInput(func(in *TopBarInput) {
		in.Usage = map[string]domain.UsageView{"claude": usageView(59, 60), "claude-x": usageView(85, 84)}
	}))
	a, b := m.Lines[0], m.Lines[1]
	provider := domain.Providers[domain.ClaudeCode]
	// The provider symbol first, in the provider color; then the label in the profile color.
	if a[0].Text != provider.Symbol || a[0].Color != provider.Color || b[0].Color != provider.Color {
		t.Errorf("provider = %+v", a[0])
	}
	if a[2].Text != "claude" || a[2].Color != "magenta" || b[2].Text != "claude-x" || b[2].Color != "blue" {
		t.Errorf("labels = %+v %+v", a[2], b[2])
	}
	checks := []struct {
		line  []Segment
		text  string
		color style.Color
	}{
		{a, " 59%", "green"}, {a, " 60%", "yellow"}, {b, " 85%", "red"}, {b, " 84%", "yellow"},
		// The empty part of a bar is gray; the full part in the usage color.
		{a, strings.Repeat(fullBlock, 5), "green"}, {a, strings.Repeat(emptyBlock, 3), "gray"},
		// Sparkline: active cells in the profile color, empty ones gray.
		{a, sparkGlyphs[2], "magenta"},
	}
	for _, c := range checks {
		if got := colorOf(c.line, c.text); got != c.color {
			t.Errorf("color of %q = %q, want %q", c.text, got, c.color)
		}
	}
	if last := a[len(a)-2]; last.Text != sparkGlyphs[0] || last.Color != "gray" {
		t.Errorf("second to last = %+v", last)
	}
}

func TestTopBarStale(t *testing.T) {
	stale := usageView(79, 23)
	stale.UpdatedAt, stale.Stale = now-3_600_000, true
	seven := 40.0
	reset := domain.UsageView{
		FiveHour:  &domain.UsageWindowView{},
		SevenDay:  &domain.UsageWindowView{Percent: &seven, ResetsAt: sevenReset},
		UpdatedAt: now - 1_000, Stale: true,
	}
	m := NewTopBarModel(topInput(func(in *TopBarInput) {
		in.Usage = map[string]domain.UsageView{"claude": stale, "claude-x": reset}
	}))
	a, b := m.Lines[0], m.Lines[1]
	if !strings.Contains(text(a), " (stale)") || !strings.Contains(text(b), " (stale)") {
		t.Errorf("no (stale): %q / %q", text(a), text(b))
	}
	if !regexp.MustCompile("5h " + dashGlyph + " +7d " + fullBlock + "{3}").MatchString(text(b)) {
		t.Errorf("a passed window should show a dash: %q", text(b))
	}
	// Everything of the windows is dim (the symbol and the label are not).
	if a[0].Dim || a[2].Dim {
		t.Error("symbol or label is dim")
	}
	for _, s := range a[4:] {
		if strings.TrimSpace(s.Text) != "" && sparkOf([]Segment{s}) == "" && !s.Dim {
			t.Errorf("segment %q is not dim", s.Text)
		}
	}
}

func TestTopBarNoUsage(t *testing.T) {
	sym := domain.Providers[domain.ClaudeCode].Symbol
	none := NewTopBarModel(topInput(func(in *TopBarInput) { in.Usage = nil }))
	a, b := none.Lines[0], none.Lines[1]
	re := fmt.Sprintf(`^%s claude {5}5h %s +7d %s +\(run: agtop setup\)`, sym, dashGlyph, dashGlyph)
	if !regexp.MustCompile(re).MatchString(text(a)) {
		t.Errorf("first line = %q", text(a))
	}
	if strings.Contains(text(b), SetupHint) {
		t.Errorf("hint on the second line: %q", text(b))
	}
	if !regexp.MustCompile(fmt.Sprintf("^%s claude-x {3}5h %s +7d %s", sym, dashGlyph, dashGlyph)).MatchString(text(b)) {
		t.Errorf("second line = %q", text(b))
	}
	for _, s := range a {
		if strings.HasPrefix(s.Text, "5h "+dashGlyph) && !s.Dim {
			t.Error("dash is not dim")
		}
	}
	// Only one profile without data: the hint goes on its line.
	half := NewTopBarModel(topInput(func(in *TopBarInput) {
		in.Usage = map[string]domain.UsageView{"claude": usageView(79, 23)}
	}))
	if strings.Contains(text(half.Lines[0]), SetupHint) || !strings.Contains(text(half.Lines[1]), SetupHint) {
		t.Errorf("half = %q / %q", text(half.Lines[0]), text(half.Lines[1]))
	}
}

func TestTopBarSetupHintProvider(t *testing.T) {
	ocSym := domain.Providers[domain.Opencode].Symbol
	ccSym := domain.Providers[domain.ClaudeCode].Symbol

	// opencode (no usage) first, then Claude Code (no usage): the hint belongs on the Claude
	// Code line only.
	two := NewTopBarModel(topInput(func(in *TopBarInput) {
		in.Profiles = []config.ProfileDir{
			{Label: "oc", Dir: "/tmp/oc", Source: config.ProfileSource("config"), Provider: domain.Opencode},
			{Label: "claude", Dir: "/tmp/a/.claude", Source: config.ProfileSource("default")},
		}
		in.Usage = nil
		in.Providers = map[string]domain.ProviderID{"oc": domain.Opencode, "claude": domain.ClaudeCode}
	}))
	oc, cc := text(two.Lines[0]), text(two.Lines[1])
	if !regexp.MustCompile("^" + ocSym + " oc ").MatchString(oc) {
		t.Errorf("opencode line = %q", oc)
	}
	if strings.Contains(oc, SetupHint) {
		t.Errorf("hint on the opencode line: %q", oc)
	}
	if !regexp.MustCompile("^" + ccSym + " claude .*" + regexp.QuoteMeta(SetupHint)).MatchString(cc) {
		t.Errorf("claude line lacks the setup hint: %q", cc)
	}

	// A single opencode profile without usage: no hint anywhere.
	one := NewTopBarModel(topInput(func(in *TopBarInput) {
		in.Profiles = []config.ProfileDir{
			{Label: "oc", Dir: "/tmp/oc", Source: config.ProfileSource("config"), Provider: domain.Opencode},
		}
		in.Usage = nil
		in.Providers = map[string]domain.ProviderID{"oc": domain.Opencode}
	}))
	if len(one.Lines) != 1 || strings.Contains(text(one.Lines[0]), SetupHint) {
		t.Errorf("single opencode line = %q", text(one.Lines[0]))
	}
}

func TestTopBarProfiles(t *testing.T) {
	sym := domain.Providers[domain.ClaudeCode].Symbol
	one := NewTopBarModel(topInput(func(in *TopBarInput) { in.Profiles = profiles[:1] }))
	if len(one.Lines) != 1 || !regexp.MustCompile("^"+sym+" claude {3}5h ").MatchString(text(one.Lines[0])) {
		t.Errorf("one = %q", text(one.Lines[0]))
	}
	none := NewTopBarModel(topInput(func(in *TopBarInput) { in.Profiles = nil }))
	if len(none.Lines) != 0 || TopBarHeight(none) != 0 {
		t.Errorf("none = %+v", none)
	}
}

func TestTopBarNarrow(t *testing.T) {
	t.Run("120 columns: everything with a shorter sparkline", func(t *testing.T) {
		for _, l := range NewTopBarModel(topInput(cols(120))).Lines {
			n := len([]rune(sparkOf(l)))
			if SegmentsWidth(l) > 120 || !strings.Contains(text(l), resetGlyph) || n < TopSparkMin || n >= TopSparkMax {
				t.Errorf("line %q (%d columns, %d cells)", text(l), SegmentsWidth(l), n)
			}
		}
	})

	t.Run("80 columns: the sparkline goes first, the windows stay whole", func(t *testing.T) {
		for _, l := range NewTopBarModel(topInput(cols(80))).Lines {
			if SegmentsWidth(l) > 80 || !strings.Contains(text(l), resetGlyph) || !strings.Contains(text(l), fullBlock) {
				t.Errorf("line %q", text(l))
			}
		}
		// Without data the hint goes before the sparkline.
		none := NewTopBarModel(topInput(cols(80), func(in *TopBarInput) { in.Usage = nil }))
		if strings.Contains(text(none.Lines[0])+text(none.Lines[1]), SetupHint) {
			t.Error("setup hint at 80 columns")
		}
	})

	t.Run("50 columns: no sparkline, no reset", func(t *testing.T) {
		m := NewTopBarModel(topInput(cols(50)))
		for _, l := range m.Lines {
			if SegmentsWidth(l) > 50 || sparkOf(l) != "" || strings.Contains(text(l), resetGlyph) {
				t.Errorf("line %q", text(l))
			}
		}
		if !regexp.MustCompile(`79%.*23%`).MatchString(text(m.Lines[0])) {
			t.Errorf("line = %q", text(m.Lines[0]))
		}
	})

	t.Run("40 columns: the bars go, only the percent stays", func(t *testing.T) {
		m := NewTopBarModel(topInput(cols(40)))
		for _, l := range m.Lines {
			if SegmentsWidth(l) > 40 || strings.Contains(text(l), fullBlock) {
				t.Errorf("line %q", text(l))
			}
		}
		sym := domain.Providers[domain.ClaudeCode].Symbol
		if !regexp.MustCompile("^" + sym + ` claude {5}5h {2}79% {3}7d {2}23%`).MatchString(text(m.Lines[0])) {
			t.Errorf("line = %q", text(m.Lines[0]))
		}
	})

	t.Run("no sparkline when disabled", func(t *testing.T) {
		m := NewTopBarModel(topInput(func(in *TopBarInput) { in.NoSparkline = true }))
		for _, l := range m.Lines {
			if sparkOf(l) != "" || !strings.Contains(text(l), resetGlyph) {
				t.Errorf("line %q", text(l))
			}
		}
	})

	t.Run("never wider than the terminal, at any width and with hostile labels", func(t *testing.T) {
		wide := strings.Repeat("漢字", 20)
		ps := append([]config.ProfileDir{}, profiles...)
		ps = append(ps, config.ProfileDir{Label: wide, Dir: "/tmp/c", Source: config.ProfileSource("config")})
		for columns := 10; columns <= 200; columns += 7 {
			for _, usage := range []map[string]domain.UsageView{nil, topInput().Usage} {
				m := NewTopBarModel(topInput(cols(columns), func(in *TopBarInput) { in.Profiles, in.Usage = ps, usage }))
				for _, l := range m.Lines {
					if w := ansi.StringWidth(text(l)); w > columns {
						t.Errorf("%d columns: line is %d wide", columns, w)
					}
				}
			}
		}
	})
}
