package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// sty is the look of a run of text; the zero value is the terminal default.
type sty struct {
	fg      style.Color
	bold    bool
	dim     bool
	inverse bool
	link    string
}

// seg is a piece of text with one look.
type seg struct {
	text string
	st   sty
}

func plain(text string) seg             { return seg{text: text} }
func fg(text string, c style.Color) seg { return seg{text: text, st: sty{fg: c}} }

// cell is one terminal column: a grapheme (w columns wide) or the continuation of a wide one.
type cell struct {
	g    string
	w    int
	st   sty
	cont bool
}

// canvas is a grid of cells the views draw on; text outside it (or outside the clip rows) is
// dropped, like an overflow-hidden box.
type canvas struct {
	w, h           int
	rows           [][]cell
	clipY0, clipY1 int
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: max(0, w), h: max(0, h)}
	c.rows = make([][]cell, c.h)
	for i := range c.rows {
		c.rows[i] = make([]cell, c.w)
	}
	c.clipY0, c.clipY1 = 0, c.h
	return c
}

// clip limits drawing to rows [y0, y1) until unclip.
func (c *canvas) clip(y0, y1 int) { c.clipY0, c.clipY1 = max(0, y0), min(c.h, y1) }
func (c *canvas) unclip()         { c.clipY0, c.clipY1 = 0, c.h }

// cellsOf splits segments into cells.
func cellsOf(segs []seg) []cell {
	var out []cell
	for _, s := range segs {
		g := uniseg.NewGraphemes(s.text)
		for g.Next() {
			str := g.Str()
			w := ansi.StringWidth(str)
			if w == 0 {
				continue
			}
			out = append(out, cell{g: str, w: w, st: s.st})
		}
	}
	return out
}

func cellsWidth(cells []cell) int {
	w := 0
	for _, c := range cells {
		w += c.w
	}
	return w
}

// headCells keeps the leading cells that fit in width columns.
func headCells(cells []cell, width int) []cell {
	used := 0
	for i, c := range cells {
		if used+c.w > width {
			return cells[:i]
		}
		used += c.w
	}
	return cells
}

// tailCells keeps the trailing cells that fit in width columns.
func tailCells(cells []cell, width int) []cell {
	used := 0
	for i, cl := range slices.Backward(cells) {
		if used+cl.w > width {
			return cells[i+1:]
		}
		used += cl.w
	}
	return cells
}

type truncMode int

const (
	truncEnd truncMode = iota
	truncMiddle
)

// fitCells cuts cells to width columns with an (unstyled) ellipsis, at the end or the middle.
func fitCells(cells []cell, width int, mode truncMode, ellipsis string) []cell {
	if width <= 0 {
		return nil
	}
	if cellsWidth(cells) <= width {
		return cells
	}
	ell := cellsOf([]seg{plain(ellipsis)})
	ew := cellsWidth(ell)
	if width <= ew {
		return headCells(ell, width)
	}
	if mode == truncMiddle {
		half := width / 2
		head := headCells(cells, half)
		tail := tailCells(cells, width-half-ew)
		out := append(append(append([]cell{}, head...), ell...), tail...)
		return out
	}
	return append(append([]cell{}, headCells(cells, width-ew)...), ell...)
}

// put writes cells from column x on row y and returns the column after them.
func (c *canvas) put(x, y int, cells []cell) int {
	if y < c.clipY0 || y >= c.clipY1 {
		return x + cellsWidth(cells)
	}
	row := c.rows[y]
	for _, cl := range cells {
		if x >= 0 && x+cl.w <= c.w {
			// A wide glyph partly overwritten loses its other half.
			if row[x].cont && x > 0 {
				row[x-1] = cell{}
			}
			row[x] = cl
			for k := 1; k < cl.w; k++ {
				row[x+k] = cell{cont: true, st: cl.st}
			}
			if end := x + cl.w; end < c.w && row[end].cont {
				row[end] = cell{}
			}
		}
		x += cl.w
	}
	return x
}

