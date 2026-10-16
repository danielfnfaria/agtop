package model

import "slices"

// View is the screen shown.
type View string

// Panel is the focused panel of the detail view.
type Panel string

// Direction is an arrow.
type Direction string

const (
	ViewTree   View = "tree"
	ViewDetail View = "detail"

	PanelAgents Panel = "agents"
	PanelTools  Panel = "tools"

	DirUp    Direction = "up"
	DirDown  Direction = "down"
	DirLeft  Direction = "left"
	DirRight Direction = "right"
)

// SessionGoneNotice is the notice when the open session left the list.
const SessionGoneNotice = "the open session left the list; back to the tree"

// NavState is the navigation state. Empty strings mean "none" (keys are never empty). It is
// comparable, so an unchanged state is == to the previous one.
type NavState struct {
	View View
	// SelectedKey is the session selected in the tree (a stable key; the snapshot order changes).
	SelectedKey string
	// SelectedIndex is the index of SelectedKey in the visible keys (0 when nothing is selected).
	SelectedIndex int
	// DetailKey is the session open in the detail.
	DetailKey string
	Panel     Panel
	// SelectedAgentKey is the agent selected in the detail (a stable key; the agent order changes).
	SelectedAgentKey string
	// AgentIndex is the last known index of the selected agent, to pick a neighbor when it goes.
	AgentIndex int
	// ExpandedAgentKey is the agent with its row expanded in the detail.
	ExpandedAgentKey string
	// ProfileFilter is the label of the filtered profile; empty means all.
	ProfileFilter string
	Help          bool
	// Notice is a short message (e.g. the open session went away).
	Notice string
}

// InitialNav is the state at startup: the tree, nothing selected.
var InitialNav = NavState{View: ViewTree, Panel: PanelAgents}

// NavContext describes what is on screen when an action happens.
type NavContext struct {
	// Keys are the visible sessions, in the displayed order (the profile filter applied).
	Keys []string
	// Grid is the rows of clusters as on screen, each cell with the horizontal center of the
	// session. Empty means linear navigation in the order of Keys.
	Grid [][]NavCell
	// AgentKeys are the agents of the open session, in the displayed order.
	AgentKeys     []string
	ProfileLabels []string
}

// ActionType is what a navigation action does.
type ActionType int

const (
	ActionMove ActionType = iota + 1
	ActionOpen
	ActionBack
	ActionTab
	ActionToggleHelp
	ActionCycleProfile
	ActionClearNotice
)

// Action is a navigation action; Dir is only for ActionMove.
type Action struct {
	Type ActionType
	Dir  Direction
}

// LeaveDetail goes back to the tree, clearing the detail state (open session, panel, agent
// selection and expansion). A non-empty notice replaces the current one.
func LeaveDetail(s NavState, notice string) NavState {
	s.View = ViewTree
	s.DetailKey = ""
	s.Panel = PanelAgents
	s.SelectedAgentKey = ""
	s.AgentIndex = 0
	s.ExpandedAgentKey = ""
	if notice != "" {
		s.Notice = notice
	}
	return s
}

// SyncNav fits the state to the current context: selection in range, open session still there,
// valid filter.
func SyncNav(s NavState, ctx NavContext) NavState {
	if s.ProfileFilter != "" && !slices.Contains(ctx.ProfileLabels, s.ProfileFilter) {
		s.ProfileFilter = ""
	}
	if s.View == ViewDetail && (s.DetailKey == "" || !slices.Contains(ctx.Keys, s.DetailKey)) {
		s = LeaveDetail(s, SessionGoneNotice)
	}

	// Nothing is selected until an arrow key picks a session; a selected session that leaves the
	// list clears the selection instead of moving it to a neighbor.
	idx := -1
	if s.SelectedKey != "" {
		idx = slices.Index(ctx.Keys, s.SelectedKey)
	}
	if idx >= 0 {
		s.SelectedIndex = idx
	} else {
		s.SelectedKey, s.SelectedIndex = "", 0
	}

	if s.View == ViewDetail {
		if len(ctx.AgentKeys) == 0 {
			s.SelectedAgentKey, s.AgentIndex = "", 0
		} else {
			idx := -1
			if s.SelectedAgentKey != "" {
				idx = slices.Index(ctx.AgentKeys, s.SelectedAgentKey)
			}
			if idx >= 0 {
				s.AgentIndex = idx
			} else {
				s.AgentIndex = clamp(s.AgentIndex, 0, len(ctx.AgentKeys)-1)
				s.SelectedAgentKey = ctx.AgentKeys[s.AgentIndex]
			}
		}
		if s.ExpandedAgentKey != "" && !slices.Contains(ctx.AgentKeys, s.ExpandedAgentKey) {
			s.ExpandedAgentKey = ""
		}
	}
	return s
}

func cellOf(grid [][]NavCell, key string) (row, col int, ok bool) {
	if key == "" {
		return 0, 0, false
	}
	for r, cells := range grid {
		for c, cell := range cells {
			if cell.Key == key {
				return r, c, true
			}
		}
	}
	return 0, 0, false
}

