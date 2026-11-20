package model

import (
	"slices"

	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// The tree: each session is a cluster, with the session box on top, a vertical stem and, below,
// the active agents. Every agent sits centered over the agents it launched: a subtree is as wide
// as its children need, the children of a box hang side by side under it and a bus joins the
// stem of the parent to the stem of each child. The size of the agent boxes is a step per level
// of the tree (see PickStepPlan): the levels nearer the session may take a larger step than the
// deeper ones, never the other way round. A block of children that does not fit the width wraps
// into rows, each one still centered on the stem of its parent, with a vertical trunk at their
// left that joins the bus of every row to the bus that hangs from the parent, crossing no box.
// Clusters sit side by side and wrap by the terminal width. The step is the largest one the tree
// fits in both dimensions: the width of a cluster picks it (see PickStepPlan) and, when the whole
// content is still taller than the body, every session drops a step together (see NewTreeLayout).
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
	// AgentWidth is the agent box of the largest step (with the border).
	AgentWidth = 26
	// AgentHeight is 3 content lines and the border.
	AgentHeight = 5
	// AgentGap is the columns between neighbor agents.
	AgentGap = 2
	// ClusterGap is the columns between neighbor clusters.
	ClusterGap = 4
	// RowGap is the rows between rows of clusters.
	RowGap = 1
	// TrunkWidth is the columns of the trunk at the left of a block of agents (the trunk and a
	// space) when it wraps into several rows.
	TrunkWidth = 2

	// MinTermColumns and MinTermRows are the smallest terminal: below it only a notice shows. At
	// the minimum width a session box still fits with the agents stacked below.
	MinTermColumns = 40
	MinTermRows    = 14

	// UnlimitedRows as bodyRows means no limit.
	UnlimitedRows = -1
)

// AgentStep is how much of an agent box is drawn. The steps go from the full box down to the
// smallest one; every level of a cluster takes the largest step its tree fits in (see
// PickStepPlan).
type AgentStep int

const (
	// StepFull is the full box: kind and model with the duration, the activity and the description.
	StepFull AgentStep = iota
	// StepNoModel drops the model from the first line and clips the description.
	StepNoModel
	// StepCompact keeps the kind with the duration and the activity, with no description.
	StepCompact
	// StepTiny keeps the kind and, under it, the spinner with the duration.
	StepTiny
)

// AgentSize is the size of an agent box, border included.
type AgentSize struct {
	Width, Height int
}

// agentSizes is the size of each step, from the largest to the smallest.
var agentSizes = [...]AgentSize{
	StepFull:    {Width: AgentWidth, Height: AgentHeight},
	StepNoModel: {Width: 20, Height: 5},
	StepCompact: {Width: 14, Height: 4},
	StepTiny:    {Width: 13, Height: 3},
}

// SmallestStep is the last step, the one that is used when no other fits.
const SmallestStep = AgentStep(len(agentSizes) - 1)

// Size is the box of the step (the full one for a step out of range).
func (s AgentStep) Size() AgentSize {
	if s < 0 || s > SmallestStep {
		return agentSizes[StepFull]
	}
	return agentSizes[s]
}

// StepPlan is the step of the agent boxes by level of the tree: the first entry is the level that
// hangs from the session and the last one holds for everything deeper. It never grows with the
// depth and never skips a step between neighbor levels (see PickStepPlan).
type StepPlan []AgentStep

// At is the step of the given level (the last one of the plan from there on).
func (p StepPlan) At(depth int) AgentStep {
	if len(p) == 0 {
		return StepFull
	}
	return p[clamp(depth, 0, len(p)-1)]
}

// Size is the box of the given level.
func (p StepPlan) Size(depth int) AgentSize { return p.At(depth).Size() }

// SessionBoxHeight is the height of every session box in a run: it depends only on how many
// profiles are watched (fixed while the viewer runs), never on the session, so the layout stays
// a grid.
func SessionBoxHeight(multiProfile bool) int {
	if multiProfile {
		return SessionHeightMulti
	}
	return SessionHeight
}

// TreeAgent is an agent to lay out with the agents it launched, in the displayed order.
type TreeAgent struct {
	Key      string
	Children []TreeAgent
}

// TreeSession is a session to lay out.
type TreeSession struct {
	Key string
	// Agents are the first-level active agents, in the displayed order; each carries its own.
	Agents []TreeAgent
}

// Connector is a line of connectors: Text starts at column Left of the cluster, on row Y.
type Connector struct {
	Y, Left int
	Text    string
}

// AgentBox is a placed agent box: its left corner inside the cluster and how far it is from the
// first level (0 for an agent that hangs from the session).
type AgentBox struct {
	Key   string
	X, Y  int
	Depth int
	// Step is the step of its level (see StepPlan).
	Step AgentStep
}

