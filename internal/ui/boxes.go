package ui

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// painter carries the look and the clocks every view draws with.
type painter struct {
	g      style.Glyphs
	t      style.Theme
	layout style.Layout
	// now is the one-second clock; spin the spinner clock (epoch ms).
	now, spin int64
	loc       *time.Location
	// links turns PR numbers into OSC 8 hyperlinks (only on a terminal).
	links bool
}

const (
	// durationReserve is the columns kept for the duration and the gap before it.
	durationReserve = 7
	// providerLead is the provider symbol and its space before a title.
	providerLead = 2
)

// safePRURL is the only shape of link that goes into an OSC 8 sequence.
var safePRURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9-]+/[A-Za-z0-9._-]+/pull/[0-9]+$`)

func stale(s *store.SessionView) bool { return s.Liveness == store.Stale }

func busy(s *store.SessionView) bool {
	return !stale(s) && style.Activity(s.Activity) == style.ActivityBusy
}

// pending is a live background session whose transcript does not exist yet: it was launched in
// the background but has not started writing.
func pending(s *store.SessionView) bool {
	return !stale(s) && s.Kind == "background" && !s.HasTranscript
}

func (p painter) spinner() string {
	frames := p.g.Spinner
	if len(frames) == 0 {
		return " "
	}
	return frames[style.SpinnerFrame(p.spin, len(frames))]
}

// ago is "5s ago" (or the clock time with time = "absolute"); fallback without a time.
func (p painter) ago(since int64, prefix, suffix, fallback string) string {
	if since == 0 {
		return fallback
	}
	if p.layout.Time == "absolute" {
		return prefix + model.FmtClock(since, p.loc)
	}
	return prefix + model.FmtAgo(p.now-since) + suffix
}

func (p painter) duration(a *store.AgentView) string {
	switch {
	case a.StartedAt == 0:
		return p.g.Dash
	case a.FinishedAt != 0:
		return model.FmtDuration(a.FinishedAt - a.StartedAt)
	}
	return model.FmtDuration(p.now - a.StartedAt)
}

// fitOwn cuts text with the glyph ellipsis when it is not the terminal's own (Ink cuts with "…").
func fitOwn(text string, width int, ellipsis string) string {
	if ellipsis == model.DefaultEllipsis {
		return text
	}
	return model.FitWidth(text, max(0, width), ellipsis)
}

func modelLabel(s *store.SessionView, fallback string) string {
	if s.Model == nil {
		return fallback
	}
	if s.Model.Name != "" {
		return s.Model.Name
	}
	if s.Model.ID != "" {
		return s.Model.ID
	}
	return fallback
}

func kindBadge(s *store.SessionView) string {
	if s.Kind == "" || s.Kind == "interactive" {
		return ""
	}
	return s.Kind
}

// border draws the sides and the bottom of a box whose top row is drawn apart.
func (p painter) border(c *canvas, x, y, w, h int, b style.Box, st sty) {
	for r := 1; r < h-1; r++ {
		c.text(x, y+r, seg{b.Left, st})
		c.text(x+w-1, y+r, seg{b.Right, st})
	}
	c.text(x, y+h-1, seg{b.BottomLeft + strings.Repeat(b.Bottom, max(0, w-2)) + b.BottomRight, st})
}

// sessionBox draws the box of a session at (x, y) with as much of it as its step holds (see
// model.SessionStep). The title bar carries the state of the session at its right, and under it
// come the content lines of the step: the profile, the model and the context on one line and the
// agent counts with the PRs on the next (the full box), the same fields behind their glyph on a
// single line (the compact one) or nothing at all (the smallest one).
func (p painter) sessionBox(c *canvas, x, y int, s *store.SessionView, step model.SessionStep, size model.SessionSize, selected, showProfile bool, tone style.Color) {
	g, t := p.g, p.t
	b := sessionPaint{painter: p, c: c, s: s, cx: x + 2, inner: size.Width - 4,
		isStale: stale(s), isPending: pending(s)}
	if b.isStale {
		b.tonef = t.Muted
	}
	color := style.SessionBorderColor(style.Activity(s.Activity), string(s.Liveness), t)
	if b.isPending {
		color = t.Pending
	}
	edge, box := g.TopRound, g.BorderRound
	if selected {
		edge, box = g.TopBold, g.BorderBold
	}
	edgeSt := sty{fg: color, dim: b.isStale}

	// The state of the session rides on the title bar: on the smallest step it is the only thing
	// left of it, so it is never dropped, only shortened.
	state := b.state(step != model.SessionTiny, step == model.SessionFull)
	title, fill := model.TitleBar(s.DisplayTitle, size.Width, providerLead, segsWidth(state), g.Ellipsis)
	provider := domain.ProviderInfo(s.Provider)
	titleSt := sty{bold: true, inverse: selected}
	if selected || b.isStale {
		titleSt.fg = color
	}
	bar := []seg{
		{edge.Left + edge.Line + " ", edgeSt},
		{g.ProviderMark(provider.Symbol), sty{fg: provider.Color, dim: b.isStale}},
		plain(" "),
		{title, titleSt},
		{" " + strings.Repeat(edge.Line, fill), edgeSt},
		plain(" "),
	}
	bar = append(bar, state...)
	bar = append(bar, fg(" "+edge.Line, color), seg{edge.Right, edgeSt})
	c.line(x, y, size.Width, g.Ellipsis, bar...)
	p.border(c, x, y, size.Width, size.Height, box, edgeSt)

	row := y + 1
	switch step {
	case model.SessionTiny:
		// No content line of its own: the one case that keeps it is the profile label, which
		// nothing else on the box would tell (see model.SessionStep.Size).
		if showProfile {
			b.fieldLine(row, tone, true)
		}
	case model.SessionCompact:
		b.fieldLine(row, tone, showProfile)
	default:
		b.modelLine(row, tone, showProfile)
		b.agentsLine(row + 1)
	}
}

// sessionPaint is one session box being drawn: what every line of it shares.
type sessionPaint struct {
	painter
	c *canvas
	s *store.SessionView
	// cx is the first content column and inner the content width (border and padding off).
	cx, inner int
	isStale   bool
	isPending bool
	// tonef mutes the text of a session that ended ("" while it lives).
	tonef style.Color
}

// state is what the session is doing, for the right of the title bar: the mark (the spinner
// while it is busy, a dot otherwise), with text what it is doing and with withTime since when.
func (b sessionPaint) state(withText, withTime bool) []seg {
	g, t := b.g, b.t
	mark, text := fg(g.Dot, t.Muted), "ended"
	switch {
	case b.isStale:
		if withTime {
			text = b.ago(b.s.StaleSince, "ended ", "", "ended")
		}
	case b.isPending:
		mark, text = fg(g.Dot, t.Pending), "bg"
	default:
		activity := style.Activity(b.s.Activity)
		mark, text = fg(g.Dot, style.ActivityColor(activity, t)), style.ActivityText(activity, b.s.RawStatus)
		if activity == style.ActivityBusy {
			mark = fg(b.spinner(), t.Busy)
		}
		if withTime {
			if since := b.ago(b.s.LastActivityAt, "", "", ""); since != "" {
				text += " " + since
			}
		}
	}
	if !withText {
		return []seg{mark}
	}
	return []seg{mark, plain(" " + text)}
}

// profileColor is the color of the label of the profile of the session.
func (b sessionPaint) profileColor(tone style.Color) style.Color {
	switch {
	case b.isStale:
		return b.t.Muted
	case tone != "":
		return tone
	case len(b.t.Profiles) > 0:
		return b.t.Profiles[0]
	}
	return ""
}

// profileField is the label of the profile, in the color of that profile.
func (b sessionPaint) profileField(tone style.Color) []seg {
	return []seg{fg(b.s.ProfileLabel, b.profileColor(tone))}
}

func (b sessionPaint) ctxLabel() model.ContextLabel {
	return model.NewContextLabel(b.s.ContextUsed, b.s.ContextPercent, b.s.ContextWindow, 10, b.g, b.t)
}

// ctxField is the share of the context window the session has used.
func (b sessionPaint) ctxField(glyph bool) []seg {
	ctx := b.ctxLabel()
	color := ctx.Color
	if b.isStale {
		color = b.t.Muted
	}
	if glyph {
		return []seg{fg(b.g.Context+" "+ctx.Pct, color)}
	}
	return []seg{fg(ctx.Pct, color)}
}

// agentsField is the active agents over the total, as a count (`1/20 agents`) or behind the gear
// (`⚙ 1/20`); it is empty with the glyph when the session never launched one.
func (b sessionPaint) agentsField(glyph bool) []seg {
	active, total := agentCounts(b.s.Agents)
	color := b.tonef
	if color == "" {
		color = b.t.Muted
		if active > 0 {
			color = b.t.Busy
		}
	}
	counts := strconv.Itoa(active) + "/" + strconv.Itoa(total)
	if glyph {
		if total == 0 {
			return nil
		}
		return []seg{fg(b.g.Gear+" "+counts, color)}
	}
	if total == 0 {
		return []seg{fg("no agents", color)}
	}
	return []seg{fg(counts+" agents", color)}
}

// prField is the PR numbers of the session, as many as fit in width columns; it is empty without
// a PR or without the room for one.
func (b sessionPaint) prField(width int) []seg {
	if len(b.s.PRs) == 0 || width <= 0 {
		return nil
	}
	return b.prSegs(b.s.PRs, width, b.isStale)
}

// modelLine is the first content line of the full box: the profile, the model with its effort
// and the context percent, with the kind badge of a session that is not interactive at its right.
// The model is the one field that gives way, so the percent never falls off the line.
func (b sessionPaint) modelLine(row int, tone style.Color, showProfile bool) {
	g, t := b.g, b.t
	room := b.inner
	if badges := model.FitBadges([]string{kindBadge(b.s)}, b.inner/2); len(badges) > 0 {
		w := model.BadgesWidth(badges)
		b.c.line(b.cx+b.inner-w, row, w, g.Ellipsis, fg(badges[0], t.Muted))
		room = max(0, b.inner-w-1)
	}
	var head, tail [][]seg
	if showProfile {
		head = append(head, b.profileField(tone))
	}
	if b.s.Effort != "" {
		tail = append(tail, []seg{fg(b.s.Effort, b.tonef)})
	}
	tail = append(tail, b.ctxField(false))
	used := 0
	for _, f := range head {
		used += segsWidth(f) + fieldGap
	}
	for _, f := range tail {
		used += segsWidth(f) + fieldGap
	}
	name := model.FitWidth(modelLabel(b.s, "unknown model"), max(0, room-used), g.Ellipsis)
	fields := append(head, []seg{fg(name, b.tonef)})
	fields = append(fields, tail...)
	b.c.line(b.cx, row, room, g.Ellipsis, b.joinFields(fields)...)
}

// agentsLine is the second content line of the full box: the agent counts and the PRs.
func (b sessionPaint) agentsLine(row int) {
	fields := [][]seg{b.agentsField(false)}
	fields = append(fields, b.prField(b.inner-segsWidth(fields[0])-fieldGap))
	b.c.line(b.cx, row, b.inner, b.g.Ellipsis, b.joinFields(fields)...)
}

// fieldLine is the single content line of the smaller steps: the same fields as the full box,
// each behind its glyph, so what is a word on the full box costs one column here.
func (b sessionPaint) fieldLine(row int, tone style.Color, showProfile bool) {
	var fields [][]seg
	if showProfile {
		fields = append(fields, b.profileField(tone))
	}
	fields = append(fields, b.agentsField(true), b.ctxField(true))
	used := 0
	for _, f := range fields {
		if len(f) > 0 {
			used += segsWidth(f) + fieldGap
		}
	}
	fields = append(fields, b.prField(b.inner-used))
	b.c.line(b.cx, row, b.inner, b.g.Ellipsis, b.joinFields(fields)...)
}

// fieldGap is the columns the middot between two fields takes.
const fieldGap = 3

// joinFields puts a middot between the fields of a content line, the empty ones left out.
func (b sessionPaint) joinFields(fields [][]seg) []seg {
	var out []seg
	for _, f := range fields {
		if len(f) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, fg(" "+b.g.Middot+" ", b.t.Muted))
		}
		out = append(out, f...)
	}
	return out
}

// segsWidth is the columns a run of segments takes.
func segsWidth(segs []seg) int {
	w := 0
	for _, s := range segs {
		w += ansi.StringWidth(s.text)
	}
	return w
}

// prSegs is the PR numbers of a session, newest first, as many as fit in width columns (+N for
// the rest).
func (p painter) prSegs(prs []domain.PullRequestRef, width int, muted bool) []seg {
	t := p.t
	shown, more := model.PRLayout(prs, width)
	color := t.Link
	if muted {
		color = t.Muted
	}
	gap := strings.Repeat(" ", model.PRGap)
	var out []seg
	for i, pr := range shown {
		if i > 0 {
			out = append(out, plain(gap))
		}
		st := sty{fg: color}
		if p.links && safePRURL.MatchString(pr.URL) {
			st.link = pr.URL
		}
		out = append(out, seg{"#" + strconv.Itoa(pr.Number), st})
	}
	if more > 0 {
		lead := ""
		if len(shown) > 0 {
			lead = gap
		}
		out = append(out, fg(lead+"+"+strconv.Itoa(more), t.Muted))
	}
	return out
}

// nestMark is the marker a nested sub-agent carries in the title of its box. Every level gets
// the same one: in the tree the place of the box already tells how deep it sits (the flat list
// of the detail is the one that needs the depth, see nestIndent). It goes inline, so the box
// keeps its height.
func nestMark(nested bool, g style.Glyphs) string {
	if !nested {
		return ""
	}
	return g.Line.UpRight + " "
}

// shortDuration is the duration of an agent in the shortest form (5s, 3m, 2h), for the steps
// with no room for the exact one.
func (p painter) shortDuration(a *store.AgentView) string {
	switch {
	case a.StartedAt == 0:
		return p.g.Dash
	case a.FinishedAt != 0:
		return model.FmtAgo(a.FinishedAt - a.StartedAt)
	}
	return model.FmtAgo(p.now - a.StartedAt)
}

// agentTitle is the first line of an agent box: the nesting marker, the kind and, with the
// model, `kind · opus`, in at most width columns.
func (p painter) agentTitle(a *store.AgentView, nested bool, width int, withModel bool) []seg {
	g := p.g
	nest := nestMark(nested, g)
	name := a.Type
	if withModel {
		if short := model.ShortModel(a.Model, g.Ellipsis); short != "" {
			name = a.Type + " " + g.Middot + " " + short
		}
	}
	name = fitOwn(model.Clip(name, model.MaxText, g.Ellipsis), width-ansi.StringWidth(nest), g.Ellipsis)
	return []seg{fg(nest, p.t.Muted), {name, sty{bold: true}}}
}

// agentActivity is the activity line of an agent box: the spinner with the activity while it
// runs, else its status. icon tells whether the activity icon fits.
func (p painter) agentActivity(a *store.AgentView, width int, color style.Color, icon bool) []seg {
	g := p.g
	if !domain.IsActive(a.Status) || a.Activity == nil {
		label := fitOwn(model.Clip(model.StatusLabel(a), model.MaxText, g.Ellipsis), width-2, g.Ellipsis)
		return []seg{fg(g.Dot+" "+label, color)}
	}
	mark, text := model.ActivityLabel(*a.Activity, g.Icons, g.Ellipsis)
	if !icon {
		return []seg{fg(p.spinner(), color), plain(" " + fitOwn(text, width-2, g.Ellipsis))}
	}
	return []seg{fg(p.spinner(), color), plain(" " + mark + " " + fitOwn(text, width-4, g.Ellipsis))}
}

// agentBox draws the box of an active agent at (x, y), at the given depth of the forest, with as
// much of it as the step of the cluster holds.
func (p painter) agentBox(c *canvas, x, y, depth int, step model.AgentStep, a *store.AgentView) {
	g, t := p.g, p.t
	color := style.StatusColor(a.Status, t)
	b := g.BorderRound
	st := sty{fg: color}
	box := step.Size()
	inner := box.Width - 4
	cx := x + 2

	if step == model.StepTiny {
		// The smallest box has room for a single line, so the kind goes in the top border. It is
		// cut for real, never by the terminal: the corner comes after it. What the kind leaves of
		// the border is line, as on the title bar of a session, so the box never looks open.
		kind := model.FitWidth(a.Type, inner, g.Ellipsis)
		fill := strings.Repeat(b.Top, max(0, inner-ansi.StringWidth(kind)))
		c.line(x, y, box.Width, g.Ellipsis,
			seg{b.TopLeft + " ", st}, seg{kind, sty{bold: true}}, seg{" " + fill + b.TopRight, st})
		p.border(c, x, y, box.Width, box.Height, b, st)
		mark := fg(g.Dot, color)
		if domain.IsActive(a.Status) && a.Activity != nil {
			mark = fg(p.spinner(), color)
		}
		dur := p.shortDuration(a)
		c.line(cx, y+1, inner, g.Ellipsis, mark, plain(" "+fitOwn(dur, inner-2, g.Ellipsis)))
		return
	}

	c.text(x, y, seg{b.TopLeft + strings.Repeat(b.Top, box.Width-2) + b.TopRight, st})
	p.border(c, x, y, box.Width, box.Height, b, st)

	// The kind has to stay readable: on the small steps the nesting marker gives way to it (the
	// place of the box in the tree already tells the level) and the duration is the short one.
	dur, nested, reserve := p.duration(a), depth > 0, durationReserve
	if step == model.StepCompact {
		dur, nested = p.shortDuration(a), false
		reserve = ansi.StringWidth(dur) + 1
	}
	dw := ansi.StringWidth(dur)
	c.line(cx, y+1, max(0, inner-1-dw), g.Ellipsis, p.agentTitle(a, nested, inner-reserve, step == model.StepFull)...)
	c.line(cx+inner-dw, y+1, dw, g.Ellipsis, plain(dur))

	c.line(cx, y+2, inner, g.Ellipsis, p.agentActivity(a, inner, color, step != model.StepCompact)...)
	if step == model.StepCompact {
		return
	}

	desc := "no description"
	if a.Description != "" {
		desc = fitOwn(model.Clip(a.Description, model.MaxText, g.Ellipsis), inner, g.Ellipsis)
	}
	c.line(cx, y+3, inner, g.Ellipsis, fg(desc, t.Muted))
}
