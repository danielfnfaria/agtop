package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const (
	// detailHeaderRows is the fixed rows of the detail header (without the panel).
	detailHeaderRows = 9
	// detailCompactHeaderRows is the compact header: title, activity/context and tabs.
	detailCompactHeaderRows = 3
	// minPanelRows: with fewer rows left for the panel, the header turns compact.
	minPanelRows = 4
	// toolIndent is the indent of the expanded tools of an agent.
	toolIndent = 7
)

// detailGeometry is the scroll of the agents panel, computed apart from the drawing so the app
// can keep the first visible agent between frames.
type detailGeometry struct {
	compact     bool
	maxToolRows int // 0: no limit
	start, end  int
	agents      []keyedAgent
}

// expandedToolRows is the rows of the expanded tools: all, or limit with the "+N" line.
func expandedToolRows(count, limit int) int {
	rows := max(1, count)
	if limit <= 0 {
		return rows
	}
	return min(rows, max(1, limit))
}

func agentRowHeight(a *store.AgentView, expanded bool, maxToolRows int) int {
	if !expanded {
		return 2
	}
	return 2 + expandedToolRows(len(a.PendingTools), maxToolRows)
}

// detailLayout scrolls the agents from prevTop to keep the selected one visible; rows < 0 means
// no limit.
func detailLayout(s *store.SessionView, nav model.NavState, rows, prevTop int) detailGeometry {
	g := detailGeometry{agents: sortedAgents(s.Agents)}
	g.compact = rows >= 0 && rows-detailHeaderRows < minPanelRows
	g.end = len(g.agents)
	if rows < 0 {
		return g
	}
	header := detailHeaderRows
	if g.compact {
		header = detailCompactHeaderRows
	}
	panelRows := max(2, rows-header)
	g.maxToolRows = max(1, panelRows-3)
	if nav.Panel == model.PanelAgents && len(g.agents) > 0 {
		extra := 0
		for _, a := range g.agents {
			if a.key == nav.ExpandedAgentKey {
				extra = agentRowHeight(a.agent, true, g.maxToolRows) - 2
			}
		}
		perPage := max(1, (panelRows-1-extra)/2)
		g.start = model.ScrollTop(prevTop, nav.AgentIndex, perPage, len(g.agents))
		g.end = min(len(g.agents), g.start+perPage)
	}
	return g
}

type detailInput struct {
	session     *store.SessionView
	profile     *config.ProfileDir
	showProfile bool
	tone        style.Color
	nav         model.NavState
	geo         detailGeometry
	rows        int // < 0: no limit
}