// text writes the segments as is (clipped only by the canvas).
func (c *canvas) text(x, y int, segs ...seg) int { return c.put(x, y, cellsOf(segs)) }

// line writes the segments cut to width columns (Ink's truncate-end).
func (c *canvas) line(x, y, width int, ellipsis string, segs ...seg) int {
	return c.put(x, y, fitCells(cellsOf(segs), width, truncEnd, ellipsis))
}

// lineMiddle writes the segments cut in the middle to width columns (Ink's truncate-middle).
func (c *canvas) lineMiddle(x, y, width int, ellipsis string, segs ...seg) int {
	return c.put(x, y, fitCells(cellsOf(segs), width, truncMiddle, ellipsis))
}

// blit copies rows [from, from+n) of src to this canvas from row y.
func (c *canvas) blit(src *canvas, from, n, y int) {
	for i := range n {
		sy, dy := from+i, y+i
		if sy < 0 || sy >= src.h || dy < c.clipY0 || dy >= c.clipY1 {
			continue
		}
		copy(c.rows[dy], src.rows[sy])
	}
}

// renderer turns a canvas into a string, with or without SGR styles and hyperlinks.
type renderer struct {
	style StyleMode
	links bool
}

func (r renderer) render(c *canvas) string {
	var b strings.Builder
	for y, row := range c.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		end := len(row)
		for end > 0 && blank(row[end-1]) {
			end--
		}
		var run strings.Builder
		var cur sty
		flush := func() {
			if run.Len() > 0 {
				b.WriteString(r.styled(run.String(), cur))
				run.Reset()
			}
		}
		for x := range end {
			cl := row[x]
			if cl.cont {
				continue
			}
			st := cl.st
			if cl.g == "" {
				st = sty{}
			}
			if st != cur {
				flush()
				cur = st
			}
			if cl.g == "" {
				run.WriteByte(' ')
			} else {
				run.WriteString(cl.g)
			}
		}
		flush()
	}
	return b.String()
}

func blank(c cell) bool {
	return !c.cont && (c.g == "" || (c.g == " " && !c.st.inverse && c.st.link == ""))
}

// styled wraps text in SGR like chalk does: with StyleAttributes only the fg is dropped (bold,
// dim and inverse stay, so the selection and stale boxes remain visible), with StyleNone there
// is no SGR, and each attribute is closed by its own reset.
func (r renderer) styled(text string, st sty) string {
	var open, closes []string
	if r.style == StyleNone {
		if r.links && st.link != "" {
			return osc8Open(st.link) + text + osc8Close
		}
		return text
	}
	if st.bold {
		open = append(open, "1")
	}
	if st.dim {
		open = append(open, "2")
	}
	if st.inverse {
		open, closes = append(open, "7"), append(closes, "27")
	}
	if st.bold || st.dim {
		closes = append(closes, "22")
	}
	if r.style == StyleFull {
		if code := fgCode(st.fg); code != "" {
			open, closes = append(open, code), append([]string{"39"}, closes...)
		}
	}
	out := text
	if len(open) > 0 {
		var b strings.Builder
		b.WriteString("\x1b[" + strings.Join(open, ";") + "m" + text)
		for _, c := range closes {
			b.WriteString("\x1b[" + c + "m")
		}
		out = b.String()
	}
	if r.links && st.link != "" {
		out = osc8Open(st.link) + out + osc8Close
	}
	return out
}

const osc8Close = "\x1b]8;;\x07"

func osc8Open(url string) string { return "\x1b]8;;" + url + "\x07" }

// fgCode is the SGR foreground parameter of a theme color (tone name or hex); "" for none.
func fgCode(c style.Color) string {
	if c == "" {
		return ""
	}
	if i, ok := style.ANSIIndex(c); ok {
		if i < 8 {
			return strconv.Itoa(30 + i)
		}
		return strconv.Itoa(90 + i - 8)
	}
	if hex, ok := style.ExpandHex(c); ok {
		v, err := strconv.ParseUint(hex[1:], 16, 32)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("38;2;%d;%d;%d", v>>16, v>>8&0xff, v&0xff)
	}
	return ""
}