// nearest is the cell of row with the center closest to x (a tie goes to the left one).
func nearest(row []NavCell, x int) (NavCell, bool) {
	var best NavCell
	found := false
	for _, cell := range row {
		if !found || abs(cell.Center-x) < abs(best.Center-x) {
			best, found = cell, true
		}
	}
	return best, found
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func selectKey(s NavState, key string, ctx NavContext) NavState {
	idx := -1
	if key != "" {
		idx = slices.Index(ctx.Keys, key)
	}
	if idx < 0 {
		return s
	}
	s.SelectedIndex, s.SelectedKey = idx, key
	return s
}

// moveInTree is spatial navigation: left and right walk the same row of clusters; up and down
// go to the neighbor row, to the cluster with the nearest center. Without a grid, left/up and
// right/down walk the order of the keys.
func moveInTree(s NavState, dir Direction, ctx NavContext) NavState {
	n := len(ctx.Keys)
	if n == 0 {
		return s
	}
	// With nothing selected, any arrow selects the first session.
	if s.SelectedKey == "" {
		s.SelectedIndex, s.SelectedKey = 0, ctx.Keys[0]
		return s
	}
	r, c, ok := cellOf(ctx.Grid, s.SelectedKey)
	if !ok {
		delta := 1
		if dir == DirLeft || dir == DirUp {
			delta = -1
		}
		target := clamp(s.SelectedIndex+delta, 0, n-1)
		s.SelectedIndex, s.SelectedKey = target, ctx.Keys[target]
		return s
	}
	row := ctx.Grid[r]
	switch dir {
	case DirLeft:
		if c > 0 {
			return selectKey(s, row[c-1].Key, ctx)
		}
	case DirRight:
		if c+1 < len(row) {
			return selectKey(s, row[c+1].Key, ctx)
		}
	case DirUp, DirDown:
		other := r + 1
		if dir == DirUp {
			other = r - 1
		}
		if other < 0 || other >= len(ctx.Grid) {
			return s
		}
		if cell, ok := nearest(ctx.Grid[other], row[c].Center); ok {
			return selectKey(s, cell.Key, ctx)
		}
	}
	return s
}

func moveInDetail(s NavState, dir Direction, ctx NavContext) NavState {
	if s.Panel != PanelAgents || len(ctx.AgentKeys) == 0 {
		return s
	}
	delta := 0
	switch dir {
	case DirUp:
		delta = -1
	case DirDown:
		delta = 1
	}
	if delta == 0 {
		return s
	}
	s.AgentIndex = clamp(s.AgentIndex+delta, 0, len(ctx.AgentKeys)-1)
	s.SelectedAgentKey = ctx.AgentKeys[s.AgentIndex]
	return s
}

// Navigate applies action over the state already synced with ctx. Pure.
func Navigate(state NavState, a Action, ctx NavContext) NavState {
	s := SyncNav(state, ctx)
	base := s
	if a.Type != ActionClearNotice {
		base.Notice = ""
	}

	switch a.Type {
	case ActionClearNotice:
		s.Notice = ""
		return s
	case ActionToggleHelp:
		base.Help = !base.Help
		return base
	case ActionBack:
		switch {
		case base.Help:
			base.Help = false
			return base
		case base.View == ViewDetail:
			return LeaveDetail(base, "")
		}
		// In the tree, Esc/Backspace clears the selection.
		base.SelectedKey, base.SelectedIndex = "", 0
		return base
	}

	// With the help open only ?, Esc and quit act.
	if base.Help {
		return base
	}

	switch a.Type {
	case ActionMove:
		if base.View == ViewTree {
			return moveInTree(base, a.Dir, ctx)
		}
		return moveInDetail(base, a.Dir, ctx)
	case ActionOpen:
		if base.View == ViewTree {
			if base.SelectedKey == "" {
				return base
			}
			// Same reset as leaving, so the detail always opens on a clean state.
			open := LeaveDetail(base, "")
			open.View, open.DetailKey = ViewDetail, base.SelectedKey
			return open
		}
		if base.Panel != PanelAgents {
			return base
		}
		key := base.SelectedAgentKey
		if key == "" && base.AgentIndex < len(ctx.AgentKeys) {
			key = ctx.AgentKeys[base.AgentIndex]
		}
		if key == "" {
			return base
		}
		if base.ExpandedAgentKey == key {
			base.ExpandedAgentKey = ""
		} else {
			base.ExpandedAgentKey = key
		}
		return base
	case ActionTab:
		if base.View != ViewDetail {
			return base
		}
		if base.Panel == PanelAgents {
			base.Panel = PanelTools
		} else {
			base.Panel = PanelAgents
		}
		return base
	case ActionCycleProfile:
		// With one profile (or none) there is nothing to filter.
		if base.View != ViewTree || len(ctx.ProfileLabels) < 2 {
			return base
		}
		i := -1
		if base.ProfileFilter != "" {
			i = slices.Index(ctx.ProfileLabels, base.ProfileFilter)
		}
		if i+1 < len(ctx.ProfileLabels) {
			base.ProfileFilter = ctx.ProfileLabels[i+1]
		} else {
			base.ProfileFilter = ""
		}
	}
	return base
}

// ScrollTop is the first visible row of a list that keeps selectedRow in the window of
// visibleRows, starting from the previous window (prevTop). It scrolls the minimum.
func ScrollTop(prevTop, selectedRow, visibleRows, totalRows int) int {
	vis := max(1, visibleRows)
	maxTop := max(0, totalRows-vis)
	top := clamp(prevTop, 0, maxTop)
	if selectedRow < top {
		top = selectedRow
	} else if selectedRow >= top+vis {
		top = selectedRow - vis + 1
	}
	return clamp(top, 0, maxTop)
}