// Size is the box of the agent.
func (b AgentBox) Size() AgentSize { return b.Step.Size() }

// Trunk is a vertical run of line at column X, from row Y, Height rows tall.
type Trunk struct {
	X, Y, Height int
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
	Stem   *Connector
	Agents []AgentBox
	// Buses are the horizontal lines that join a parent to its children, one per row of children.
	Buses []Connector
	// Trunks are the vertical lines that join the buses of the rows a block wrapped into.
	Trunks []Trunk
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

// subtreeWidth is the columns the agent, at the given level, takes with the agents it launched,
// nothing wrapped.
func subtreeWidth(a TreeAgent, plan StepPlan, depth int) int {
	return max(plan.Size(depth).Width, forestWidth(a.Children, plan, depth+1))
}

// forestWidth is the columns the agents of a level take side by side, nothing wrapped (0 without
// agents).
func forestWidth(agents []TreeAgent, plan StepPlan, depth int) int {
	total := 0
	for i, a := range agents {
		if i > 0 {
			total += AgentGap
		}
		total += subtreeWidth(a, plan, depth)
	}
	return total
}

// MaxBlockRows is how many rows a block of siblings may wrap into for a step to be worth taking:
// two rows of readable boxes tell more than a single row of the smallest ones.
const MaxBlockRows = 2

// ForestDepth is how many levels the forest has (0 without agents).
func ForestDepth(agents []TreeAgent) int {
	depth := 0
	for _, a := range agents {
		depth = max(depth, 1+ForestDepth(a.Children))
	}
	return depth
}

// blockRows is the most rows a block of siblings of the measured tree wrapped into.
func blockRows(rows [][]sized) int {
	most := len(rows)
	for _, row := range rows {
		for _, s := range row {
			if len(s.rows) > 0 {
				most = max(most, blockRows(s.rows))
			}
		}
	}
	return most
}

// planRows is the most rows a block of siblings of the tree wraps into with the boxes of the
// plan, and whether the cluster still holds maxWidth columns.
func planRows(s TreeSession, plan StepPlan, maxWidth int) (rows int, fits bool) {
	blocks, wrapped, rowsWidth := childRows(s.Agents, plan, 0, maxWidth)
	if len(blocks) == 0 {
		return 0, true
	}
	return blockRows(blocks), trunkRoom(wrapped)+rowsWidth <= maxWidth
}

// fitsRows reports whether the tree of the session, with the boxes of the plan, holds maxWidth
// columns with no block of siblings wrapping into more than maxRows rows.
func fitsRows(s TreeSession, plan StepPlan, maxWidth, maxRows int) bool {
	rows, fits := planRows(s, plan, maxWidth)
	return fits && rows <= maxRows
}

// PickStepPlan is the step of every level of a session. It starts from the largest step the whole
// tree fits maxWidth columns in with no block of siblings over MaxBlockRows rows (the smallest
// step when none of them does, and then the blocks wrap into as many rows as they need). From
// there every level, from the first one down, takes one step more as long as the whole cluster
// still holds the width within MaxBlockRows rows: the agents nearer the session are the ones
// worth reading, so they end up no smaller than the deeper ones and at most one step apart.
func PickStepPlan(s TreeSession, maxWidth int) StepPlan {
	levels := ForestDepth(s.Agents)
	if levels == 0 {
		return StepPlan{StepFull}
	}
	base := SmallestStep
	for step := StepFull; step < SmallestStep; step++ {
		if fitsRows(s, StepPlan{step}, maxWidth, MaxBlockRows) {
			base = step
			break
		}
	}
	plan := make(StepPlan, levels)
	for i := range plan {
		plan[i] = base
	}
	for d := range plan {
		if plan[d] == StepFull || (d > 0 && plan[d]-1 < plan[d-1]) {
			continue
		}
		promoted := slices.Clone(plan)
		promoted[d]--
		if fitsRows(s, promoted, maxWidth, MaxBlockRows) {
			plan = promoted
		}
	}
	return plan
}

// center is the horizontal center of a box of the given width that starts at left.
func center(left, width int) int { return left + width/2 }

// sized is a measured subtree: how much room it takes and the rows its children wrap into.
type sized struct {
	agent TreeAgent
	// step and box are the step of its level and the box it draws.
	step AgentStep
	box  AgentSize
	// width and height are the room the whole subtree takes.
	width, height int
	// rows are the children, in the rows they wrap into (nil without children).
	rows [][]sized
	// wrapped tells the rows need a trunk at their left to join them.
	wrapped bool
	// rowsWidth is the widest row of children (the trunk not counted).
	rowsWidth int
}

// rowWidth is the columns a row of subtrees takes, with the gaps between them.
func rowWidth(row []sized) int {
	total := 0
	for i, s := range row {
		if i > 0 {
			total += AgentGap
		}
		total += s.width
	}
	return total
}

// rowHeight is the rows the tallest subtree of the row takes.
func rowHeight(row []sized) int {
	height := 0
	for _, s := range row {
		height = max(height, s.height)
	}
	return height
}

// measure sizes the subtree of a, which sits on the given level, with its children in at most
// budget columns.
func measure(a TreeAgent, plan StepPlan, depth, budget int) sized {
	box := plan.Size(depth)
	s := sized{agent: a, step: plan.At(depth), box: box, width: box.Width, height: box.Height}
	s.rows, s.wrapped, s.rowsWidth = childRows(a.Children, plan, depth+1, budget)
	if len(s.rows) == 0 {
		return s
	}
	s.width = max(box.Width, trunkRoom(s.wrapped)+s.rowsWidth)
	for _, row := range s.rows {
		s.height += 1 + rowHeight(row)
	}
	return s
}

func measureAll(agents []TreeAgent, plan StepPlan, depth, budget int) []sized {
	out := make([]sized, len(agents))
	for i, a := range agents {
		out[i] = measure(a, plan, depth, budget)
	}
	return out
}

// trunkRoom is the columns a block of agents keeps at its left for the trunk.
func trunkRoom(wrapped bool) int {
	if wrapped {
		return TrunkWidth
	}
	return 0
}

// childRows measures children and splits them into the rows they wrap into: one row when they
// fit in budget columns, else as many as needed in the budget left of the trunk.
func childRows(children []TreeAgent, plan StepPlan, depth, budget int) (rows [][]sized, wrapped bool, rowsWidth int) {
	if len(children) == 0 {
		return nil, false, 0
	}
	if one := measureAll(children, plan, depth, budget); rowWidth(one) <= budget {
		return [][]sized{one}, false, rowWidth(one)
	}
	room := max(plan.Size(depth).Width, budget-TrunkWidth)
	rows = packRows(measureAll(children, plan, depth, room), room)
	for _, row := range rows {
		rowsWidth = max(rowsWidth, rowWidth(row))
	}
	return rows, len(rows) > 1, rowsWidth
}

// packRows fills rows of at most room columns, in order (one subtree per row at the very least).
func packRows(all []sized, room int) [][]sized {
	var rows [][]sized
	var current []sized
	for _, s := range all {
		if len(current) > 0 && rowWidth(current)+AgentGap+s.width > room {
			rows = append(rows, current)
			current = nil
		}
		current = append(current, s)
	}
	if len(current) > 0 {
		rows = append(rows, current)
	}
	return rows
}

// boxAnchor is the column of the stem of a box of boxWidth columns that starts at column x of a
// subtree of width columns: centered over its children when they are wider than the box, else
// centered in the room the subtree takes.
func boxAnchor(x, width, boxWidth, trunk, rowsWidth int) int {
	if rowsWidth > 0 && trunk+rowsWidth > boxWidth {
		return x + trunk + rowsWidth/2
	}
	return center(x+(width-boxWidth)/2, boxWidth)
}

// placer fills a cluster with the boxes, buses and trunks of its agents.
type placer struct {
	line style.Lines
	c    *ClusterLayout
}

// place puts the box of s, which takes width columns from column x with its top on row y, and
// everything under it. It returns the column of its stem (the center of its box).
func (p *placer) place(s sized, depth, x, y int) int {
	trunk := trunkRoom(s.wrapped)
	anchor := boxAnchor(x, s.width, s.box.Width, trunk, s.rowsWidth)
	p.c.Agents = append(p.c.Agents,
		AgentBox{Key: s.agent.Key, X: anchor - s.box.Width/2, Y: y, Depth: depth, Step: s.step})
	if len(s.rows) > 0 {
		p.placeRows(s.rows, s.wrapped, anchor, x+trunk, depth+1, y+s.box.Height)
	}
	return anchor
}

// placeRows lays out the rows of children of a box: anchor is the column of the stem of that
// box, minLeft the leftmost column the children may take (right of the trunk, when the rows
// wrapped) and y the row of the first bus. Every row is centered on the stem; the next ones hang
// from the trunk. It returns the row right below the last child.
func (p *placer) placeRows(rows [][]sized, wrapped bool, anchor, minLeft, depth, y int) int {
	lefts := make([]int, len(rows))
	for i, row := range rows {
		lefts[i] = max(minLeft, anchor-rowWidth(row)/2)
	}
	trunkX := slices.Min(lefts) - TrunkWidth
	firstBus, lastBus := y, y
	for i, row := range rows {
		busY, x := y, lefts[i]
		centers := make([]int, len(row))
		for j, child := range row {
			centers[j] = p.place(child, depth, x, busY+1)
			x += child.width + AgentGap
		}
		// Only the first row joins the stem of the parent; the others rise only by the trunk.
		ups := []int{anchor}
		if i > 0 {
			ups = []int{trunkX}
		}
		downs := centers
		if wrapped && i < len(rows)-1 {
			downs = append([]int{trunkX}, centers...)
		}
		left, text := ConnectorRow(ups, downs, p.line)
		p.c.Buses = append(p.c.Buses, Connector{Y: busY, Left: left, Text: text})
		lastBus = busY
		y = busY + 1 + rowHeight(row)
	}
	if wrapped && lastBus > firstBus {
		p.c.Trunks = append(p.c.Trunks, Trunk{X: trunkX, Y: firstBus + 1, Height: lastBus - firstBus - 1})
	}
	return y
}

// NewClusterLayout lays out the cluster of a session in at most maxWidth columns, with the agent
// boxes of the given step. The first-level agents sit centered under the session, each agent
// centered over the ones it launched, and a bus joins every parent to its children
// (`┌──┴──┐`). A block of children that does not fit the width wraps into rows joined by a
// trunk at their left (`├`/`└`), with no link to the agents above. Without X and Y, which
// TreeLayout fills.
func NewClusterLayout(s TreeSession, maxWidth, boxHeight int, plan StepPlan, line style.Lines) ClusterLayout {
	rows, wrapped, rowsWidth := childRows(s.Agents, plan, 0, maxWidth)
	trunk := trunkRoom(wrapped)
	width := max(SessionWidth, trunk+rowsWidth)
	anchor := boxAnchor(0, width, SessionWidth, trunk, rowsWidth)
	c := ClusterLayout{
		Key: s.Key, Width: width, Height: boxHeight,
		SessionLeft: anchor - SessionWidth/2, Anchor: anchor,
	}
	if len(rows) == 0 {
		return c
	}
	c.Stem = &Connector{Y: boxHeight, Left: anchor, Text: line.Vertical}
	p := placer{line: line, c: &c}
	p.placeRows(rows, wrapped, anchor, trunk, 0, boxHeight+1)
	for _, a := range c.Agents {
		box := a.Size()
		c.Width = max(c.Width, a.X+box.Width)
		c.Height = max(c.Height, a.Y+box.Height)
	}
	return c
}

// NewTreeLayout spreads the clusters in rows that fit in columns, in the given order, each row
// centered. boxHeight is the height of the session boxes (see SessionBoxHeight). bodyRows is the
// rows the body gives the tree (UnlimitedRows for no limit): each session first takes the step
// its width allows (see PickStepPlan) and, when the content is taller than the body, every
// session drops a step together until the whole thing fits, since the height belongs to the set
// of clusters and not to any one of them. When not even the smallest boxes fit, the content
// keeps the boxes of the width and scrolls (see Viewport).
func NewTreeLayout(sessions []TreeSession, columns, bodyRows, boxHeight int, line style.Lines) TreeLayout {
	cols := max(1, columns)
	plans := make([]StepPlan, len(sessions))
	for i, s := range sessions {
		plans[i] = PickStepPlan(s, cols)
	}
	layout := placeClusters(sessions, plans, cols, boxHeight, line)
	if bodyRows < 0 || layout.ContentHeight <= bodyRows {
		return layout
	}
	for demoteAll(plans) {
		if smaller := placeClusters(sessions, plans, cols, boxHeight, line); smaller.ContentHeight <= bodyRows {
			return smaller
		}
	}
	// Not even the smallest boxes fit: the session boxes have a fixed height, so enough sessions
	// overflow the body on their own. Shrinking the agents buys nothing then, and the largest
	// boxes read better, so the tree keeps them and the rest scrolls.
	return layout
}

// demoteAll drops every level of every plan one step; it reports whether any of them moved, so a
// caller that is still out of room stops instead of looping. The monotony of a plan survives it:
// the distance between neighbor levels either stays or closes to zero.
func demoteAll(plans []StepPlan) bool {
	moved := false
	for _, plan := range plans {
		for d, step := range plan {
			if step < SmallestStep {
				plan[d], moved = step+1, true
			}
		}
	}
	return moved
}

// placeClusters lays every session out with its plan and spreads the clusters in rows of at most
// cols columns, each row centered.
func placeClusters(sessions []TreeSession, plans []StepPlan, cols, boxHeight int, line style.Lines) TreeLayout {
	clusters := make([]ClusterLayout, len(sessions))
	for i, s := range sessions {
		clusters[i] = NewClusterLayout(s, cols, boxHeight, plans[i], line)
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
