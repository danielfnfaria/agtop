package model

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/ui/style"
)

var line = style.UnicodeGlyphs.Line

const (
	hz = "─"
	vt = "│"
	dr = "┌"
	dl = "┐"
	hu = "┴"
	hd = "┬"
	cx = "┼"
	vr = "├"
	vl = "┤"
	ur = "└"
)

// upLinks are the line characters with a link going up.
var upLinks = []string{vt, ur, "┘", vr, vl, hu, cx}

func rep(s string, n int) string { return strings.Repeat(s, n) }

func session(key string, agents int) TreeSession {
	s := TreeSession{Key: key}
	for i := range agents {
		s.Agents = append(s.Agents, TreeAgent{Key: fmt.Sprintf("%s-a%d", key, i)})
	}
	return s
}

func sessions(n, agents int) []TreeSession {
	out := make([]TreeSession, n)
	for i := range out {
		out[i] = session(fmt.Sprintf("s%d", i), agents)
	}
	return out
}

// cluster lays a session out the way the tree does: the steps are picked by the width.
func cluster(s TreeSession, maxWidth int) ClusterLayout {
	return NewClusterLayout(s, maxWidth, SessionHeight, PickStepPlan(s, maxWidth), line)
}

// clusterStep forces one step for every level, to exercise the wrapping of a tree that does not
// fit.
func clusterStep(s TreeSession, maxWidth int, step AgentStep) ClusterLayout {
	return NewClusterLayout(s, maxWidth, SessionHeight, StepPlan{step}, line)
}

// layoutOf lays the sessions out with no height limit, so only the width picks the steps.
func layoutOf(s []TreeSession, columns int) TreeLayout {
	return NewTreeLayout(s, columns, UnlimitedRows, SessionHeight, line)
}

// agentRows are the agent boxes by row, each row from left to right.
func agentRows(c ClusterLayout) [][]AgentBox {
	boxes := slices.Clone(c.Agents)
	slices.SortFunc(boxes, func(a, b AgentBox) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		return a.X - b.X
	})
	var rows [][]AgentBox
	for i, b := range boxes {
		if i == 0 || b.Y != boxes[i-1].Y {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], b)
	}
	return rows
}

// rowKeys are the keys of a row of boxes.
func rowKeys(row []AgentBox) []string {
	out := make([]string, len(row))
	for i, b := range row {
		out[i] = b.Key
	}
	return out
}

// busAt is the bus on the given row of the cluster.
func busAt(t *testing.T, c ClusterLayout, y int) Connector {
	t.Helper()
	for _, b := range c.Buses {
		if b.Y == y {
			return b
		}
	}
	t.Fatalf("no bus on row %d: %+v", y, c.Buses)
	return Connector{}
}

func TestConnectorRow(t *testing.T) {
	tests := []struct {
		name       string
		ups, downs []int
		wantLeft   int
		wantText   string
	}{
		{"one up and one down in the same column: straight", []int{17}, []int{17}, 17, vt},
		{"two agents", []int{25}, []int{12, 38}, 12, dr + rep(hz, 12) + hu + rep(hz, 12) + dl},
		{"three agents, middle under the stem", []int{38}, []int{12, 38, 64}, 12, dr + rep(hz, 25) + cx + rep(hz, 25) + dl},
		{"stem off center and at an end", []int{0}, []int{0, 5, 10}, 0, vr + rep(hz, 4) + hd + rep(hz, 4) + dl},
		{"no links", nil, nil, 0, ""},
	}
	for _, tt := range tests {
		left, text := ConnectorRow(tt.ups, tt.downs, line)
		if left != tt.wantLeft || text != tt.wantText {
			t.Errorf("%s: got %d %q, want %d %q", tt.name, left, text, tt.wantLeft, tt.wantText)
		}
	}
}

func TestAgentSteps(t *testing.T) {
	last := AgentSize{Width: 1 << 30, Height: 1 << 30}
	for step := StepFull; step <= SmallestStep; step++ {
		size := step.Size()
		if size.Width >= last.Width || size.Height > last.Height {
			t.Errorf("step %d (%+v) is not smaller than %+v", step, size, last)
		}
		last = size
	}
	if got := StepFull.Size(); got != (AgentSize{Width: AgentWidth, Height: AgentHeight}) {
		t.Errorf("full step = %+v", got)
	}
	if got := AgentStep(-1).Size(); got != StepFull.Size() {
		t.Errorf("step out of range = %+v", got)
	}
}

func TestStepPlan(t *testing.T) {
	plan := StepPlan{StepFull, StepNoModel}
	// The last step of the plan holds for every level below it.
	for depth, want := range map[int]AgentStep{-1: StepFull, 0: StepFull, 1: StepNoModel, 7: StepNoModel} {
		if got := plan.At(depth); got != want {
			t.Errorf("level %d: step %d, want %d", depth, got, want)
		}
	}
	if got := plan.Size(1); got != StepNoModel.Size() {
		t.Errorf("size = %+v", got)
	}
	if got := StepPlan(nil).At(3); got != StepFull {
		t.Errorf("empty plan = %d", got)
	}
}

