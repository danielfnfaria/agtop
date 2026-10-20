package model

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// MaxText caps the characters of any displayed string, before the fit by width.
const MaxText = 300

// DefaultEllipsis is the ellipsis of the unicode preset.
const DefaultEllipsis = "…"

// jsRound rounds half up, like Math.round.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// Clip cuts s to limit characters (code points) with an ellipsis. Control characters (C0, DEL and
// C1, including newline, tab and the ESC of ANSI sequences) become spaces first, so a title can
// neither deform the layout nor inject terminal sequences.
func Clip(s string, limit int, ellipsis string) string {
	if limit <= 0 {
		return ""
	}
	clean := strings.Map(func(r rune) rune {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(clean) <= limit {
		return clean
	}
	runes := []rune(clean)
	return string(runes[:limit-1]) + ellipsis
}

// toFixed1 formats x with one decimal like JavaScript's toFixed(1): exact ties round up.
func toFixed1(x float64) string {
	exact := strconv.FormatFloat(x, 'f', 40, 64)
	if dot := strings.IndexByte(exact, '.'); dot >= 0 && exact[dot+2] == '5' && strings.Trim(exact[dot+3:], "0") == "" {
		return strconv.FormatFloat(x+0.05, 'f', 1, 64)
	}
	return strconv.FormatFloat(x, 'f', 1, 64)
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

// FmtTokens writes tokens in short form: 950, 1.2k, 45k, 1.5M.
func FmtTokens(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		return trimZero(toFixed1(float64(n)/1000)) + "k"
	case n < 1_000_000:
		return strconv.Itoa(int(math.Round(float64(n)/1000))) + "k"
	}
	return trimZero(toFixed1(float64(n)/1_000_000)) + "M"
}

// FmtAgo writes a short duration: 5s, 3m, 2h, 4d (rounds down; negative is 0s).
func FmtAgo(ms int64) string {
	s := max(0, ms/1000)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	h := m / 60
	if h < 24 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", h/24)
}

// FmtDuration writes a duration with two levels: 42s, 3m05s, 1h02m.
func FmtDuration(ms int64) string {
	s := max(0, ms/1000)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s%60)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}

// FmtClock writes the HH:MM:SS of epochMs in loc (local time when nil).
func FmtClock(epochMs int64, loc *time.Location) string {
	return time.UnixMilli(epochMs).In(orLocal(loc)).Format("15:04:05")
}

func orLocal(loc *time.Location) *time.Location {
	if loc == nil {
		return time.Local
	}
	return loc
}

// ContextLabel is the context bar and its numbers.
type ContextLabel struct {
	Bar string
	// Filled and Empty are the two halves of Bar, to paint apart.
	Filled, Empty string
	Pct           string
	Numbers       string
	Color         style.Color
}

// NewContextLabel builds the context bar; "~" marks an estimated window (default or inferred),
// every other source is exact.
func NewContextLabel(used int, percent float64, w domain.ContextWindow, width int, g style.Glyphs, t style.Theme) ContextLabel {
	approx := ""
	if w.Source == domain.SourceDefault || w.Source == domain.SourceInferred {
		approx = "~"
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		percent = 0
	}
	clamped := math.Min(100, math.Max(0, percent))
	n := int(jsRound(clamped / 100 * float64(width)))
	filled := strings.Repeat(g.BarFull, n)
	empty := strings.Repeat(g.BarEmpty, width-n)
	return ContextLabel{
		Bar:     filled + empty,
		Filled:  filled,
		Empty:   empty,
		Pct:     fmt.Sprintf("%s%.0f%%", approx, jsRound(percent)),
		Numbers: FmtTokens(used) + "/" + approx + FmtTokens(w.Size),
		Color:   style.ContextColor(clamped, t),
	}
}

// StatusLabel is the status of an agent, with the reason when it failed.
func StatusLabel(a *store.AgentView) string {
	if a.Status == domain.StatusFailed && a.FailureReason != "" {
		return "failed (" + a.FailureReason + ")"
	}
	return string(a.Status)
}

// FitWidth cuts s to at most width terminal columns, by grapheme, with an ellipsis when cut.
// Wide characters (CJK, emoji) count 2 and are never split.
func FitWidth(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	clean := Clip(s, MaxText, ellipsis)
	if ansi.StringWidth(clean) <= width {
		return clean
	}
	return ansi.Truncate(clean, width, ellipsis)
}

// FitBadges lays labels side by side (one space apart) in at most width columns: each fitted
// with FitWidth, the narrowest first, none taking more than an even share of what is left (so a
// long one does not hide a short one). The result keeps the order of labels.
func FitBadges(labels []string, width int) []string {
	out := make([]string, len(labels))
	type entry struct{ i, w int }
	order := make([]entry, len(labels))
	for i, l := range labels {
		order[i] = entry{i, ansi.StringWidth(Clip(l, MaxText, DefaultEllipsis))}
	}
	slices.SortFunc(order, func(a, b entry) int {
		if a.w != b.w {
			return a.w - b.w
		}
		return a.i - b.i
	})
	left := max(0, width-max(0, len(labels)-1))
	for n, e := range order {
		share := left / (len(order) - n)
		fitted := FitWidth(labels[e.i], min(e.w, share), DefaultEllipsis)
		out[e.i] = fitted
		left -= ansi.StringWidth(fitted)
	}
	return out
}

// BadgesWidth is the columns of FitBadges laid out with one space between them.
func BadgesWidth(labels []string) int {
	shown, sum := 0, 0
	for _, l := range labels {
		if l != "" {
			shown++
			sum += ansi.StringWidth(l)
		}
	}
	return sum + max(0, shown-1)
}

// TitleSpinnerColumns are the columns of the spinner slot at the right of a title bar: " ⠋ ─"
// before the corner.
const TitleSpinnerColumns = 4

// TitleBar fits the title of a top border (`╭─ ✳ title ───── ⠋ ─╮`; the spinner slot only with
// spinner). lead is how many columns go before the title after `╭─ ` (the provider symbol and
// its space: 2; 0 for none). It returns the fitted title and how many line characters fill the
// gap after it, so the whole line is exactly width columns.
func TitleBar(title string, width int, spinner bool, lead int, ellipsis string) (fitted string, fill int) {
	// corner + line + space | lead | title | space + fill (>= 1) | spinner slot | corner
	fixed := 3 + lead + 2 + 1
	if spinner {
		fixed += TitleSpinnerColumns
	}
	fitted = FitWidth(title, max(0, width-fixed), ellipsis)
	fill = max(1, width-fixed+1-ansi.StringWidth(fitted))
	return fitted, fill
}

var modelFamilies = []string{"opus", "sonnet", "haiku", "fable"}

// ShortModel is the model name for the agent box: the family (opus...), else a short id. It is
// empty without a model.
func ShortModel(model, ellipsis string) string {
	if model == "" {
		return ""
	}
	lower := strings.ToLower(model)
	for _, f := range modelFamilies {
		if strings.Contains(lower, f) {
			return f
		}
	}
	short := model
	if len(short) >= 7 && strings.EqualFold(short[:7], "claude-") {
		short = short[7:]
	}
	if strings.HasSuffix(short, "]") {
		if i := strings.LastIndexByte(short, '['); i >= 0 {
			short = short[:i]
		}
	}
	if n := len(short); n > 9 && short[n-9] == '-' && allDigits(short[n-8:]) {
		short = short[:n-9]
	}
	if short == "" {
		short = model
	}
	return Clip(short, 12, ellipsis)
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// PRGap is the separator between PR labels.
const PRGap = 2

// PRLayout picks the PRs that fit in width columns, newest first, each as #<n>; more counts the
// ones left out (shown as +N).
func PRLayout(prs []domain.PullRequestRef, width int) (shown []domain.PullRequestRef, more int) {
	newest := slices.Clone(prs)
	slices.Reverse(newest)
	labels := make([]int, len(newest))
	for i, p := range newest {
		labels[i] = len("#" + strconv.Itoa(p.Number))
	}
	for k := len(newest); k >= 0; k-- {
		rest := len(newest) - k
		w := PRGap * max(0, k-1)
		for _, l := range labels[:k] {
			w += l
		}
		if rest > 0 {
			if k > 0 {
				w += PRGap
			}
			w += len("+" + strconv.Itoa(rest))
		}
		if w <= width {
			return newest[:k], rest
		}
	}
	return nil, len(newest)
}

// ActivityLabel is the icon and text of an agent activity; another tool shows its (clipped)
// name.
func ActivityLabel(a domain.AgentActivity, icons style.Icons, ellipsis string) (icon, text string) {
	text = activityText[a.Kind]
	if a.Kind == domain.KindTool && a.Tool != "" {
		text = Clip(a.Tool, 40, ellipsis)
	}
	return activityIcon(a.Kind, icons), text
}

var activityText = map[domain.ActivityKind]string{
	domain.KindWriting:    "writing code",
	domain.KindReading:    "reading",
	domain.KindSearching:  "searching",
	domain.KindWeb:        "web",
	domain.KindDelegating: "delegating",
	domain.KindTests:      "running tests",
	domain.KindBuilding:   "building",
	domain.KindGit:        "git",
	domain.KindInstalling: "installing",
	domain.KindBash:       "running bash",
	domain.KindTool:       "tool",
	domain.KindThinking:   "thinking",
	domain.KindStarting:   "starting",
}

func activityIcon(k domain.ActivityKind, i style.Icons) string {
	switch k {
	case domain.KindWriting:
		return i.Writing
	case domain.KindReading:
		return i.Reading
	case domain.KindSearching:
		return i.Searching
	case domain.KindWeb:
		return i.Web
	case domain.KindDelegating:
		return i.Delegating
	case domain.KindTests:
		return i.Tests
	case domain.KindBuilding:
		return i.Building
	case domain.KindGit:
		return i.Git
	case domain.KindInstalling:
		return i.Installing
	case domain.KindBash:
		return i.Bash
	case domain.KindTool:
		return i.Tool
	case domain.KindThinking:
		return i.Thinking
	case domain.KindStarting:
		return i.Starting
	}
	return ""
}
