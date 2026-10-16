package model

import (
	"testing"
)

// grid is the clusters `a b / c d / e` (centers: left 10, right 50, alone 30).
var grid = [][]NavCell{
	{{"a", 10}, {"b", 50}},
	{{"c", 10}, {"d", 50}},
	{{"e", 30}},
}

func navCtx(mod ...func(*NavContext)) NavContext {
	c := NavContext{
		Keys:          []string{"a", "b", "c", "d", "e"},
		Grid:          grid,
		ProfileLabels: []string{"claude", "claude-x"},
	}
	for _, m := range mod {
		m(&c)
	}
	return c
}

func withKeys(k ...string) func(*NavContext)   { return func(c *NavContext) { c.Keys = k } }
func withAgents(k ...string) func(*NavContext) { return func(c *NavContext) { c.AgentKeys = k } }
func withGrid(g [][]NavCell) func(*NavContext) { return func(c *NavContext) { c.Grid = g } }
func withLabels(l ...string) func(*NavContext) { return func(c *NavContext) { c.ProfileLabels = l } }

// selectedA is the tree with `a` selected (what the first arrow key leaves behind).
var selectedA = func() NavState {
	s := InitialNav
	s.SelectedKey, s.SelectedIndex = "a", 0
	return s
}()

var (
	mvUp    = Action{ActionMove, DirUp}
	mvDown  = Action{ActionMove, DirDown}
	mvLeft  = Action{ActionMove, DirLeft}
	mvRight = Action{ActionMove, DirRight}
	open    = Action{Type: ActionOpen}
	back    = Action{Type: ActionBack}
	tab     = Action{Type: ActionTab}
	help    = Action{Type: ActionToggleHelp}
	cycle   = Action{Type: ActionCycleProfile}
	clearN  = Action{Type: ActionClearNotice}
)

func run(s NavState, c NavContext, actions ...Action) NavState {
	for _, a := range actions {
		s = Navigate(s, a, c)
	}
	return s
}