func TestForestWidth(t *testing.T) {
	// p takes the widest of its box and its two children, side by side with q.
	forest := []TreeAgent{nestedAgent("p", TreeAgent{Key: "c1"}, TreeAgent{Key: "c2"}), {Key: "q"}}
	tests := []struct {
		plan StepPlan
		want int
	}{
		{StepPlan{StepFull}, 2*AgentWidth + AgentGap + AgentGap + AgentWidth},
		// The level of the children takes a smaller step, so the subtree of p shrinks with it.
		{StepPlan{StepFull, StepTiny}, 13 + AgentGap + 13 + AgentGap + AgentWidth},
		{StepPlan{StepTiny}, 13 + AgentGap + 13 + AgentGap + 13},
	}
	for _, tt := range tests {
		if got := forestWidth(forest, tt.plan, 0); got != tt.want {
			t.Errorf("plan %v: width %d, want %d", tt.plan, got, tt.want)
		}
	}
	if got := ForestDepth(forest); got != 2 {
		t.Errorf("depth = %d", got)
	}
	if got := forestWidth(nil, StepPlan{StepFull}, 0); got != 0 {
		t.Errorf("empty forest = %d", got)
	}
}

func TestPickStepPlan(t *testing.T) {
	// Three first-level agents: 82 columns at the full step, 64 at 20, 46 at 14, 43 at 13.
	three := session("s", 3)
	// A coordinator with eight agents of its own: no step holds them in one row under 100
	// columns, so they take two rows at 20 and the coordinator alone goes up one step.
	fan8 := TreeSession{Key: "s", Agents: []TreeAgent{nestedAgent("p")}}
	for i := range 8 {
		fan8.Agents[0].Children = append(fan8.Agents[0].Children, TreeAgent{Key: fmt.Sprint("c", i)})
	}
	nested := TreeSession{Key: "s", Agents: []TreeAgent{nestedAgent("p", TreeAgent{Key: "c1"}, TreeAgent{Key: "c2"})}}
	tests := []struct {
		name    string
		agents  TreeSession
		columns int
		want    StepPlan
	}{
		{"three in one row", three, 100, StepPlan{StepFull}},
		{"three in one row, exactly", three, 82, StepPlan{StepFull}},
		// 81 columns hold no more than two full boxes, and two rows of them beat one row of
		// smaller ones.
		{"three in two rows at the full step", three, 81, StepPlan{StepFull}},
		{"three in two rows at 20", three, 54, StepPlan{StepNoModel}},
		{"three in two rows at 14", three, 43, StepPlan{StepCompact}},
		{"three that never fit two rows", three, 30, StepPlan{StepTiny}},
		{"no agents", session("s", 0), 40, StepPlan{StepFull}},
		// A nested tree is as wide as its widest level needs.
		{"nested in one row", nested, 54, StepPlan{StepFull, StepFull}},
		{"nested wrapping the children", nested, 53, StepPlan{StepFull, StepFull}},
		{"the first level goes up one step", fan8, 100, StepPlan{StepFull, StepNoModel}},
	}
	for _, tt := range tests {
		if got := PickStepPlan(tt.agents, tt.columns); !slices.Equal(got, tt.want) {
			t.Errorf("%s (%d columns): plan %v, want %v", tt.name, tt.columns, got, tt.want)
		}
	}
}

