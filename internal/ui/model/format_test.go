package model

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const ellipsis = DefaultEllipsis

// kanji is two CJK ideographs (2 columns each).
const kanji = "漢字"

func TestFmtTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"}, {-5, "0"}, {950, "950"}, {1_000, "1k"}, {1_234, "1.2k"}, {1_250, "1.3k"},
		{1_150, "1.1k"}, {45_400, "45k"}, {1_000_000, "1M"}, {1_500_000, "1.5M"}, {1_250_000, "1.3M"},
	}
	for _, tt := range tests {
		if got := FmtTokens(tt.n); got != tt.want {
			t.Errorf("FmtTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFmtAgoAndDuration(t *testing.T) {
	ago := []struct {
		ms   int64
		want string
	}{{-5, "0s"}, {59_999, "59s"}, {125_000, "2m"}, {3 * 3_600_000, "3h"}, {50 * 3_600_000, "2d"}}
	for _, tt := range ago {
		if got := FmtAgo(tt.ms); got != tt.want {
			t.Errorf("FmtAgo(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
	dur := []struct {
		ms   int64
		want string
	}{{-1, "0s"}, {42_000, "42s"}, {65_000, "1m05s"}, {3_720_000, "1h02m"}}
	for _, tt := range dur {
		if got := FmtDuration(tt.ms); got != tt.want {
			t.Errorf("FmtDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestFmtClock(t *testing.T) {
	if got := FmtClock(1_700_000_000_000, utc); got != "22:13:20" {
		t.Errorf("FmtClock = %q", got)
	}
}

func TestClip(t *testing.T) {
	ctl := "a\nb\tc\x1b[31m\x7fd\u0085e\u009bf"
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"cut", "abcdef", 4, "abc…"},
		{"fits", "abc", 4, "abc"},
		{"zero", "abc", 0, ""},
		{"controls become spaces", ctl, 100, "a b c [31m d e f"},
		{"cut after replace", "ab\ncdef", 4, "ab …"},
		{"code points", "日本語日本語", 4, "日本語…"},
	}
	for _, tt := range tests {
		if got := Clip(tt.in, tt.max, ellipsis); got != tt.want {
			t.Errorf("%s: Clip = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestContextLabel(t *testing.T) {
	g, th := style.UnicodeGlyphs, style.DarkTheme
	tests := []struct {
		name         string
		used         int
		percent      float64
		window       domain.ContextWindow
		pct, numbers string
	}{
		{"table is exact", 90_000, 45, domain.ContextWindow{Size: 200_000, Source: domain.SourceTable}, "45%", "90k/200k"},
		{"default is approximate", 50_000, 25, domain.ContextWindow{Size: 200_000, Source: domain.SourceDefault}, "~25%", "50k/~200k"},
		{"inferred is approximate", 215_000, 21.5, domain.ContextWindow{Size: 1_000_000, Source: domain.SourceInferred}, "~22%", "215k/~1M"},
		{"reported", 215_000, 21.5, domain.ContextWindow{Size: 1_000_000, Source: domain.SourceReported}, "22%", "215k/1M"},
		{"model", 215_000, 21.5, domain.ContextWindow{Size: 1_000_000, Source: domain.SourceModel}, "22%", "215k/1M"},
		{"suffix", 215_000, 21.5, domain.ContextWindow{Size: 1_000_000, Source: domain.SourceSuffix}, "22%", "215k/1M"},
	}
	for _, tt := range tests {
		l := NewContextLabel(tt.used, tt.percent, tt.window, 10, g, th)
		if l.Pct != tt.pct || l.Numbers != tt.numbers {
			t.Errorf("%s: got %q %q, want %q %q", tt.name, l.Pct, l.Numbers, tt.pct, tt.numbers)
		}
	}
	over := NewContextLabel(300_000, 150, domain.ContextWindow{Size: 200_000, Source: domain.SourceTable}, 10, g, th)
	if over.Empty != "" || over.Pct != "150%" || over.Color != "red" {
		t.Errorf("over = %+v", over)
	}
	half := NewContextLabel(1, 50, domain.ContextWindow{Size: 2, Source: domain.SourceTable}, 10, g, th)
	if half.Bar != strings.Repeat("█", 5)+strings.Repeat("░", 5) || half.Filled != strings.Repeat("█", 5) {
		t.Errorf("bar = %q", half.Bar)
	}
}

func TestStatusLabel(t *testing.T) {
	tests := []struct {
		a    store.AgentView
		want string
	}{
		{store.AgentView{Status: domain.StatusFailed, FailureReason: "stopped"}, "failed (stopped)"},
		{store.AgentView{Status: domain.StatusFailed}, "failed"},
		{store.AgentView{Status: domain.StatusRunning}, "running"},
	}
	for _, tt := range tests {
		if got := StatusLabel(&tt.a); got != tt.want {
			t.Errorf("StatusLabel = %q, want %q", got, tt.want)
		}
	}
}

func TestFitWidth(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"wide enough", "abcdef", 10, "abcdef"},
		{"exact", "abcdef", 6, "abcdef"},
		{"cut", "abcdef", 4, "abc…"},
		{"zero", "abc", 0, ""},
		{"negative", "abc", -1, ""},
		{"cjk exact", kanji, 4, kanji},
		{"cjk cut leaves one ideograph", kanji, 3, "漢…"},
		{"cjk does not fit beside ellipsis", kanji, 2, "…"},
		{"controls become spaces", "a\x1b[31mb", 20, "a [31mb"},
	}
	for _, tt := range tests {
		if got := FitWidth(tt.in, tt.width, ellipsis); got != tt.want {
			t.Errorf("%s: FitWidth = %q, want %q", tt.name, got, tt.want)
		}
	}
	if w := ansi.StringWidth(kanji); w != 4 {
		t.Errorf("width of kanji = %d", w)
	}
}

func TestFitBadges(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		width  int
		want   []string
	}{
		{"everything fits", []string{"sdk-cli", "claude-x"}, 16, []string{"sdk-cli", "claude-x"}},
		{"short keeps its width, long takes the rest", []string{"sdk-cli", "a-very-long-profile"}, 16, []string{"sdk-cli", "a-very-…"}},
	}
	for _, tt := range tests {
		if got := FitBadges(tt.labels, tt.width); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
	out := FitBadges([]string{strings.Repeat(kanji, 4), strings.Repeat("b", 20)}, 12)
	total := 1
	for _, l := range out {
		total += ansi.StringWidth(l)
		if !strings.HasSuffix(l, ellipsis) {
			t.Errorf("%q should end with an ellipsis", l)
		}
	}
	if total > 12 {
		t.Errorf("badges take %d columns, want <= 12", total)
	}
	if got := BadgesWidth([]string{"ab", "", "cde"}); got != 6 {
		t.Errorf("BadgesWidth = %d", got)
	}
}

func TestTitleBar(t *testing.T) {
	titles := []string{"short", strings.Repeat("x", 80), strings.Repeat(kanji, 20)}
	for _, lead := range []int{0, 2} {
		for _, spinner := range []bool{false, true} {
			for _, title := range titles {
				fitted, fill := TitleBar(title, 34, spinner, lead, ellipsis)
				// corner + line + space | lead | title | space + fill | spinner slot | corner
				total := 3 + lead + ansi.StringWidth(fitted) + 1 + fill + 1
				if spinner {
					total += TitleSpinnerColumns
				}
				if total != 34 || fill < 1 {
					t.Errorf("lead=%d spinner=%v title=%q: total %d fill %d", lead, spinner, title, total, fill)
				}
			}
		}
	}
	if got, _ := TitleBar("short", 34, false, 0, ellipsis); got != "short" {
		t.Errorf("short title = %q", got)
	}
	if got, _ := TitleBar(strings.Repeat("x", 80), 34, true, 0, ellipsis); !strings.HasSuffix(got, ellipsis) {
		t.Errorf("long title = %q", got)
	}
	long := strings.Repeat("x", 80)
	with, _ := TitleBar(long, 34, false, 2, ellipsis)
	without, _ := TitleBar(long, 34, false, 0, ellipsis)
	if ansi.StringWidth(with) != ansi.StringWidth(without)-2 {
		t.Errorf("lead should come out of the title: %d vs %d", ansi.StringWidth(with), ansi.StringWidth(without))
	}
}

func TestPRLayout(t *testing.T) {
	pr := func(n int) domain.PullRequestRef { return domain.PullRequestRef{Owner: "a", Repo: "b", Number: n} }
	nums := func(prs []domain.PullRequestRef) []int {
		out := []int{}
		for _, p := range prs {
			out = append(out, p.Number)
		}
		return out
	}
	eight := make([]domain.PullRequestRef, 8)
	for i := range eight {
		eight[i] = pr(10_001 + i)
	}
	tests := []struct {
		name  string
		prs   []domain.PullRequestRef
		width int
		want  []int
		more  int
	}{
		{"newest first, all fit", []domain.PullRequestRef{pr(1), pr(2), pr(3)}, 30, []int{3, 2, 1}, 0},
		// Four `#1000N` (6 each, 2 apart) = 30; with `+4` it would be 34: three and `+5`.
		{"+N counts the ones left out", eight, 30, []int{10_008, 10_007, 10_006}, 5},
		{"nothing fits", []domain.PullRequestRef{pr(123_456)}, 3, []int{}, 1},
		{"empty", nil, 10, []int{}, 0},
	}
	for _, tt := range tests {
		shown, more := PRLayout(tt.prs, tt.width)
		if !reflect.DeepEqual(nums(shown), tt.want) || more != tt.more {
			t.Errorf("%s: got %v +%d, want %v +%d", tt.name, nums(shown), more, tt.want, tt.more)
		}
	}
}

func TestShortModel(t *testing.T) {
	tests := []struct{ model, want string }{
		{"claude-opus-5-5", "opus"},
		{"claude-sonnet-4-5-20250929", "sonnet"},
		{"Claude-Haiku-4", "haiku"},
		{"claude-fable-1[1m]", "fable"},
		{"claude-mystery-2-20250101", "mystery-2"},
		{"claude-mystery[1m]", "mystery"},
		{"some-really-long-model-name", "some-really…"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ShortModel(tt.model, ellipsis); got != tt.want {
			t.Errorf("ShortModel(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestActivityLabel(t *testing.T) {
	icons := style.UnicodeGlyphs.Icons
	tests := []struct {
		a          domain.AgentActivity
		icon, text string
	}{
		{domain.AgentActivity{Kind: domain.KindWriting}, icons.Writing, "writing code"},
		{domain.AgentActivity{Kind: domain.KindTests}, icons.Tests, "running tests"},
		{domain.AgentActivity{Kind: domain.KindThinking}, icons.Thinking, "thinking"},
		{domain.AgentActivity{Kind: domain.KindTool, Tool: "mcp__x__do"}, icons.Tool, "mcp__x__do"},
		{domain.AgentActivity{Kind: domain.KindTool}, icons.Tool, "tool"},
	}
	for _, tt := range tests {
		icon, text := ActivityLabel(tt.a, icons, ellipsis)
		if icon != tt.icon || text != tt.text {
			t.Errorf("%v: got %q %q, want %q %q", tt.a, icon, text, tt.icon, tt.text)
		}
	}
	_, long := ActivityLabel(domain.AgentActivity{Kind: domain.KindTool, Tool: "a\x1bb" + strings.Repeat("c", 60)}, icons, ellipsis)
	if utf8.RuneCountInString(long) != 40 || strings.ContainsRune(long, 0x1b) {
		t.Errorf("long tool = %q", long)
	}
}