// drawDetail draws the detail of a session from row y; it returns the next row.
func (p painter) drawDetail(c *canvas, y int, in detailInput) int {
	g, t := p.g, p.t
	s := in.session
	tone := in.tone
	if tone == "" {
		tone = "magenta"
		if len(t.Profiles) > 0 {
			tone = t.Profiles[0]
		}
	}
	w := c.w
	p.titleLine(c, y, s, in.showProfile, tone)
	y++
	if in.geo.compact {
		p.activityContextLine(c, y, s)
		y++
	} else {
		y = p.detailHeader(c, y, s, in.profile, in.showProfile)
		y++
	}
	tools := len(s.PendingTools)
	agents := in.geo.agents
	tab := func(active bool, label string) seg {
		if active {
			return seg{" " + label + " ", sty{fg: t.Accent, bold: true, inverse: true}}
		}
		return fg(" "+label+" ", t.Muted)
	}
	c.line(0, y, w, g.Ellipsis,
		tab(in.nav.Panel == model.PanelAgents, "Agents ("+strconv.Itoa(len(agents))+")"),
		plain(" "),
		tab(in.nav.Panel == model.PanelTools, "Pending tools ("+strconv.Itoa(tools)+")"))
	y++

	muted := sty{fg: t.Muted}
	if in.nav.Panel == model.PanelAgents {
		if len(agents) == 0 {
			c.line(0, y, w, g.Ellipsis, seg{"no agents in this session", muted})
			return y + 1
		}
		for i := in.geo.start; i < in.geo.end; i++ {
			a := agents[i]
			y = p.agentRow(c, y, a.agent, i == in.nav.AgentIndex, a.key == in.nav.ExpandedAgentKey, in.geo.maxToolRows)
		}
		if in.geo.start > 0 || in.geo.end < len(agents) {
			c.line(0, y, w, g.Ellipsis, seg{"agents " + strconv.Itoa(in.geo.start+1) + "-" +
				strconv.Itoa(in.geo.end) + " of " + strconv.Itoa(len(agents)), muted})
			y++
		}
		return y
	}
	if tools == 0 {
		c.line(0, y, w, g.Ellipsis, seg{"no pending tools in this session", muted})
		return y + 1
	}
	limit := tools
	if in.rows >= 0 {
		header := detailHeaderRows
		if in.geo.compact {
			header = detailCompactHeaderRows
		}
		limit = max(1, max(2, in.rows-header)-1)
	}
	for i, tool := range s.PendingTools {
		if i >= limit {
			break
		}
		p.toolLine(c, y, tool.PendingTool, 1)
		y++
	}
	if tools > limit {
		c.line(0, y, w, g.Ellipsis, seg{"+" + strconv.Itoa(tools-limit) + " " + plural(tools-limit, "tool", "tools"), muted})
		y++
	}
	return y
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (p painter) titleLine(c *canvas, y int, s *store.SessionView, showProfile bool, tone style.Color) {
	g, t := p.g, p.t
	isStale := stale(s)
	kind := kindBadge(s)
	provider := domain.ProviderInfo(s.Provider)
	profileText, kindText := "", ""
	if showProfile {
		profileText = " [" + model.Clip(s.ProfileLabel, model.MaxText, g.Ellipsis) + "]"
	}
	if kind != "" {
		kindText = " [" + model.Clip(kind, model.MaxText, g.Ellipsis) + "]"
	}
	title := fitOwn(model.Clip(s.DisplayTitle, model.MaxText, g.Ellipsis),
		c.w-2-ansi.StringWidth(profileText)-ansi.StringWidth(kindText), g.Ellipsis)
	titleSt := sty{bold: true}
	if isStale {
		titleSt.fg = t.Muted
	}
	segs := []seg{
		{g.ProviderMark(provider.Symbol), sty{fg: provider.Color, dim: isStale}},
		plain(" "),
		{title, titleSt},
	}
	if showProfile {
		col := tone
		if isStale {
			col = t.Muted
		}
		segs = append(segs, fg(profileText, col))
	}
	if kind != "" {
		segs = append(segs, fg(kindText, t.Muted))
	}
	c.line(0, y, c.w, g.Ellipsis, segs...)
}

func (p painter) activityContextLine(c *canvas, y int, s *store.SessionView) {
	g, t := p.g, p.t
	ctx := model.NewContextLabel(s.ContextUsed, s.ContextPercent, s.ContextWindow, 20, g, t)
	var segs []seg
	if stale(s) {
		segs = append(segs, fg(g.Dot+" "+p.ago(s.StaleSince, "ended ", " ago", "ended"), t.Muted))
	} else {
		segs = append(segs,
			fg(g.Dot, style.ActivityColor(style.Activity(s.Activity), t)),
			plain(" "+style.ActivityText(style.Activity(s.Activity), s.RawStatus)))
	}
	segs = append(segs, plain(" "+g.Middot+" "), fg("context ", t.Muted), fg(ctx.Bar, ctx.Color),
		plain(" "+ctx.Pct+" "+ctx.Numbers))
	c.line(0, y, c.w, g.Ellipsis, segs...)
}

// detailHeader draws the rows after the title (cwd ... last activity) and returns the next row.
func (p painter) detailHeader(c *canvas, y int, s *store.SessionView, profile *config.ProfileDir, showProfile bool) int {
	g, t := p.g, p.t
	w := c.w
	sep := plain(" " + g.Middot + " ")
	label := func(text string) seg { return fg(text+" ", t.Muted) }
	clip := func(v string) string { return model.Clip(v, model.MaxText, g.Ellipsis) }
	orDash := func(v string) string {
		if v == "" {
			return g.Dash
		}
		return v
	}

	c.lineMiddle(0, y, w, g.Ellipsis, label("cwd"), plain(fitOwn(clip(orDash(s.Cwd)), w-4, g.Ellipsis)))
	y++

	modelText := modelLabel(s, g.Dash)
	if s.Effort != "" {
		modelText += " (" + s.Effort + ")"
	}
	c.line(0, y, w, g.Ellipsis,
		label("branch"), plain(clip(orDash(s.GitBranch))), sep,
		label("model"), plain(clip(modelText)), sep,
		label("version"), plain(clip(orDash(s.Version))), sep,
		label("pid"), plain(strconv.Itoa(s.PID)))
	y++

	provider := domain.ProviderInfo(s.Provider)
	dir := g.Dash
	if profile != nil {
		dir = profile.Dir
	}
	segs := []seg{label("provider"), fg(g.ProviderMark(provider.Symbol), provider.Color),
		plain(" " + provider.DisplayName), sep}
	if showProfile {
		segs = append(segs, label("profile"), plain(clip(s.ProfileLabel)+" ("+clip(dir)+")"))
	} else {
		segs = append(segs, label("config"), plain(clip(dir)))
	}
	c.lineMiddle(0, y, w, g.Ellipsis, segs...)
	y++

	p.activityContextLine(c, y, s)
	y++

	tot := s.Totals
	m := g.Middot
	c.line(0, y, w, g.Ellipsis, label("tokens"), plain("input "+model.FmtTokens(tot.Input)+" "+m+
		" output "+model.FmtTokens(tot.Output)+" "+m+" cache read "+model.FmtTokens(tot.CacheRead)+
		" "+m+" cache create "+model.FmtTokens(tot.CacheCreate)))
	y++

	started := g.Dash
	if s.StartedAt != 0 {
		started = model.FmtClock(s.StartedAt, p.loc)
	}
	last := p.ago(s.LastActivityAt, "", " ago", "no activity")
	if s.LastActivityAt != 0 && p.layout.Time != "absolute" {
		last += " (" + model.FmtClock(s.LastActivityAt, p.loc) + ")"
	}
	c.line(0, y, w, g.Ellipsis, label("started"), plain(started), sep, label("last activity"), plain(last))
	return y + 1
}

func (p painter) toolLine(c *canvas, y int, tool domain.PendingTool, indent int) {
	g := p.g
	segs := []seg{plain(strings.Repeat(" ", indent)), fg(model.Clip(tool.Name, model.MaxText, g.Ellipsis), p.t.Accent)}
	if tool.Target != "" {
		segs = append(segs, plain(" "+model.Clip(tool.Target, model.MaxText, g.Ellipsis)))
	}
	c.line(0, y, c.w, g.Ellipsis, segs...)
}

// agentRow draws an agent of the detail (two rows, plus its tools when expanded).
func (p painter) agentRow(c *canvas, y int, a *store.AgentView, selected, expanded bool, maxToolRows int) int {
	g, t := p.g, p.t
	w := c.w
	ell := g.Ellipsis
	color := style.StatusColor(a.Status, t)
	sep := " " + g.Middot + " "
	ctxPct := model.NewContextLabel(a.Context.Used, a.Context.Percent, a.Context.ContextWindow, 10, g, t).Pct
	status := g.Dot + " " + fitOwn(model.StatusLabel(a), w-6, ell)
	room := w - (3 + ansi.StringWidth(status) + 1)
	typ := fitOwn(model.Clip(a.Type, model.MaxText, ell), room, ell)
	descRoom := room - ansi.StringWidth(typ) - 2
	desc := ""
	if a.Description != "" && descRoom > 0 {
		desc = "  " + fitOwn(model.Clip(a.Description, model.MaxText, ell), descRoom, ell)
	}
	short := model.ShortModel(a.Model, ell)
	if short == "" {
		short = g.Dash
	}
	bgfg := "fg"
	if a.IsBackground {
		bgfg = "bg"
	}
	tail := ""
	if a.Activity != nil {
		icon, text := model.ActivityLabel(*a.Activity, g.Icons, ell)
		tail = sep + icon + " " + text
	}
	ctxText := "ctx " + model.FmtTokens(a.Context.Used) + " (" + ctxPct + ")" + sep + "out " + model.FmtTokens(a.Totals.Output)
	head := "     " + short + sep + bgfg + sep + "000000" + sep + ctxText
	tailText := ""
	if tail != "" {
		if tailRoom := w - ansi.StringWidth(head); tailRoom > 0 {
			tailText = fitOwn(tail, tailRoom, ell)
		}
	}

	pointer := " "
	if selected {
		pointer = g.Pointer
	}
	mark := " "
	switch {
	case expanded:
		mark = g.Expanded
	case len(a.PendingTools) > 0:
		mark = g.Collapsed
	}
	c.line(0, y, w, ell, fg(pointer, t.Accent), plain(mark+" "), fg(status, color), plain(" "),
		seg{typ, sty{bold: selected}}, plain(desc))
	y++
	c.line(0, y, w, ell, fg("     "+short+sep+bgfg+sep+p.duration(a)+sep+ctxText+tailText, t.Muted))
	y++
	if !expanded {
		return y
	}
	pad := strings.Repeat(" ", toolIndent)
	if len(a.PendingTools) == 0 {
		c.line(0, y, w, ell, fg(pad+"no pending tools", t.Muted))
		return y + 1
	}
	rows := expandedToolRows(len(a.PendingTools), maxToolRows)
	shown := a.PendingTools
	if rows < len(shown) {
		shown = shown[:max(0, rows-1)]
	}
	for _, tool := range shown {
		p.toolLine(c, y, tool.PendingTool, toolIndent)
		y++
	}
	if hidden := len(a.PendingTools) - len(shown); hidden > 0 {
		c.line(0, y, w, ell, fg(pad+"+"+strconv.Itoa(hidden)+" "+plural(hidden, "tool", "tools"), t.Muted))
		y++
	}
	return y
}