func TestClusterLayout(t *testing.T) {
	t.Run("no active agents: only the box", func(t *testing.T) {
		c := cluster(session("s", 0), 140)
		if c.Width != SessionWidth || c.Height != SessionHeight || c.SessionLeft != 0 || c.Stem != nil || len(c.Agents) != 0 {
			t.Errorf("got %+v", c)
		}
	})

	t.Run("one agent: straight stem and connector", func(t *testing.T) {
		c := cluster(session("s", 1), 140)
		if c.Width != SessionWidth || c.Anchor != 17 || clusterSteps(t, c).At(0) != StepFull {
			t.Errorf("width %d anchor %d step %d", c.Width, c.Anchor, clusterSteps(t, c).At(0))
		}
		if want := (Connector{Y: SessionHeight, Left: 17, Text: vt}); *c.Stem != want {
			t.Errorf("stem = %+v", *c.Stem)
		}
		want := []AgentBox{{Key: "s-a0", X: 4, Y: SessionHeight + 2}}
		if !reflect.DeepEqual(c.Agents, want) {
			t.Errorf("agents = %+v", c.Agents)
		}
		if bus := busAt(t, c, SessionHeight+1); bus != (Connector{Y: SessionHeight + 1, Left: 17, Text: vt}) {
			t.Errorf("bus = %+v", bus)
		}
		if c.Height != SessionHeight+2+AgentHeight || len(c.Trunks) != 0 {
			t.Errorf("height = %d trunks = %+v", c.Height, c.Trunks)
		}
	})

	t.Run("two agents", func(t *testing.T) {
		c := cluster(session("s", 2), 140)
		if c.Width != 54 || c.SessionLeft != 10 || c.Anchor != 27 {
			t.Errorf("width %d left %d anchor %d", c.Width, c.SessionLeft, c.Anchor)
		}
		want := Connector{Y: SessionHeight + 1, Left: 13, Text: dr + rep(hz, 13) + hu + rep(hz, 13) + dl}
		if bus := busAt(t, c, SessionHeight+1); bus != want {
			t.Errorf("bus = %+v", bus)
		}
	})

	t.Run("three agents: cross under the session", func(t *testing.T) {
		c := cluster(session("s", 3), 140)
		if c.Width != 82 || busAt(t, c, SessionHeight+1).Text != dr+rep(hz, 27)+cx+rep(hz, 27)+dl {
			t.Errorf("width %d bus %q", c.Width, busAt(t, c, SessionHeight+1).Text)
		}
	})

	t.Run("agents that do not fit even at the smallest step wrap into rows joined by a trunk", func(t *testing.T) {
		// At the full step 110 columns hold 4 agents, 3 with the trunk (2 columns): 5 become 3 + 2.
		c := clusterStep(session("s", 5), 110, StepFull)
		if c.Width != 84 || c.Anchor != 43 || c.SessionLeft != 26 {
			t.Errorf("width %d anchor %d left %d", c.Width, c.Anchor, c.SessionLeft)
		}
		rows := agentRows(c)
		if got := []int{len(rows[0]), len(rows[1])}; !slices.Equal(got, []int{3, 2}) {
			t.Fatalf("rows = %v", got)
		}
		first, second := rows[0], rows[1]
		if first[0].Y != SessionHeight+2 || second[0].Y != first[0].Y+AgentHeight+1 {
			t.Errorf("rows at %d and %d", first[0].Y, second[0].Y)
		}
		if first[0].X != 2 || second[0].X != 16 {
			t.Errorf("lefts %d and %d", first[0].X, second[0].X)
		}
		wantFirst := Connector{Y: SessionHeight + 1, Left: 0, Text: dr + rep(hz, 14) + hd + rep(hz, 27) + cx + rep(hz, 27) + dl}
		if bus := busAt(t, c, SessionHeight+1); bus != wantFirst {
			t.Errorf("first bus = %+v", bus)
		}
		wantSecond := Connector{Y: first[0].Y + AgentHeight, Left: 0, Text: ur + rep(hz, 28) + hd + rep(hz, 27) + dl}
		if bus := busAt(t, c, first[0].Y+AgentHeight); bus != wantSecond {
			t.Errorf("second bus = %+v", bus)
		}
		// The trunk runs from right below the first bus to right above the last one.
		want := []Trunk{{X: 0, Y: SessionHeight + 2, Height: AgentHeight}}
		if !reflect.DeepEqual(c.Trunks, want) {
			t.Errorf("trunks = %+v", c.Trunks)
		}
		if c.Height != second[0].Y+AgentHeight {
			t.Errorf("height = %d", c.Height)
		}
	})

	t.Run("seven agents in three rows: continuous trunk, nothing rises between the agents", func(t *testing.T) {
		c := clusterStep(session("s", 7), 110, StepFull)
		var counts, lefts []int
		for _, row := range agentRows(c) {
			counts = append(counts, len(row))
			lefts = append(lefts, row[0].X)
		}
		if !slices.Equal(counts, []int{3, 3, 1}) || !slices.Equal(lefts, []int{2, 2, 30}) {
			t.Fatalf("rows %v lefts %v", counts, lefts)
		}
		buses := slices.Clone(c.Buses)
		slices.SortFunc(buses, func(a, b Connector) int { return a.Y - b.Y })
		var firsts []string
		for _, b := range buses {
			firsts = append(firsts, string([]rune(b.Text)[:1]))
			if b.Left != 0 {
				t.Errorf("bus on row %d starts at %d", b.Y, b.Left)
			}
		}
		if !slices.Equal(firsts, []string{dr, vr, ur}) {
			t.Errorf("bus corners %v", firsts)
		}
		if got := buses[1].Text; got != vr+rep(hz, 14)+hd+rep(hz, 27)+hd+rep(hz, 27)+dl {
			t.Errorf("second bus = %q", got)
		}
		if got := buses[2].Text; got != ur+rep(hz, 42)+dl {
			t.Errorf("third bus = %q", got)
		}
		for i, b := range buses {
			var upAt []int
			for k, ch := range []rune(b.Text) {
				if slices.Contains(upLinks, string(ch)) {
					upAt = append(upAt, b.Left+k)
				}
			}
			// Only the first row joins the stem of the session; the others rise only by the trunk.
			want := []int{0}
			if i == 0 {
				want = []int{c.Anchor}
			}
			if !slices.Equal(upAt, want) {
				t.Errorf("row %d rises at %v, want %v", i, upAt, want)
			}
		}
		// One trunk, from the first bus down to the last one, with no gap.
		want := []Trunk{{X: 0, Y: buses[0].Y + 1, Height: buses[2].Y - buses[0].Y - 1}}
		if !reflect.DeepEqual(c.Trunks, want) {
			t.Errorf("trunks = %+v", c.Trunks)
		}
		if c.Width > 110 {
			t.Errorf("width %d", c.Width)
		}
	})

	t.Run("narrow: one agent per row, hanging from the trunk", func(t *testing.T) {
		c := clusterStep(session("s", 3), 40, StepFull)
		if c.Width != SessionWidth || c.Anchor != 17 {
			t.Errorf("width %d anchor %d", c.Width, c.Anchor)
		}
		var lefts []int
		for _, row := range agentRows(c) {
			if len(row) != 1 {
				t.Fatalf("rows = %+v", agentRows(c))
			}
			lefts = append(lefts, row[0].X)
		}
		if !slices.Equal(lefts, []int{4, 4, 4}) {
			t.Errorf("lefts %v", lefts)
		}
		buses := slices.Clone(c.Buses)
		slices.SortFunc(buses, func(a, b Connector) int { return a.Y - b.Y })
		want := []Connector{
			{Y: SessionHeight + 1, Left: 2, Text: dr + rep(hz, 14) + vl},
			{Y: SessionHeight + 1 + 1 + AgentHeight, Left: 2, Text: vr + rep(hz, 14) + dl},
			{Y: SessionHeight + 1 + 2*(1+AgentHeight), Left: 2, Text: ur + rep(hz, 14) + dl},
		}
		if !reflect.DeepEqual(buses, want) {
			t.Errorf("buses = %+v", buses)
		}
		if !reflect.DeepEqual(c.Trunks, []Trunk{{X: 2, Y: SessionHeight + 2, Height: 2*(1+AgentHeight) - 1}}) {
			t.Errorf("trunks = %+v", c.Trunks)
		}
		if c.Height != SessionHeight+1+3*(1+AgentHeight) {
			t.Errorf("height = %d", c.Height)
		}
	})

	t.Run("a tree too wide for one row wraps instead of shrinking the boxes", func(t *testing.T) {
		// 110 columns hold three full boxes with the trunk: 5 agents become 3 + 2, which says
		// more than a single row of 20-column boxes.
		c := cluster(session("s", 5), 110)
		if steps := clusterSteps(t, c); steps.At(0) != StepFull || len(agentRows(c)) != 2 || len(c.Trunks) != 1 {
			t.Errorf("steps %v rows %d trunks %+v", steps, len(agentRows(c)), c.Trunks)
		}
		if c.Width != 2+3*AgentWidth+2*AgentGap {
			t.Errorf("width = %d", c.Width)
		}
		checkCentered(t, c, session("s", 5).Agents)
	})
}