func TestSyncNav(t *testing.T) {
	sel := func(key string, idx int) NavState {
		s := InitialNav
		s.SelectedKey, s.SelectedIndex = key, idx
		return s
	}
	t.Run("empty list: nothing selected, index 0", func(t *testing.T) {
		if got := SyncNav(sel("x", 3), navCtx(withKeys())); got.SelectedKey != "" || got.SelectedIndex != 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("starts with nothing selected", func(t *testing.T) {
		if got := SyncNav(InitialNav, navCtx()); got.SelectedKey != "" || got.SelectedIndex != 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("follows the key when the order changes", func(t *testing.T) {
		s0 := SyncNav(sel("c", 2), navCtx())
		s1 := SyncNav(s0, navCtx(withKeys("c", "a", "b")))
		if s1.SelectedKey != "c" || s1.SelectedIndex != 0 {
			t.Errorf("got %+v", s1)
		}
	})
	t.Run("a selected session that leaves clears the selection", func(t *testing.T) {
		s0 := SyncNav(sel("b", 1), navCtx())
		if got := SyncNav(s0, navCtx(withKeys("a", "c", "d"))); got.SelectedKey != "" || got.SelectedIndex != 0 {
			t.Errorf("got %+v", got)
		}
		s2 := SyncNav(sel("e", 4), navCtx())
		if got := SyncNav(s2, navCtx(withKeys("a", "b"))); got.SelectedKey != "" || got.SelectedIndex != 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("an unchanged state is equal", func(t *testing.T) {
		s0 := SyncNav(InitialNav, navCtx())
		if SyncNav(s0, navCtx()) != s0 {
			t.Error("changed")
		}
		s1 := SyncNav(selectedA, navCtx())
		if SyncNav(s1, navCtx()) != s1 {
			t.Error("changed")
		}
	})
	t.Run("the open session going away returns to the tree with a notice", func(t *testing.T) {
		opened := run(selectedA, navCtx(), open)
		if opened.View != ViewDetail {
			t.Fatalf("view = %s", opened.View)
		}
		s := SyncNav(opened, navCtx(withKeys("b", "c")))
		if s.View != ViewTree || s.DetailKey != "" || s.Notice != SessionGoneNotice || s.SelectedKey != "" {
			t.Errorf("got %+v", s)
		}
	})
	t.Run("selected agent out of range and gone expanded one are fixed", func(t *testing.T) {
		c := navCtx(withAgents("x", "y", "z"))
		s0 := run(selectedA, c, open, mvDown, mvDown, open)
		if s0.AgentIndex != 2 || s0.ExpandedAgentKey != "z" {
			t.Fatalf("s0 = %+v", s0)
		}
		s1 := SyncNav(s0, navCtx(withAgents("x")))
		if s1.AgentIndex != 0 || s1.ExpandedAgentKey != "" {
			t.Errorf("s1 = %+v", s1)
		}
	})
	t.Run("reordering agents keeps the same agent selected and expanded", func(t *testing.T) {
		c := navCtx(withAgents("x", "y", "z"))
		s0 := run(selectedA, c, open, mvDown)
		if s0.SelectedAgentKey != "y" || s0.AgentIndex != 1 {
			t.Fatalf("s0 = %+v", s0)
		}
		c1 := navCtx(withAgents("y", "z", "x"))
		s1 := SyncNav(s0, c1)
		if s1.SelectedAgentKey != "y" || s1.AgentIndex != 0 {
			t.Errorf("s1 = %+v", s1)
		}
		if got := run(s0, c1, open).ExpandedAgentKey; got != "y" {
			t.Errorf("expanded = %q", got)
		}
		if got := run(s0, c1, mvDown).SelectedAgentKey; got != "z" {
			t.Errorf("down = %q", got)
		}
	})
	t.Run("a vanished selected agent leaves the neighbor at the same index", func(t *testing.T) {
		s0 := run(selectedA, navCtx(withAgents("x", "y", "z")), open, mvDown)
		s1 := SyncNav(s0, navCtx(withAgents("x", "z")))
		if s1.SelectedAgentKey != "z" || s1.AgentIndex != 1 {
			t.Errorf("s1 = %+v", s1)
		}
	})
	t.Run("a profile filter that is gone goes back to all", func(t *testing.T) {
		s := InitialNav
		s.ProfileFilter = "old"
		if got := SyncNav(s, navCtx()); got.ProfileFilter != "" {
			t.Errorf("filter = %q", got.ProfileFilter)
		}
	})
	t.Run("a pending close confirmation clears when the session leaves", func(t *testing.T) {
		s := selectedA
		s.Close = ClosePrompt{Key: "a"}
		if got := SyncNav(s, navCtx(withKeys("b", "c"))); got.Close.Key != "" {
			t.Errorf("close key = %q", got.Close.Key)
		}
		if got := SyncNav(s, navCtx(withKeys("a"))); got.Close.Key != "a" {
			t.Errorf("close key = %q", got.Close.Key)
		}
	})
}

func TestNavigateTree(t *testing.T) {
	t.Run("left and right walk the same row, without wrapping", func(t *testing.T) {
		c := navCtx()
		tests := []struct {
			name    string
			actions []Action
			want    string
		}{
			{"right", []Action{mvRight}, "b"},
			{"left at the start", []Action{mvLeft}, "a"},
			{"right at the end does not wrap", []Action{mvRight, mvRight, mvRight}, "b"},
			{"down, right, left, left", []Action{mvDown, mvRight, mvLeft, mvLeft}, "c"},
		}
		for _, tt := range tests {
			if got := run(selectedA, c, tt.actions...).SelectedKey; got != tt.want {
				t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("up and down go to the nearest cluster of the neighbor row", func(t *testing.T) {
		c := navCtx()
		tests := []struct {
			name    string
			actions []Action
			want    string
		}{
			{"up at the top", []Action{mvUp}, "a"},
			{"down", []Action{mvDown}, "c"},
			{"right, down", []Action{mvRight, mvDown}, "d"},
			{"a row with one cluster takes any column", []Action{mvRight, mvDown, mvDown}, "e"},
			{"left column too", []Action{mvDown, mvDown}, "e"},
			{"down at the bottom does nothing", []Action{mvDown, mvDown, mvDown}, "e"},
			{"a tie goes to the left one", []Action{mvDown, mvDown, mvUp}, "c"},
		}
		for _, tt := range tests {
			if got := run(selectedA, c, tt.actions...).SelectedKey; got != tt.want {
				t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
			}
		}
	})

	t.Run("the nearest wins across rows of different widths", func(t *testing.T) {
		c := navCtx(withGrid([][]NavCell{
			{{"a", 20}, {"b", 60}, {"c", 110}},
			{{"d", 40}, {"e", 80}},
		}))
		tests := []struct {
			actions []Action
			want    string
		}{
			{[]Action{mvRight, mvRight, mvDown}, "e"},
			{[]Action{mvDown, mvRight, mvUp}, "b"},
			{[]Action{mvDown, mvUp}, "a"},
		}
		for _, tt := range tests {
			if got := run(selectedA, c, tt.actions...).SelectedKey; got != tt.want {
				t.Errorf("%v: %q, want %q", tt.actions, got, tt.want)
			}
		}
	})

	t.Run("with nothing selected any arrow selects the first session", func(t *testing.T) {
		for _, g := range [][][]NavCell{grid, nil} {
			for _, dir := range []Direction{DirUp, DirDown, DirLeft, DirRight} {
				got := run(InitialNav, navCtx(withGrid(g)), Action{ActionMove, dir})
				if got.SelectedKey != "a" || got.SelectedIndex != 0 {
					t.Errorf("%s: %+v", dir, got)
				}
			}
		}
		// The selection follows the displayed order, not a fixed key.
		if got := run(InitialNav, navCtx(withKeys("d", "a")), mvDown).SelectedKey; got != "d" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("Enter with nothing selected does not open", func(t *testing.T) {
		got := run(InitialNav, navCtx(), open)
		if got.View != ViewTree || got.DetailKey != "" || got.SelectedKey != "" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("without a grid the keys order is linear", func(t *testing.T) {
		c := navCtx(withGrid(nil))
		if got := run(selectedA, c, mvRight, mvRight).SelectedKey; got != "c" {
			t.Errorf("got %q", got)
		}
		if got := run(selectedA, c, mvDown, mvUp).SelectedKey; got != "a" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("an empty list: moving and opening do nothing", func(t *testing.T) {
		got := run(InitialNav, navCtx(withKeys(), withGrid(nil)), mvDown, open)
		if got.View != ViewTree || got.SelectedKey != "" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("p cycles all, each label, all", func(t *testing.T) {
		c := navCtx()
		s1 := run(InitialNav, c, cycle)
		s2 := run(s1, c, cycle)
		s3 := run(s2, c, cycle)
		if s1.ProfileFilter != "claude" || s2.ProfileFilter != "claude-x" || s3.ProfileFilter != "" {
			t.Errorf("filters: %q %q %q", s1.ProfileFilter, s2.ProfileFilter, s3.ProfileFilter)
		}
	})

	t.Run("p with fewer than two profiles does nothing", func(t *testing.T) {
		for _, labels := range [][]string{{"claude"}, nil} {
			if got := run(InitialNav, navCtx(withLabels(labels...)), cycle); got.ProfileFilter != "" {
				t.Errorf("%v: %q", labels, got.ProfileFilter)
			}
		}
	})

	t.Run("Tab in the tree does nothing", func(t *testing.T) {
		c := navCtx()
		s0 := SyncNav(selectedA, c)
		if got := run(s0, c, tab); got != s0 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("Esc clears the selection; the next arrow selects the first again", func(t *testing.T) {
		c := navCtx()
		s0 := run(selectedA, c, mvRight, mvDown)
		if s0.SelectedKey != "d" {
			t.Fatalf("s0 = %q", s0.SelectedKey)
		}
		cleared := run(s0, c, back)
		if cleared.View != ViewTree || cleared.SelectedKey != "" || cleared.SelectedIndex != 0 {
			t.Errorf("cleared = %+v", cleared)
		}
		if run(cleared, c, back) != cleared {
			t.Error("a second Esc changed the state")
		}
		if got := run(cleared, c, mvRight).SelectedKey; got != "a" {
			t.Errorf("got %q", got)
		}
	})
}

func TestNavigateDetailAndHelp(t *testing.T) {
	c := navCtx(withAgents("x", "y"))

	t.Run("Enter opens the selected; Esc goes back keeping the selection", func(t *testing.T) {
		s := run(selectedA, c, mvRight, open)
		if s.View != ViewDetail || s.DetailKey != "b" || s.Panel != PanelAgents || s.AgentIndex != 0 {
			t.Errorf("s = %+v", s)
		}
		b := run(s, c, back)
		if b.View != ViewTree || b.DetailKey != "" || b.SelectedKey != "b" {
			t.Errorf("b = %+v", b)
		}
	})

	t.Run("up and down choose the agent; Enter expands and collapses", func(t *testing.T) {
		s := run(selectedA, c, open, mvDown)
		if s.AgentIndex != 1 || run(s, c, mvDown).AgentIndex != 1 {
			t.Errorf("index = %d", s.AgentIndex)
		}
		exp := run(s, c, open)
		if exp.ExpandedAgentKey != "y" || run(exp, c, open).ExpandedAgentKey != "" {
			t.Errorf("expanded = %q", exp.ExpandedAgentKey)
		}
	})

	t.Run("Tab switches the panel; in tools the arrows and Enter leave the agents alone", func(t *testing.T) {
		s := run(selectedA, c, open, tab)
		if s.Panel != PanelTools {
			t.Fatalf("panel = %s", s.Panel)
		}
		s2 := run(s, c, mvDown, open)
		if s2.AgentIndex != 0 || s2.ExpandedAgentKey != "" {
			t.Errorf("s2 = %+v", s2)
		}
		if run(s, c, tab).Panel != PanelAgents {
			t.Error("Tab did not switch back")
		}
	})

	t.Run("Enter without agents does nothing", func(t *testing.T) {
		s := run(selectedA, navCtx(), open, open)
		if s.View != ViewDetail || s.ExpandedAgentKey != "" {
			t.Errorf("s = %+v", s)
		}
	})

	t.Run("? opens the help, which blocks navigation; Esc or ? close it", func(t *testing.T) {
		h := run(selectedA, c, help)
		if !h.Help {
			t.Fatal("help closed")
		}
		blocked := run(h, c, mvRight, open)
		if blocked.SelectedKey != "a" || blocked.View != ViewTree {
			t.Errorf("blocked = %+v", blocked)
		}
		if closed := run(h, c, back); closed.Help || closed.SelectedKey != "a" {
			t.Errorf("closed = %+v", closed)
		}
		if run(h, c, help).Help {
			t.Error("? did not close")
		}
	})

	t.Run("Esc with the help open in the detail closes only the help", func(t *testing.T) {
		s := run(selectedA, c, open, help, back)
		if s.View != ViewDetail || s.Help {
			t.Errorf("s = %+v", s)
		}
	})

	t.Run("any action clears the notice; so does clearNotice", func(t *testing.T) {
		c1 := navCtx(withKeys("b"))
		gone := SyncNav(run(selectedA, c, open), c1)
		if gone.Notice == "" {
			t.Fatal("no notice")
		}
		if run(gone, c1, mvDown).Notice != "" || run(gone, c1, clearN).Notice != "" {
			t.Error("notice not cleared")
		}
	})
}

func TestScrollTop(t *testing.T) {
	tests := []struct {
		name                     string
		prev, selected, vis, tot int
		want                     int
	}{
		{"keeps the window while the selected fits", 0, 1, 2, 5, 0},
		{"keeps the window, lower", 2, 3, 2, 5, 2},
		{"scrolls down the minimum", 0, 2, 2, 5, 1},
		{"scrolls up the minimum", 3, 1, 2, 5, 1},
		{"window past the end after shrinking", 4, 0, 2, 2, 0},
		{"window past the end, taller", 10, 1, 3, 2, 0},
		{"zero height counts as 1", 0, 3, 0, 5, 3},
	}
	for _, tt := range tests {
		if got := ScrollTop(tt.prev, tt.selected, tt.vis, tt.tot); got != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestLeaveDetail(t *testing.T) {
	in := NavState{
		View: ViewDetail, SelectedKey: "a", DetailKey: "a", Panel: PanelTools,
		SelectedAgentKey: "x", AgentIndex: 3, ExpandedAgentKey: "x", Notice: "old",
	}
	want := NavState{View: ViewTree, SelectedKey: "a", Panel: PanelAgents, Notice: "old"}
	if got := LeaveDetail(in, ""); got != want {
		t.Errorf("got %+v", got)
	}
	if got := LeaveDetail(in, "gone").Notice; got != "gone" {
		t.Errorf("notice = %q", got)
	}
}

func TestKeyToCommand(t *testing.T) {
	navCmd := func(a Action) Command { return Command{Kind: CommandNav, Nav: a} }
	tests := []struct {
		key  string
		want Command
		ok   bool
	}{
		{"ctrl+c", Command{Kind: CommandQuit}, true},
		{"q", Command{Kind: CommandQuit}, true},
		{"ctrl+l", Command{Kind: CommandRedraw}, true},
		{"up", navCmd(mvUp), true}, {"k", navCmd(mvUp), true},
		{"down", navCmd(mvDown), true}, {"j", navCmd(mvDown), true},
		{"left", navCmd(mvLeft), true}, {"h", navCmd(mvLeft), true},
		{"right", navCmd(mvRight), true}, {"l", navCmd(mvRight), true},
		{"enter", navCmd(open), true},
		{"esc", navCmd(back), true}, {"backspace", navCmd(back), true}, {"delete", navCmd(back), true},
		{"tab", navCmd(tab), true}, {"shift+tab", navCmd(tab), true},
		{"?", navCmd(help), true},
		{"p", navCmd(cycle), true},
		{"x", Command{Kind: CommandClose}, true},
		{"ctrl+k", Command{}, false}, {"alt+q", Command{}, false}, {"", Command{}, false},
	}
	for _, tt := range tests {
		got, ok := KeyToCommand(tt.key)
		if got != tt.want || ok != tt.ok {
			t.Errorf("KeyToCommand(%q) = %+v, %v", tt.key, got, ok)
		}
	}
}
