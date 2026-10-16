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
	// sessionContent is the columns inside a session box (border and padding off).
	sessionContent = model.SessionWidth - 4
	// agentContent is the columns inside an agent box.
	agentContent = model.AgentWidth - 4
	// durationReserve is the columns kept for the duration and the gap before it.
	durationReserve = 7
	// badgesShare is the least share of the model line the kind badge gets.
	badgesShare = 0.4
	// providerLead is the provider symbol and its space before a title.
	providerLead = 2
)

// safePRURL is the only shape of link that goes into an OSC 8 sequence.
var safePRURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9-]+/[A-Za-z0-9._-]+/pull/[0-9]+$`)

func stale(s *store.SessionView) bool { return s.Liveness == store.Stale }

func busy(s *store.SessionView) bool {
	return !stale(s) && style.Activity(s.Activity) == style.ActivityBusy
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

// modelLineLayout splits the model line: the model on the left, the kind badge on the right with
// at least badgesShare of the line.
func modelLineLayout(modelText, kind string, width int, ellipsis string) (string, string) {
	if kind == "" {
		return model.FitWidth(modelText, width, ellipsis), ""
	}
	modelWidth := ansi.StringWidth(model.Clip(modelText, model.MaxText, model.DefaultEllipsis))
	budget := max(int(float64(width)*badgesShare), width-1-modelWidth)
	fitted := model.FitBadges([]string{kind}, budget)
	used := model.BadgesWidth(fitted)
	room := width
	if used > 0 {
		room = width - used - 1
	}
	shown := ""
	if len(fitted) > 0 {
		shown = fitted[0]
	}
	return model.FitWidth(modelText, max(0, room), ellipsis), shown
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

// sessionBox draws the box of a session at (x, y): the title bar, the profile line (2+
// profiles), model, context, agents, PRs and activity, then the bottom border.
func (p painter) sessionBox(c *canvas, x, y int, s *store.SessionView, selected, showProfile bool, tone style.Color) {
	g, t := p.g, p.t
	isStale := stale(s)
	color := style.SessionBorderColor(style.Activity(s.Activity), string(s.Liveness), t)
	edge, box := g.TopRound, g.BorderRound
	if selected {
		edge, box = g.TopBold, g.BorderBold
	}
	edgeSt := sty{fg: color, dim: isStale}
	isBusy := busy(s)
	title, fill := model.TitleBar(s.DisplayTitle, model.SessionWidth, isBusy, providerLead, g.Ellipsis)
	provider := domain.ProviderInfo(s.Provider)
	titleSt := sty{bold: true, inverse: selected}
	if selected || isStale {
		titleSt.fg = color
	}
	bar := []seg{
		{edge.Left + edge.Line + " ", edgeSt},
		{g.ProviderMark(provider.Symbol), sty{fg: provider.Color, dim: isStale}},
		plain(" "),
		{title, titleSt},
		{" " + strings.Repeat(edge.Line, fill), edgeSt},
	}
	if isBusy {
		bar = append(bar, plain(" "), fg(p.spinner(), t.Busy), fg(" "+edge.Line, color))
	}
	bar = append(bar, seg{edge.Right, edgeSt})
	c.line(x, y, model.SessionWidth, g.Ellipsis, bar...)

	h := model.SessionBoxHeight(showProfile)
	p.border(c, x, y, model.SessionWidth, h, box, edgeSt)

	cx, row := x+2, y+1
	var tonef style.Color
	if isStale {
		tonef = t.Muted
	}
	if showProfile {
		text := model.FitWidth(s.ProfileLabel, sessionContent, g.Ellipsis)
		pad := (sessionContent - ansi.StringWidth(text)) / 2
		col := tone
		if col == "" && len(t.Profiles) > 0 {
			col = t.Profiles[0]
		}
		if isStale {
			col = t.Muted
		}
		c.line(cx+pad, row, sessionContent-pad, g.Ellipsis, fg(text, col))
		row++
	}

	name := modelLabel(s, "unknown model")
	modelText := name
	if s.Effort != "" {
		modelText = name + " " + g.Middot + " " + s.Effort
	}
	m, kind := modelLineLayout(modelText, kindBadge(s), sessionContent, g.Ellipsis)
	kindW := 0
	if kind != "" {
		kindW = ansi.StringWidth(kind)
		c.line(cx+sessionContent-kindW, row, kindW, g.Ellipsis, fg(kind, t.Muted))
		kindW++
	}
	c.line(cx, row, sessionContent-kindW, g.Ellipsis, fg(m, tonef))
	row++

	ctx := model.NewContextLabel(s.ContextUsed, s.ContextPercent, s.ContextWindow, 10, g, t)
	barColor := ctx.Color
	if isStale {
		barColor = t.Muted
	}
	c.line(cx, row, sessionContent, g.Ellipsis,
		fg("ctx ", tonef), fg(ctx.Bar, barColor),
		fg(" "+padStart(ctx.Pct, 4)+"  "+ctx.Numbers, tonef))
	row++

	active, total := agentCounts(s.Agents)
	agentsColor := tonef
	if agentsColor == "" {
		agentsColor = t.Muted
		if active > 0 {
			agentsColor = t.Busy
		}
	}
	agents := "none"
	if total > 0 {
		agents = strconv.Itoa(active) + " active " + g.Middot + " " + strconv.Itoa(total) + " total"
	}
	c.line(cx, row, sessionContent, g.Ellipsis, fg("agents  "+agents, agentsColor))
	row++

	c.line(cx, row, sessionContent, g.Ellipsis, p.prLine(s.PRs, isStale)...)
	row++

	if isStale {
		c.line(cx, row, sessionContent, g.Ellipsis,
			fg(g.Dot+" "+p.ago(s.StaleSince, "ended ", " ago", "ended"), t.Muted))
	} else {
		c.line(cx, row, sessionContent, g.Ellipsis,
			fg(g.Dot, style.ActivityColor(style.Activity(s.Activity), t)),
			plain(" "+style.ActivityText(style.Activity(s.Activity), s.RawStatus)+" "+g.Middot+" "+
				p.ago(s.LastActivityAt, "", " ago", "no activity")))
	}
}

// prLine is the PR numbers, newest first, as many as fit (+N for the rest), or "no PRs".
func (p painter) prLine(prs []domain.PullRequestRef, muted bool) []seg {
	t := p.t
	if len(prs) == 0 {
		return []seg{{"no PRs", sty{fg: t.Muted, dim: true}}}
	}
	shown, more := model.PRLayout(prs, sessionContent)
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

// agentBox draws the compact box of an active agent at (x, y).
func (p painter) agentBox(c *canvas, x, y int, a *store.AgentView) {
	g, t := p.g, p.t
	color := style.StatusColor(a.Status, t)
	b := g.BorderRound
	st := sty{fg: color}
	c.text(x, y, seg{b.TopLeft + strings.Repeat(b.Top, model.AgentWidth-2) + b.TopRight, st})
	p.border(c, x, y, model.AgentWidth, model.AgentHeight, b, st)
	cx := x + 2

	dur := p.duration(a)
	dw := ansi.StringWidth(dur)
	name := a.Type
	if short := model.ShortModel(a.Model, g.Ellipsis); short != "" {
		name = a.Type + " " + g.Middot + " " + short
	}
	name = fitOwn(model.Clip(name, model.MaxText, g.Ellipsis), agentContent-durationReserve, g.Ellipsis)
	c.line(cx, y+1, max(0, agentContent-1-dw), g.Ellipsis, seg{name, sty{bold: true}})
	c.line(cx+agentContent-dw, y+1, dw, g.Ellipsis, plain(dur))

	if !domain.IsActive(a.Status) || a.Activity == nil {
		label := fitOwn(model.Clip(model.StatusLabel(a), model.MaxText, g.Ellipsis), agentContent-2, g.Ellipsis)
		c.line(cx, y+2, agentContent, g.Ellipsis, fg(g.Dot+" "+label, color))
	} else {
		icon, text := model.ActivityLabel(*a.Activity, g.Icons, g.Ellipsis)
		c.line(cx, y+2, agentContent, g.Ellipsis,
			fg(p.spinner(), color), plain(" "+icon+" "+fitOwn(text, agentContent-4, g.Ellipsis)))
	}

	desc := "no description"
	if a.Description != "" {
		desc = fitOwn(model.Clip(a.Description, model.MaxText, g.Ellipsis), agentContent, g.Ellipsis)
	}
	c.line(cx, y+3, agentContent, g.Ellipsis, fg(desc, t.Muted))
}

func padStart(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}