func TestTreeLayout(t *testing.T) {
	t.Run("one session is centered, horizontally and by the viewport vertically", func(t *testing.T) {
		l := layoutOf(sessions(1, 0), 140)
		c := l.Rows[0].Clusters[0]
		if len(l.Rows) != 1 || c.X != 53 || c.Y != 0 || l.ContentHeight != SessionHeight {
			t.Errorf("layout = %+v", l)
		}
		vp := l.Viewport(40, "s0", 0)
		if vp.Scrolling || vp.OffsetY != 16 || vp.Top != 0 {
			t.Errorf("viewport = %+v", vp)
		}
	})

	t.Run("sessions sit side by side and wrap by the width", func(t *testing.T) {
		tests := []struct {
			n, columns int
			want       []int
		}{
			{2, 140, []int{2}}, {3, 140, []int{3}}, {4, 140, []int{3, 1}}, {5, 140, []int{3, 2}},
			{6, 140, []int{3, 3}}, {6, 100, []int{2, 2, 2}}, {3, 60, []int{1, 1, 1}},
		}
		for _, tt := range tests {
			var got []int
			for _, r := range layoutOf(sessions(tt.n, 0), tt.columns).Rows {
				got = append(got, len(r.Clusters))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("%d sessions at %d: %v, want %v", tt.n, tt.columns, got, tt.want)
			}
		}
	})

	t.Run("rows are centered and stack with a one-row gap", func(t *testing.T) {
		l := layoutOf(sessions(4, 0), 140)
		r0, r1 := l.Rows[0], l.Rows[1]
		if r0.Y != 0 || r0.Left != 15 || r0.Width != 110 || r1.Y != SessionHeight+1 || r1.Left != 53 {
			t.Errorf("rows = %+v %+v", r0, r1)
		}
		var xs []int
		for _, c := range r0.Clusters {
			xs = append(xs, c.X)
		}
		if !slices.Equal(xs, []int{15, 53, 91}) || l.ContentHeight != 2*SessionHeight+1 {
			t.Errorf("xs %v height %d", xs, l.ContentHeight)
		}
	})

	t.Run("cluster width is the max of the session and the agent row", func(t *testing.T) {
		l := layoutOf([]TreeSession{session("a", 3), session("b", 0), session("c", 2)}, 140)
		var got [][]int
		for _, r := range l.Rows {
			var ws []int
			for _, c := range r.Clusters {
				ws = append(ws, c.Width)
			}
			got = append(got, ws)
		}
		if !reflect.DeepEqual(got, [][]int{{82, 34}, {54}}) || l.Rows[0].Height != SessionHeight+2+AgentHeight {
			t.Errorf("widths %v height %d", got, l.Rows[0].Height)
		}
	})

	t.Run("no row passes the width", func(t *testing.T) {
		all := slices.Concat([]TreeSession{session("a", 6)}, sessions(5, 1), []TreeSession{session("z", 2)})
		for _, columns := range []int{40, 60, 80, 100, 140} {
			for _, r := range layoutOf(all, columns).Rows {
				if r.Left+r.Width > columns {
					t.Errorf("%d columns: row ends at %d", columns, r.Left+r.Width)
				}
				for _, c := range r.Clusters {
					if c.X+c.Width > columns {
						t.Errorf("%d columns: cluster ends at %d", columns, c.X+c.Width)
					}
				}
			}
		}
	})

	t.Run("navGrid has the absolute center of each session", func(t *testing.T) {
		want := [][]NavCell{
			{{"s0", 32}, {"s1", 70}, {"s2", 108}},
			{{"s3", 70}},
		}
		if got := layoutOf(sessions(4, 0), 140).NavGrid(); !reflect.DeepEqual(got, want) {
			t.Errorf("grid = %+v", got)
		}
	})
}

