package model

import (
	"slices"

	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// The tree: each session is a cluster, with the session box on top, a vertical stem and, below,
// the active agents side by side joined by a horizontal bus. When the agents do not fit in one
// row, a vertical trunk runs down the left of the agents and joins the bus of each next row
// without crossing any box. Clusters sit side by side and wrap by the terminal width.
//
// Coordinates: x in columns and y in rows, from the corner of the body (the area above the
// status bar). Inside a cluster, positions are relative to its corner.
const (
	// SessionWidth is the session box (with the border).
	SessionWidth = 34
	// SessionHeight is the session box with a single profile: the title bar, 5 content lines and
	// the bottom border.
	SessionHeight = 7
	// SessionHeightMulti has one more line, the centered profile label under the title.
	SessionHeightMulti = SessionHeight + 1
	// AgentWidth is the compact agent box (with the border).
	AgentWidth = 26
	// AgentHeight is 3 content lines and the border.
	AgentHeight = 5
	// AgentGap is the columns between neighbor agents.
	AgentGap = 2
	// ClusterGap is the columns between neighbor clusters.
	ClusterGap = 4
	// RowGap is the rows between rows of clusters.
	RowGap = 1
	// TrunkWidth is the columns of the trunk at the left of the agents (the trunk and a space)
	// when there are several rows.
	TrunkWidth = 2

	// MinTermColumns and MinTermRows are the smallest terminal: below it only a notice shows. At
	// the minimum width a session box still fits with the agents stacked below.
	MinTermColumns = 40
	MinTermRows    = 14

	// UnlimitedRows as bodyRows means no limit.
	UnlimitedRows = -1
)

// SessionBoxHeight is the height of every session box in a run: it depends only on how many
// profiles are watched (fixed while the viewer runs), never on the session, so the layout stays
// a grid.
func SessionBoxHeight(multiProfile bool) int {
	if multiProfile {
		return SessionHeightMulti
	}
	return SessionHeight
}

// TreeSession is a session to lay out.
type TreeSession struct {
	Key string
	// AgentKeys are the active agents, in the displayed order.
	AgentKeys []string
}

// Connector is a line of connectors: Text starts at column Left of the cluster, on row Y.
type Connector struct {
	Y, Left int
	Text    string
}

// AgentLine is a row of agents of a cluster and the bus right above it.
type AgentLine struct {
	// Y is the row (relative to the cluster) of the top of the agent boxes.
	Y int
	// Left is the column (relative to the cluster) of the first agent; the next ones come every
	// AgentWidth + AgentGap.
	Left      int
	Keys      []string
	Connector Connector
	// Trunk is the column (relative to the cluster) of the trunk that runs beside the agents of
	// this row, down to the next; valid only with HasTrunk (not the last row, nor with one row).
	Trunk    int
	HasTrunk bool
}

// ClusterLayout is a session box with its agents.
type ClusterLayout struct {
	Key string
	// X is the absolute column of the left corner; Y the absolute row of the top (that of its
	// row of clusters).
	X, Y          int
	Width, Height int
	// SessionLeft is the column (relative) of the session box.
	SessionLeft int
	// Anchor is the column (relative) of the center of the session box, where the stem leaves.
	Anchor int
	// Stem is the line under the session; nil without active agents.
	Stem  *Connector
	Lines []AgentLine
}

// ClusterRow is a row of clusters.
type ClusterRow struct {
	Y int
	// Left is the column of the first cluster (the row is centered).
	Left, Width, Height int
	Clusters            []ClusterLayout
}

// TreeLayout is the placement of every cluster.
type TreeLayout struct {
	Columns int
	// SessionHeight is the height of the session boxes (see SessionBoxHeight).
	SessionHeight int
	Rows          []ClusterRow
	// ContentHeight is the height of all rows of clusters, with the gaps between them.
	ContentHeight int
}

// lineChar is the line character for the links present at a point.
func lineChar(up, down, left, right bool, line style.Lines) string {
	if !left && !right {
		return line.Vertical
	}
	if !up && !down {
		return line.Horizontal
	}
	if left && right {
		switch {
		case up && down:
			return line.Cross
		case up:
			return line.HorizontalUp
		}
		return line.HorizontalDown
	}
	if up && down {
		if right {
			return line.VerticalRight
		}
		return line.VerticalLeft
	}
	if right {
		if down {
			return line.DownRight
		}
		return line.UpRight
	}
	if down {
		return line.DownLeft
	}
	return line.UpLeft
}

// ConnectorRow draws the bus between the columns ups (links up) and downs (links down). It
// returns the text from the smallest column and that column; e.g. up 25, downs 12 and 38 gives
// `┌──…┴…──┐` at 12.
func ConnectorRow(ups, downs []int, line style.Lines) (left int, text string) {
	all := append(slices.Clone(ups), downs...)
	if len(all) == 0 {
		return 0, ""
	}
	lo, hi := slices.Min(all), slices.Max(all)
	var b []byte
	for x := lo; x <= hi; x++ {
		up, down := slices.Contains(ups, x), slices.Contains(downs, x)
		if up || down {
			b = append(b, lineChar(up, down, x > lo, x < hi, line)...)
		} else {
			b = append(b, line.Horizontal...)
		}
	}
	return lo, string(b)
}

// AgentsPerLine is how many agents fit in maxWidth columns (at least 1).
func AgentsPerLine(maxWidth int) int {
	return max(1, floorDiv(maxWidth+AgentGap, AgentWidth+AgentGap))
}

// floorDiv is the integer division rounded toward negative infinity.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// AgentLineWidth is the width of a row with n agents.
func AgentLineWidth(n int) int {
	if n <= 0 {
		return 0
	}
	return n*AgentWidth + (n-1)*AgentGap
}

// center is the horizontal center of a box of the given width that starts at left.
func center(left, width int) int { return left + width/2 }

// NewClusterLayout lays out the cluster of a session in at most maxWidth columns. In one row the
// agents sit centered under the session and the bus joins the stem to the drops (`┌──┴──┐`). In
// several rows the bus of the first also drops through the trunk, left of the agents; the bus of
// each next row leaves the trunk (`├`/`└`), with no link to the agents above. Without X and Y,
// which TreeLayout fills.
func NewClusterLayout(s TreeSession, maxWidth, boxHeight int, line style.Lines) ClusterLayout {
	n := len(s.AgentKeys)
	multiLine := n > AgentsPerLine(maxWidth)
	perLine := max(1, n)
	if multiLine {
		perLine = AgentsPerLine(maxWidth - TrunkWidth)
	}
	firstWidth := AgentLineWidth(min(n, perLine))
	width := firstWidth
	if multiLine {
		width += TrunkWidth
	}
	width = max(SessionWidth, width)
	// In several rows the agent rows are centered in the area right of the trunk and the session
	// on them; at the minimum width the session rules and the trunk fits in the leftover.
	var anchor int
	if multiLine && width > SessionWidth {
		anchor = TrunkWidth + firstWidth/2
	} else {
		anchor = center((width-SessionWidth)/2, SessionWidth)
	}
	c := ClusterLayout{
		Key: s.Key, Width: width, Height: boxHeight,
		SessionLeft: anchor - SessionWidth/2, Anchor: anchor,
	}
	if n == 0 {
		return c
	}

	c.Stem = &Connector{Y: boxHeight, Left: anchor, Text: line.Vertical}
	var groups [][]string
	for i := 0; i < n; i += perLine {
		groups = append(groups, s.AgentKeys[i:min(i+perLine, n)])
	}
	// Each row is centered on the stem (the first, the widest, sets the trunk column).
	lefts := make([]int, len(groups))
	for i, keys := range groups {
		lefts[i] = anchor - AgentLineWidth(len(keys))/2
	}
	trunk := 0
	if multiLine {
		trunk = slices.Min(lefts) - TrunkWidth
	}

	busY := boxHeight + 1
	for i, keys := range groups {
		left := lefts[i]
		drops := make([]int, len(keys))
		for j := range keys {
			drops[j] = center(left+j*(AgentWidth+AgentGap), AgentWidth)
		}
		continues := multiLine && i < len(groups)-1
		ups := []int{anchor}
		if i > 0 && multiLine {
			ups = []int{trunk}
		}
		downs := drops
		if continues {
			downs = append([]int{trunk}, drops...)
		}
		l, text := ConnectorRow(ups, downs, line)
		al := AgentLine{
			Y: busY + 1, Left: left, Keys: keys,
			Connector: Connector{Y: busY, Left: l, Text: text},
		}
		if continues {
			al.Trunk, al.HasTrunk = trunk, true
		}
		c.Lines = append(c.Lines, al)
		busY += 1 + AgentHeight
	}
	c.Height = c.Lines[len(c.Lines)-1].Y + AgentHeight
	return c
}

// NewTreeLayout spreads the clusters in rows that fit in columns, in the given order, each row
// centered. boxHeight is the height of the session boxes (see SessionBoxHeight).
func NewTreeLayout(sessions []TreeSession, columns, boxHeight int, line style.Lines) TreeLayout {
	cols := max(1, columns)
	clusters := make([]ClusterLayout, len(sessions))
	for i, s := range sessions {
		clusters[i] = NewClusterLayout(s, cols, boxHeight, line)
	}

	var groups [][]ClusterLayout
	var current []ClusterLayout
	width := 0
	for _, c := range clusters {
		next := c.Width
		if len(current) > 0 {
			next = width + ClusterGap + c.Width
		}
		if len(current) > 0 && next > cols {
			groups = append(groups, current)
			current, width = []ClusterLayout{c}, c.Width
		} else {
			current = append(current, c)
			width = next
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}

	layout := TreeLayout{Columns: cols, SessionHeight: boxHeight}
	y := 0
	for _, group := range groups {
		rowWidth, height := 0, 0
		for i, c := range group {
			rowWidth += c.Width
			if i > 0 {
				rowWidth += ClusterGap
			}
			height = max(height, c.Height)
		}
		left := max(0, (cols-rowWidth)/2)
		x := left
		for i := range group {
			group[i].X, group[i].Y = x, y
			x += group[i].Width + ClusterGap
		}
		layout.Rows = append(layout.Rows, ClusterRow{Y: y, Left: left, Width: rowWidth, Height: height, Clusters: group})
		y += height + RowGap
	}
	if len(layout.Rows) > 0 {
		layout.ContentHeight = y - RowGap
	}
	return layout
}

// TreeViewport is the vertical window over the content.
type TreeViewport struct {
	// Top is the first visible row of the content.
	Top int
	// OffsetY is the blank rows before the content (vertical centering when it fits).
	OffsetY int
	// ViewRows is the visible rows of the content.
	ViewRows int
	// Scrolling: the content does not fit, so there is an indicator row above and another below.
	Scrolling bool
	Above     bool
	Below     bool
}

// Find returns the cluster with the given key.
func (l TreeLayout) Find(key string) (ClusterLayout, bool) {
	if key == "" {
		return ClusterLayout{}, false
	}
	for _, row := range l.Rows {
		for _, c := range row.Clusters {
			if c.Key == key {
				return c, true
			}
		}
	}
	return ClusterLayout{}, false
}

// Viewport windows the body (bodyRows rows; UnlimitedRows for no limit). When everything fits it
// centers; otherwise it keeps a row for an indicator above and another below and scrolls the
// minimum from prevTop to keep the box of the selected session visible.
func (l TreeLayout) Viewport(bodyRows int, selectedKey string, prevTop int) TreeViewport {
	total := l.ContentHeight
	if bodyRows < 0 || total <= bodyRows {
		offsetY := 0
		if bodyRows >= 0 {
			offsetY = (bodyRows - total) / 2
		}
		return TreeViewport{OffsetY: offsetY, ViewRows: total}
	}
	viewRows := max(1, bodyRows-2)
	maxTop := max(0, total-viewRows)
	top := clamp(prevTop, 0, maxTop)
	if sel, ok := l.Find(selectedKey); ok {
		start, end := sel.Y, sel.Y+l.SessionHeight
		if start < top {
			top = start
		} else if end > top+viewRows {
			top = end - viewRows
		}
	}
	top = clamp(top, 0, maxTop)
	return TreeViewport{
		Top: top, ViewRows: viewRows, Scrolling: true,
		Above: top > 0, Below: top+viewRows < total,
	}
}

// NavCell is a session in the navigation grid.
type NavCell struct {
	Key string
	// Center is the absolute column of the center of the session (to pick the nearest neighbor).
	Center int
}

// NavGrid is the rows of clusters for spatial navigation.
func (l TreeLayout) NavGrid() [][]NavCell {
	grid := make([][]NavCell, len(l.Rows))
	for i, row := range l.Rows {
		for _, c := range row.Clusters {
			grid[i] = append(grid[i], NavCell{Key: c.Key, Center: c.X + c.Anchor})
		}
	}
	return grid
}

// clamp limits n to [lo, hi].
func clamp(n, lo, hi int) int { return min(hi, max(lo, n)) }
