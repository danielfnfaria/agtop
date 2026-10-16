package ui

import (
	"strconv"
	"strings"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const (
	profileKey = "p"
	// helpMaxErrors is how many recent errors the help shows.
	helpMaxErrors = 5
	helpKeyWidth  = 18
)

var helpKeys = [][2]string{
	{"arrows / h j k l", "move between sessions (tree) or agents (detail)"},
	{"Enter", "open the session; in the detail, expand the agent"},
	{"x", "close the selected session in the tree (SIGTERM, after a confirmation)"},
	{"Esc / Backspace", "back to the tree (or close this help)"},
	{"Tab", "switch the detail panel (agents / pending tools)"},
	{profileKey, "filter by profile (all, then each label)"},
	{"?", "show or hide this help"},
	{"Ctrl-L", "redraw the whole screen (if something outside garbled it)"},
	{"q / Ctrl-C", "quit"},
}

// helpLine draws one line of the help at (x, y) in width columns.
type helpLine func(c *canvas, x, y, width int)

// drawHelp draws the key help and the recent store errors in a box from row y, cut to rows
// (< 0: no limit); it returns the next row.
func (p painter) drawHelp(c *canvas, y, rows int, errors []string, profiles []config.ProfileDir, tones map[string]style.Color) int {
	g, t := p.g, p.t
	ell := g.Ellipsis
	clip := func(s string) string { return model.Clip(s, model.MaxText, ell) }
	multi := len(profiles) > 1
	recent := errors
	if len(recent) > helpMaxErrors {
		recent = recent[len(recent)-helpMaxErrors:]
	}
	muted := sty{fg: t.Muted}

	lines := []helpLine{func(c *canvas, x, y, w int) { c.line(x, y, w, ell, seg{"Help", sty{bold: true}}) }}
	for _, k := range helpKeys {
		if !multi && k[0] == profileKey {
			continue
		}
		k := k
		lines = append(lines, func(c *canvas, x, y, w int) {
			c.line(x, y, min(w, helpKeyWidth), ell, fg(k[0], t.Accent))
			c.line(x+helpKeyWidth, y, w-helpKeyWidth, ell, plain(k[1]))
		})
	}
	for _, text := range []string{
		"The screen shows only metadata (titles, models, counts, tool names);",
		"never prompts, commands or outputs.",
	} {
		text := text
		lines = append(lines, func(c *canvas, x, y, w int) { c.line(x, y, w, ell, seg{text, muted}) })
	}
	for _, pr := range profiles {
		pr := pr
		lines = append(lines, func(c *canvas, x, y, w int) {
			if !multi {
				c.lineMiddle(x, y, w, ell, seg{"config " + clip(pr.Dir), muted})
				return
			}
			tone, ok := tones[pr.Label]
			if !ok && len(t.Profiles) > 0 {
				tone = t.Profiles[0]
			}
			c.lineMiddle(x, y, w, ell, seg{"profile ", muted}, fg(clip(pr.Label), tone),
				seg{" (" + clip(pr.Dir) + ")", muted})
		})
	}
	if len(errors) == 0 {
		lines = append(lines, func(c *canvas, x, y, w int) { c.line(x, y, w, ell, seg{"Recent errors: none", muted}) })
	} else {
		head := "Recent errors ("
		if len(recent) < len(errors) {
			head += "last " + strconv.Itoa(len(recent)) + " of "
		}
		head += strconv.Itoa(len(errors)) + ")"
		lines = append(lines, func(c *canvas, x, y, w int) {
			c.line(x, y, w, ell, seg{head, sty{fg: t.Failed, bold: true}})
		})
		for _, e := range recent {
			e := e
			lines = append(lines, func(c *canvas, x, y, w int) {
				c.lineMiddle(x, y, w, ell, fg(g.Middot+" "+clip(e), t.Failed))
			})
		}
	}

	shown, hidden := lines, 0
	if rows >= 0 {
		avail := max(1, rows-2)
		if len(lines) > avail {
			shown = lines[:max(0, avail-1)]
			hidden = len(lines) - len(shown)
		}
	}
	inner := len(shown)
	if hidden > 0 {
		inner++
	}
	b := g.BorderRound
	st := sty{fg: t.Accent}
	w := c.w
	c.text(0, y, seg{b.TopLeft + strings.Repeat(b.Top, max(0, w-2)) + b.TopRight, st})
	p.border(c, 0, y, w, inner+2, b, st)
	cw := max(0, w-4)
	for i, l := range shown {
		l(c, 2, y+1+i, cw)
	}
	if hidden > 0 {
		c.line(2, y+1+len(shown), cw, ell, seg{"+" + strconv.Itoa(hidden) + " " + plural(hidden, "line", "lines") +
			" (enlarge the terminal)", muted})
	}
	return y + inner + 2
}