func TestTreeViewport(t *testing.T) {
	l := layoutOf(sessions(6, 0), 60) // 6 rows of 7 + 5 gaps = 47 rows

	if vp := l.Viewport(UnlimitedRows, "s0", 0); vp.Scrolling || vp.OffsetY != 0 || vp.Top != 0 {
		t.Errorf("unlimited = %+v", vp)
	}
	if l.ContentHeight != 47 {
		t.Fatalf("content height = %d", l.ContentHeight)
	}
	first := l.Viewport(22, "s0", 0)
	if want := (TreeViewport{Top: 0, ViewRows: 20, Scrolling: true, Below: true}); first != want {
		t.Errorf("first = %+v", first)
	}
	// s2 starts at row 16 and ends at 23: the 20-row window ends at 23.
	third := l.Viewport(22, "s2", first.Top)
	if third.Top != 3 || !third.Above || !third.Below {
		t.Errorf("third = %+v", third)
	}
	// s1 (8..15) is already visible.
	if back := l.Viewport(22, "s1", third.Top); back.Top != 3 {
		t.Errorf("back = %+v", back)
	}
	last := l.Viewport(22, "s5", 3)
	if last.Top != 27 || !last.Above || last.Below {
		t.Errorf("last = %+v", last)
	}
}

// TestTreeLayoutFitsTheHeight covers the second dimension of the step: a tree that fits the
// width but not the body shrinks until it does, and keeps the readable boxes when no step fits.
func TestTreeLayoutFitsTheHeight(t *testing.T) {
	// A chain four deep is one box wide, so the width alone leaves it at the full step. Its
	// height is the session box, the stem and a row per level: 32 rows at the full step and at
	// 20 columns of box, 28 at 14 and 24 at 13.
	deep := []TreeSession{{Key: "s", Agents: []TreeAgent{chain("d", 4)}}}
	layout := func(rows int) TreeLayout { return NewTreeLayout(deep, 140, rows, SessionHeight, line) }

	t.Run("no limit: the width alone picks the step", func(t *testing.T) {
		l := layout(UnlimitedRows)
		if steps := clusterSteps(t, l.Rows[0].Clusters[0]); steps.At(0) != StepFull || l.ContentHeight != 32 {
			t.Errorf("steps %v height %d", steps, l.ContentHeight)
		}
	})

	t.Run("taller than the body: one step down at a time", func(t *testing.T) {
		tests := []struct {
			rows   int
			want   AgentStep
			height int
		}{
			{40, StepFull, 32}, {32, StepFull, 32}, {31, StepCompact, 28}, {28, StepCompact, 28},
			{27, StepTiny, 24}, {24, StepTiny, 24},
		}
		for _, tt := range tests {
			l := layout(tt.rows)
			c := l.Rows[0].Clusters[0]
			steps := clusterSteps(t, c)
			if steps.At(0) != tt.want || l.ContentHeight != tt.height {
				t.Errorf("%d rows: steps %v height %d, want step %d height %d",
					tt.rows, steps, l.ContentHeight, tt.want, tt.height)
			}
			checkSteps(t, c)
		}
	})

	t.Run("not even the smallest step fits: shrinking reads more agents, so it shrinks", func(t *testing.T) {
		// The chain in a body of 20 rows: no step fits, and the smaller boxes bring one more
		// agent whole into the body, so the tree takes them and the rest scrolls.
		counts := stepCounts(deep, 140, 20)
		l := layout(20)
		got := wholeBoxes(l, 20)
		if got != slices.Max(counts) || got <= counts[StepFull] {
			t.Errorf("whole boxes %d, by step %v", got, counts)
		}
		if l.ContentHeight <= 20 {
			t.Errorf("content height %d", l.ContentHeight)
		}
		checkSteps(t, l.Rows[0].Clusters[0])
		if vp := l.Viewport(20, "s", 0); !vp.Scrolling || !vp.Below {
			t.Errorf("viewport = %+v", vp)
		}
	})

	t.Run("not even the smallest step fits: shrinking reads no more, so the boxes stay", func(t *testing.T) {
		// Five sessions with one agent each in 100 columns: two clusters per row, three rows of
		// them and more content than the body however small the agents get. The session boxes
		// have a fixed height, so shrinking brings no further agent into the body and the full
		// boxes, which read better, stay.
		five := sessions(5, 1)
		counts := stepCounts(five, 100, 30)
		l := NewTreeLayout(five, 100, 30, SessionHeight, line)
		got := wholeBoxes(l, 30)
		if got != slices.Max(counts) || got != counts[StepFull] {
			t.Errorf("whole boxes %d, by step %v", got, counts)
		}
		if l.ContentHeight <= 30 {
			t.Errorf("content height %d", l.ContentHeight)
		}
		c := l.Rows[0].Clusters[0]
		if steps := clusterSteps(t, c); steps.At(0) != StepFull {
			t.Errorf("steps %v", steps)
		}
		checkSteps(t, c)
	})

	t.Run("enough session boxes overflow on their own and still scroll", func(t *testing.T) {
		// Six sessions with no agents, one per row: 47 rows of session boxes in 20 rows of body.
		l := NewTreeLayout(sessions(6, 0), 60, 20, SessionHeight, line)
		if len(l.Rows) != 6 || l.ContentHeight != 6*SessionHeight+5 {
			t.Fatalf("layout = %+v", l)
		}
		if vp := l.Viewport(20, "s0", 0); !vp.Scrolling || !vp.Below {
			t.Errorf("viewport = %+v", vp)
		}
	})

	t.Run("the step of a level stays no smaller than the one above it", func(t *testing.T) {
		// A coordinator with eight agents of its own: the width puts the first level one step
		// above the second, and the drop by height keeps that order.
		fan8 := TreeSession{Key: "s", Agents: []TreeAgent{nestedAgent("p")}}
		for i := range 8 {
			fan8.Agents[0].Children = append(fan8.Agents[0].Children, TreeAgent{Key: fmt.Sprint("c", i)})
		}
		one := []TreeSession{fan8}
		wide := NewTreeLayout(one, 100, UnlimitedRows, SessionHeight, line)
		if got := clusterSteps(t, wide.Rows[0].Clusters[0]); !slices.Equal(got, StepPlan{StepFull, StepNoModel}) {
			t.Fatalf("plan of the width = %v", got)
		}
		short := NewTreeLayout(one, 100, wide.ContentHeight-1, SessionHeight, line)
		c := short.Rows[0].Clusters[0]
		if got := clusterSteps(t, c); !slices.Equal(got, StepPlan{StepNoModel, StepCompact}) {
			t.Errorf("plan in %d rows = %v", wide.ContentHeight-1, got)
		}
		checkSteps(t, c)
		if short.ContentHeight > wide.ContentHeight-1 {
			t.Errorf("height %d over the %d rows", short.ContentHeight, wide.ContentHeight-1)
		}
	})
}

func TestSessionBoxHeight(t *testing.T) {
	if SessionBoxHeight(false) != SessionHeight || SessionBoxHeight(true) != SessionHeightMulti || SessionHeightMulti != SessionHeight+1 {
		t.Error("box heights")
	}
	h := SessionHeightMulti
	c := NewClusterLayout(session("s", 1), 140, h, StepPlan{StepFull}, line)
	if c.Stem.Y != h || c.Agents[0].Y != h+2 || c.Buses[0].Y != h+1 || c.Height != h+2+AgentHeight {
		t.Errorf("cluster = %+v", c)
	}
	if got := NewClusterLayout(session("t", 0), 140, h, StepPlan{StepFull}, line).Height; got != h {
		t.Errorf("empty cluster height = %d", got)
	}
	l := NewTreeLayout(sessions(4, 0), 140, UnlimitedRows, h, line)
	if l.SessionHeight != h || l.Rows[1].Y != h+1 || l.ContentHeight != 2*h+1 {
		t.Errorf("layout = %+v", l)
	}

	// The viewport keeps the whole taller box of the selection visible.
	six := NewTreeLayout(sessions(6, 0), 60, UnlimitedRows, h, line) // 6 rows of 8 + 5 gaps = 53
	first := six.Viewport(22, "s0", 0)
	if six.ContentHeight != 53 || first.Top != 0 || first.ViewRows != 20 {
		t.Errorf("first = %+v (%d)", first, six.ContentHeight)
	}
	// s2 starts at row 18 and ends at 26: the 20-row window ends at 26.
	if got := six.Viewport(22, "s2", first.Top).Top; got != 6 {
		t.Errorf("top = %d", got)
	}
	one := NewTreeLayout(sessions(1, 0), 60, UnlimitedRows, h, line)
	if vp := one.Viewport(20, "s0", 0); vp.Scrolling || vp.OffsetY != 6 {
		t.Errorf("one = %+v", vp)
	}
}

// nestedAgent is an agent with the given children.
func nestedAgent(key string, children ...TreeAgent) TreeAgent {
	return TreeAgent{Key: key, Children: children}
}

// wholeBoxes counts the agent boxes of the layout that stand whole, borders included, in the
// first rows rows of the content: the measure the tree maximizes when no step fits the body.
func wholeBoxes(l TreeLayout, rows int) int {
	count := 0
	for _, row := range l.Rows {
		for _, c := range row.Clusters {
			for _, a := range c.Agents {
				if c.Y+a.Y >= 0 && c.Y+a.Y+a.Size().Height <= rows {
					count++
				}
			}
		}
	}
	return count
}

// stepCounts is how many agent boxes stand whole in rows rows of body with every level of every
// session forced to each step, from the full box to the smallest one.
func stepCounts(s []TreeSession, columns, rows int) []int {
	counts := make([]int, SmallestStep+1)
	for step := StepFull; step <= SmallestStep; step++ {
		plans := make([]StepPlan, len(s))
		for i := range plans {
			plans[i] = StepPlan{step}
		}
		counts[step] = wholeBoxes(placeClusters(s, plans, columns, SessionHeight, line), rows)
	}
	return counts
}

// chain is a line of descent key0 -> key1 -> ... of the given length.
func chain(prefix string, depth int) TreeAgent {
	a := TreeAgent{Key: fmt.Sprintf("%s%d", prefix, depth-1)}
	for i := depth - 2; i >= 0; i-- {
		a = nestedAgent(fmt.Sprintf("%s%d", prefix, i), a)
	}
	return a
}

type rect struct {
	key                 string
	x, y, width, height int
}

func (r rect) overlaps(o rect) bool {
	return r.x < o.x+o.width && o.x < r.x+r.width && r.y < o.y+o.height && o.y < r.y+r.height
}

// boxes is every box of the cluster, the session one included (that one first).
func boxes(c ClusterLayout, boxHeight int) []rect {
	out := []rect{{key: c.Key, x: c.SessionLeft, width: SessionWidth, height: boxHeight}}
	for _, b := range c.Agents {
		out = append(out, rect{key: b.Key, x: b.X, y: b.Y, width: b.Size().Width, height: b.Size().Height})
	}
	return out
}

// allKeys is every key of a forest, in depth-first order.
func allKeys(agents []TreeAgent) []string {
	var out []string
	for _, a := range agents {
		out = append(out, a.Key)
		out = append(out, allKeys(a.Children)...)
	}
	return out
}

// checkCluster asserts the invariants: no box crosses another, every box fits in the cluster and
// the cluster in the width, and every agent shows up exactly once.
func checkCluster(t *testing.T, c ClusterLayout, maxWidth int, want []string) {
	t.Helper()
	all := boxes(c, SessionHeight)
	var keys []string
	for i, r := range all {
		if r.x < 0 || r.x+r.width > c.Width {
			t.Errorf("%s at %d..%d leaves the cluster of %d", r.key, r.x, r.x+r.width, c.Width)
		}
		if r.y+r.height > c.Height {
			t.Errorf("%s ends at row %d, cluster height %d", r.key, r.y+r.height, c.Height)
		}
		for _, o := range all[i+1:] {
			if r.overlaps(o) {
				t.Errorf("%+v crosses %+v", r, o)
			}
		}
		if i > 0 {
			keys = append(keys, r.key)
		}
	}
	if c.Width > maxWidth {
		t.Errorf("cluster width %d over the %d columns", c.Width, maxWidth)
	}
	slices.Sort(keys)
	wantSorted := slices.Sorted(slices.Values(want))
	if !slices.Equal(keys, wantSorted) {
		t.Errorf("agents = %v, want %v", keys, wantSorted)
	}
}

// extent is the columns the subtree of the agent spans, boxes only.
func extent(a TreeAgent, byKey map[string]AgentBox) (left, right int) {
	b := byKey[a.Key]
	left, right = b.X, b.X+b.Size().Width
	for _, child := range a.Children {
		l, r := extent(child, byKey)
		left, right = min(left, l), max(right, r)
	}
	return left, right
}

// blockExtent is the columns the whole block of children spans, in every row it wrapped into.
func blockExtent(children []TreeAgent, byKey map[string]AgentBox) (left, right int) {
	left, right = 1<<30, -1
	for _, child := range children {
		l, r := extent(child, byKey)
		left, right = min(left, l), max(right, r)
	}
	return left, right
}

// checkCentered asserts every box sits over the center of the block of its children, the rows it
// wrapped into included (the layout rounds to the column, so one column of slack), and that the
// session does too.
func checkCentered(t *testing.T, c ClusterLayout, agents []TreeAgent) {
	t.Helper()
	byKey := make(map[string]AgentBox, len(c.Agents))
	for _, b := range c.Agents {
		byKey[b.Key] = b
	}
	centered := func(name string, anchor int, children []TreeAgent) {
		if len(children) == 0 {
			return
		}
		left, right := blockExtent(children, byKey)
		if mid := (left + right) / 2; abs(anchor-mid) > 1 {
			t.Errorf("%s at %d is not over the center %d of its children", name, anchor, mid)
		}
	}
	centered(c.Key, c.Anchor, agents)
	var walk func([]TreeAgent)
	walk = func(list []TreeAgent) {
		for _, a := range list {
			b := byKey[a.Key]
			centered(a.Key, b.X+b.Size().Width/2, a.Children)
			walk(a.Children)
		}
	}
	walk(agents)
}

// clusterSteps is the plan the boxes of the cluster carry, read back level by level (StepFull
// for a level with no box).
func clusterSteps(t *testing.T, c ClusterLayout) StepPlan {
	t.Helper()
	levels := 0
	byDepth := map[int]AgentStep{}
	for _, b := range c.Agents {
		if step, ok := byDepth[b.Depth]; ok && step != b.Step {
			t.Errorf("%s at level %d has step %d, the level has %d", b.Key, b.Depth, b.Step, step)
		}
		byDepth[b.Depth] = b.Step
		levels = max(levels, b.Depth+1)
	}
	plan := make(StepPlan, levels)
	for d := range plan {
		plan[d] = byDepth[d]
	}
	return plan
}

// checkSteps asserts the plan of the cluster: the step of a level is never smaller than the one
// above it and never more than one step apart.
func checkSteps(t *testing.T, c ClusterLayout) {
	t.Helper()
	plan := clusterSteps(t, c)
	for d := 1; d < len(plan); d++ {
		if diff := plan[d] - plan[d-1]; diff < 0 || diff > 1 {
			t.Errorf("steps %v jump at level %d", plan, d)
		}
	}
}

// blockRowCount is the most rows a block of siblings of the cluster wrapped into: the boxes of a
// block all share the row of their top, so the rows of a parent are the distinct tops of its
// children.
func blockRowCount(c ClusterLayout, agents []TreeAgent) int {
	byKey := make(map[string]AgentBox, len(c.Agents))
	for _, b := range c.Agents {
		byKey[b.Key] = b
	}
	var count func(children []TreeAgent) int
	count = func(children []TreeAgent) int {
		tops := map[int]bool{}
		most := 0
		for _, child := range children {
			tops[byKey[child.Key].Y] = true
			most = max(most, count(child.Children))
		}
		return max(most, len(tops))
	}
	return count(agents)
}

func TestNestedClusterLayout(t *testing.T) {
	// coord launched two agents, one of which launched another; solo launched none.
	nested := TreeSession{Key: "s", Agents: []TreeAgent{
		nestedAgent("coord", nestedAgent("exec1", TreeAgent{Key: "leaf"}), TreeAgent{Key: "exec2"}),
		{Key: "solo"},
	}}

	t.Run("a parent sits centered over its children, each level a row lower", func(t *testing.T) {
		c := cluster(nested, 100)
		if steps := clusterSteps(t, c); steps.At(0) != StepFull {
			t.Fatalf("steps %v", steps)
		}
		checkCluster(t, c, 100, allKeys(nested.Agents))
		checkCentered(t, c, nested.Agents)
		checkSteps(t, c)
		// coord takes its two children side by side (54 columns), solo one box: 82 in all.
		if c.Width != 82 || c.Anchor != 41 {
			t.Errorf("width %d anchor %d", c.Width, c.Anchor)
		}
		rows := agentRows(c)
		if !slices.Equal(rowKeys(rows[0]), []string{"coord", "solo"}) ||
			!slices.Equal(rowKeys(rows[1]), []string{"exec1", "exec2"}) ||
			!slices.Equal(rowKeys(rows[2]), []string{"leaf"}) {
			t.Fatalf("rows = %v %v %v", rowKeys(rows[0]), rowKeys(rows[1]), rowKeys(rows[2]))
		}
		for i, row := range rows {
			if want := SessionHeight + 2 + i*(AgentHeight+1); row[0].Y != want {
				t.Errorf("row %d at %d, want %d", i, row[0].Y, want)
			}
			if row[0].Depth != i {
				t.Errorf("row %d has depth %d", i, row[0].Depth)
			}
		}
		// The bus of a single child is a straight line under its parent.
		leafBus := busAt(t, c, rows[2][0].Y-1)
		if leafBus.Text != vt || leafBus.Left != rows[2][0].X+AgentWidth/2 {
			t.Errorf("bus of the leaf = %+v", leafBus)
		}
		if len(c.Trunks) != 0 {
			t.Errorf("trunks = %+v", c.Trunks)
		}
	})

	t.Run("the invariants hold at every width", func(t *testing.T) {
		wide := TreeSession{Key: "s", Agents: []TreeAgent{
			nestedAgent("a", TreeAgent{Key: "a1"}, TreeAgent{Key: "a2"}, TreeAgent{Key: "a3"},
				nestedAgent("a4", TreeAgent{Key: "a4x"})),
			nestedAgent("b", TreeAgent{Key: "b1"}),
			{Key: "c"},
		}}
		for _, columns := range []int{40, 80, 100, 120, 160} {
			t.Run(fmt.Sprint(columns), func(t *testing.T) {
				c := cluster(wide, columns)
				checkCluster(t, c, columns, allKeys(wide.Agents))
				checkCentered(t, c, wide.Agents)
				checkSteps(t, c)
				if got := blockRowCount(c, wide.Agents); got > MaxBlockRows {
					t.Errorf("%d rows of siblings at steps %v", got, clusterSteps(t, c))
				}
			})
		}
	})

	t.Run("a wide fan wraps and stays inside", func(t *testing.T) {
		fan := TreeSession{Key: "s", Agents: []TreeAgent{nestedAgent("p")}}
		for i := range 20 {
			fan.Agents[0].Children = append(fan.Agents[0].Children, TreeAgent{Key: fmt.Sprintf("c%d", i)})
		}
		for _, columns := range []int{40, 80, 100, 120, 160} {
			t.Run(fmt.Sprint(columns), func(t *testing.T) {
				c := cluster(fan, columns)
				checkCluster(t, c, columns, allKeys(fan.Agents))
				checkCentered(t, c, fan.Agents)
				checkSteps(t, c)
				if len(c.Trunks) != 1 {
					t.Errorf("trunks = %+v", c.Trunks)
				}
				// Twenty agents only hold two rows from 160 columns on (ten boxes of the
				// smallest step a row); below that the smallest step wraps into as many rows as
				// it needs.
				rows, steps := blockRowCount(c, fan.Agents), clusterSteps(t, c)
				if columns >= 160 && rows > MaxBlockRows {
					t.Errorf("%d rows of siblings at steps %v", rows, steps)
				}
				if columns < 160 && steps.At(1) != SmallestStep {
					t.Errorf("steps %v", steps)
				}
			})
		}
	})

	t.Run("a chain eight deep keeps every box inside", func(t *testing.T) {
		deep := TreeSession{Key: "s", Agents: []TreeAgent{chain("d", 8)}}
		for _, columns := range []int{40, 80, 100, 160} {
			t.Run(fmt.Sprint(columns), func(t *testing.T) {
				c := cluster(deep, columns)
				checkCluster(t, c, columns, allKeys(deep.Agents))
				checkCentered(t, c, deep.Agents)
				checkSteps(t, c)
				if steps := clusterSteps(t, c); steps.At(0) != StepFull || c.Height != SessionHeight+1+8*(1+AgentHeight) {
					t.Errorf("steps %v height %d", steps, c.Height)
				}
			})
		}
	})

	t.Run("every agent shows once in a mixed forest at every width", func(t *testing.T) {
		mixed := TreeSession{Key: "s", Agents: []TreeAgent{
			nestedAgent("p", TreeAgent{Key: "p1"}, TreeAgent{Key: "p2"}, TreeAgent{Key: "p3"},
				TreeAgent{Key: "p4"}, TreeAgent{Key: "p5"}),
			{Key: "q"}, {Key: "r"}, {Key: "t"},
		}}
		for _, columns := range []int{40, 60, 80, 100, 120, 160, 200} {
			c := cluster(mixed, columns)
			checkCluster(t, c, columns, allKeys(mixed.Agents))
			checkCentered(t, c, mixed.Agents)
			checkSteps(t, c)
		}
	})
}
