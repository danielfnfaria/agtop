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
	return NewClusterLayout(s, maxWidth, SessionFull.Size(false), SessionFull, PickStepPlan(s, maxWidth), line)
}

// clusterStep forces one step for every level, to exercise the wrapping of a tree that does not
// fit.
func clusterStep(s TreeSession, maxWidth int, step AgentStep) ClusterLayout {
	return NewClusterLayout(s, maxWidth, SessionFull.Size(false), SessionFull, StepPlan{step}, line)
}

// layoutOf lays the sessions out with no height limit, so only the width picks the steps.
func layoutOf(s []TreeSession, columns int) TreeLayout {
	return NewTreeLayout(s, columns, UnlimitedRows, false, line)
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
		checkBuses(t, c, session("s", 5).Agents)
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
		if vp.Scrolling || vp.OffsetY != (40-SessionHeight)/2 || vp.Top != 0 {
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
	l := layoutOf(sessions(6, 0), 60) // 6 rows of 4 + 5 gaps = 29 rows

	if vp := l.Viewport(UnlimitedRows, "s0", 0); vp.Scrolling || vp.OffsetY != 0 || vp.Top != 0 {
		t.Errorf("unlimited = %+v", vp)
	}
	if l.ContentHeight != 29 {
		t.Fatalf("content height = %d", l.ContentHeight)
	}
	first := l.Viewport(12, "s0", 0)
	if want := (TreeViewport{Top: 0, ViewRows: 10, Scrolling: true, Below: true}); first != want {
		t.Errorf("first = %+v", first)
	}
	// s2 starts at row 10 and ends at 14: the 10-row window ends at 14.
	third := l.Viewport(12, "s2", first.Top)
	if third.Top != 4 || !third.Above || !third.Below {
		t.Errorf("third = %+v", third)
	}
	// s1 (5..9) is already visible.
	if back := l.Viewport(12, "s1", third.Top); back.Top != 4 {
		t.Errorf("back = %+v", back)
	}
	last := l.Viewport(12, "s5", 4)
	if last.Top != 19 || !last.Above || last.Below {
		t.Errorf("last = %+v", last)
	}
}

// TestTreeLayoutFitsTheHeight covers the second dimension of the step: a tree that fits the
// width but not the body shrinks until it does, and keeps the readable boxes when no step fits.
func TestTreeLayoutFitsTheHeight(t *testing.T) {
	// A chain four deep is one box wide, so the width alone leaves it at the full step. Its
	// height is the session box, the stem and a row per level: 29 rows at the full step and at
	// 20 columns of box, 25 at 14 and 21 at 13.
	deep := []TreeSession{{Key: "s", Agents: []TreeAgent{chain("d", 4)}}}
	layout := func(rows int) TreeLayout { return NewTreeLayout(deep, 140, rows, false, line) }

	t.Run("no limit: the width alone picks the step", func(t *testing.T) {
		l := layout(UnlimitedRows)
		if steps := clusterSteps(t, l.Rows[0].Clusters[0]); steps.At(0) != StepFull || l.ContentHeight != 29 {
			t.Errorf("steps %v height %d", steps, l.ContentHeight)
		}
	})

	t.Run("taller than the body: one step down at a time", func(t *testing.T) {
		tests := []struct {
			rows   int
			want   AgentStep
			height int
		}{
			{40, StepFull, 29}, {29, StepFull, 29}, {28, StepCompact, 25}, {25, StepCompact, 25},
			{24, StepTiny, 21}, {21, StepTiny, 21},
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

	t.Run("not even the smallest boxes fit: the tree reads as many agents as it can", func(t *testing.T) {
		// The chain in a body of 12 rows: no candidate fits, and the smallest session box brings
		// more of the chain into those rows than any agent step does on its own, so the tree
		// takes it and the rest scrolls.
		counts := stepCounts(deep, 140, 12)
		l := layout(12)
		got := wholeBoxes(l, 12)
		if got <= slices.Max(counts) {
			t.Errorf("whole boxes %d, by agent step with the full session box %v", got, counts)
		}
		if l.ContentHeight <= 12 {
			t.Errorf("content height %d", l.ContentHeight)
		}
		checkSteps(t, l.Rows[0].Clusters[0])
		if vp := l.Viewport(12, "s", 0); !vp.Scrolling || !vp.Below {
			t.Errorf("viewport = %+v", vp)
		}
	})

	t.Run("the session boxes give back the rows the agents had to lose", func(t *testing.T) {
		// Five sessions with one agent each in 100 columns: three rows of full session boxes
		// overflow a body of 24 rows, and no agent step saves it. The session boxes shrink
		// instead, which fits one more cluster per row, and the agent boxes grow back to the
		// step the width allows (see regrow).
		five := sessions(5, 1)
		l := NewTreeLayout(five, 100, 24, false, line)
		if l.ContentHeight > 24 {
			t.Errorf("content height %d over the 24 rows", l.ContentHeight)
		}
		if got := wholeBoxes(l, 24); got != 5 {
			t.Errorf("whole agent boxes = %d, want the 5 of them", got)
		}
		c := l.Rows[0].Clusters[0]
		if c.SessionStep != SessionCompact || c.SessionBox != SessionCompact.Size(false) {
			t.Errorf("session step %d box %+v", c.SessionStep, c.SessionBox)
		}
		if steps := clusterSteps(t, c); steps.At(0) != StepFull {
			t.Errorf("agent steps %v, want the full box back", steps)
		}
		checkCluster(t, c, 100, []string{"s0-a0"})
	})

	t.Run("the session boxes alone make room for sessions with no agents", func(t *testing.T) {
		// Nine sessions with no agents in 60 columns: 44 rows of full boxes in a body of 20.
		// They have no tree under them, so they shrink first and end up two to a row.
		l := NewTreeLayout(sessions(9, 0), 60, 20, false, line)
		if l.ContentHeight > 20 || len(l.Rows) != 5 {
			t.Fatalf("content height %d in %d rows", l.ContentHeight, len(l.Rows))
		}
		for _, row := range l.Rows {
			for _, c := range row.Clusters {
				if c.SessionStep != SessionCompact || c.Height != SessionCompact.Size(false).Height {
					t.Errorf("%s: step %d height %d", c.Key, c.SessionStep, c.Height)
				}
			}
		}
		if vp := l.Viewport(20, "s0", 0); vp.Scrolling {
			t.Errorf("viewport = %+v", vp)
		}
	})

	t.Run("enough sessions overflow the body whatever the boxes and still scroll", func(t *testing.T) {
		// Six sessions with an agent each in a body of 10 rows: not even the smallest boxes fit.
		l := NewTreeLayout(sessions(6, 1), 60, 10, false, line)
		if l.ContentHeight <= 10 {
			t.Fatalf("content height %d", l.ContentHeight)
		}
		if vp := l.Viewport(10, "s0", 0); !vp.Scrolling || !vp.Below {
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
		wide := NewTreeLayout(one, 100, UnlimitedRows, false, line)
		if got := clusterSteps(t, wide.Rows[0].Clusters[0]); !slices.Equal(got, StepPlan{StepFull, StepNoModel}) {
			t.Fatalf("plan of the width = %v", got)
		}
		short := NewTreeLayout(one, 100, wide.ContentHeight-1, false, line)
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
	// The label of the profile is a field of the content line, so watching a second profile
	// costs no height; only the smallest step, which has no content line of its own, takes a
	// line for it.
	if SessionFull.Size(false).Height != SessionHeight || SessionFull.Size(true).Height != SessionHeight {
		t.Errorf("box heights %d / %d", SessionFull.Size(false).Height, SessionFull.Size(true).Height)
	}
	if got := SessionTiny.Size(true).Height; got != SessionTiny.Size(false).Height+1 {
		t.Errorf("the smallest box with profiles = %d", got)
	}

	h := SessionHeight
	c := NewClusterLayout(session("s", 1), 140, SessionFull.Size(true), SessionFull, StepPlan{StepFull}, line)
	if c.Stem.Y != h || c.Agents[0].Y != h+2 || c.Buses[0].Y != h+1 || c.Height != h+2+AgentHeight {
		t.Errorf("cluster = %+v", c)
	}
	if got := NewClusterLayout(session("t", 0), 140, SessionFull.Size(true), SessionFull, StepPlan{StepFull}, line).Height; got != h {
		t.Errorf("empty cluster height = %d", got)
	}
	l := NewTreeLayout(sessions(4, 0), 140, UnlimitedRows, true, line)
	if l.Rows[1].Y != h+1 || l.ContentHeight != 2*h+1 {
		t.Errorf("layout = %+v", l)
	}

	// The viewport keeps the whole box of the selection visible.
	six := NewTreeLayout(sessions(6, 0), 60, UnlimitedRows, true, line) // 6 rows of 4 + 5 gaps = 29
	first := six.Viewport(22, "s0", 0)
	if six.ContentHeight != 6*h+5 || first.Top != 0 || first.ViewRows != 20 {
		t.Errorf("first = %+v (%d)", first, six.ContentHeight)
	}
	// s5 starts at row 25 and ends at 29: the 20-row window ends at 29.
	if got := six.Viewport(22, "s5", first.Top).Top; got != 9 {
		t.Errorf("top = %d", got)
	}
	one := NewTreeLayout(sessions(1, 0), 60, UnlimitedRows, true, line)
	if vp := one.Viewport(20, "s0", 0); vp.Scrolling || vp.OffsetY != (20-h)/2 {
		t.Errorf("one = %+v", vp)
	}
}

// fanSession is a session with roots first-level agents, each with children of its own.
func fanSession(key string, roots, children int) TreeSession {
	s := TreeSession{Key: key}
	for i := range roots {
		root := nestedAgent(fmt.Sprintf("%s-r%d", key, i))
		for j := range children {
			root.Children = append(root.Children, TreeAgent{Key: fmt.Sprintf("%s-r%dc%d", key, i, j)})
		}
		s.Agents = append(s.Agents, root)
	}
	return s
}

// TestSessionSteps covers the steps of the session box: their sizes, when they are taken and
// what the rest of the layout makes of boxes that no longer share one height.
func TestSessionSteps(t *testing.T) {
	t.Run("the steps only get smaller, and a profile adds a line to each", func(t *testing.T) {
		if SessionFull.Size(false) != (SessionSize{SessionWidth, SessionHeight}) ||
			SessionFull.Size(true) != SessionFull.Size(false) {
			t.Errorf("full box = %+v / %+v", SessionFull.Size(false), SessionFull.Size(true))
		}
		for step := SessionFull; step < SmallestSessionStep; step++ {
			this, next := step.Size(false), (step + 1).Size(false)
			if next.Width > this.Width || next.Height >= this.Height {
				t.Errorf("step %d %+v is not smaller than step %d %+v", step+1, next, step, this)
			}
			if multi := step.Size(true); multi.Height != this.Height || multi.Width != this.Width {
				t.Errorf("step %d with profiles = %+v", step, multi)
			}
		}
		if SmallestSessionStep.Size(false).Height < 2 {
			t.Error("the smallest box has no room for its two borders")
		}
		if got := SessionStep(99).Size(false); got != SessionFull.Size(false) {
			t.Errorf("step out of range = %+v", got)
		}
	})

	t.Run("the agent boxes are exhausted before a session box shrinks", func(t *testing.T) {
		// The chain four deep (see TestTreeLayoutFitsTheHeight) at 21 rows: the agents are at
		// the smallest step and the session box is still the full one.
		deep := []TreeSession{{Key: "s", Agents: []TreeAgent{chain("d", 4)}}}
		c := NewTreeLayout(deep, 140, 21, false, line).Rows[0].Clusters[0]
		if steps := clusterSteps(t, c); steps.At(0) != SmallestStep || c.SessionStep != SessionFull {
			t.Errorf("agent steps %v, session step %d", clusterSteps(t, c), c.SessionStep)
		}
		// One row less and the session box is the only thing left to give it.
		if got := NewTreeLayout(deep, 140, 20, false, line).Rows[0].Clusters[0]; got.SessionStep == SessionFull {
			t.Errorf("session step %d at 20 rows", got.SessionStep)
		}
	})

	t.Run("a session with no active agents shrinks one step ahead", func(t *testing.T) {
		// An idle session and one with a tree of twelve agents in a body of 24 rows: the idle
		// box, with no tree of its own, is a step ahead of the busy one all the way down.
		idle := []TreeSession{{Key: "idle"}, fanSession("busy", 3, 3)}
		l := NewTreeLayout(idle, 100, 24, false, line)
		if l.ContentHeight > 24 {
			t.Fatalf("content height %d over the 24 rows", l.ContentHeight)
		}
		steps := map[string]SessionStep{}
		for _, row := range l.Rows {
			for _, c := range row.Clusters {
				steps[c.Key] = c.SessionStep
			}
		}
		if steps["idle"] != SessionTiny || steps["busy"] != SessionCompact {
			t.Errorf("steps = %v, want the idle box one step ahead of the busy one", steps)
		}
	})

	t.Run("the round of the demotion puts the idle sessions a step ahead", func(t *testing.T) {
		all := []TreeSession{{Key: "idle"}, session("busy", 1)}
		want := [][]SessionStep{
			{SessionFull, SessionFull},
			{SessionCompact, SessionFull},
			{SessionTiny, SessionCompact},
			{SessionTiny, SessionTiny},
		}
		for round, want := range want {
			if got := sessionSteps(all, round); !slices.Equal(got, want) {
				t.Errorf("round %d = %v, want %v", round, got, want)
			}
		}
	})

	t.Run("no box leaves its cluster at any step", func(t *testing.T) {
		s := fanSession("s", 3, 2)
		for step := SessionFull; step <= SmallestSessionStep; step++ {
			for _, multi := range []bool{false, true} {
				box := step.Size(multi)
				c := NewClusterLayout(s, 100, box, step, PickStepPlan(s, 100), line)
				if c.SessionBox != box || c.SessionStep != step {
					t.Errorf("step %d: box %+v", step, c.SessionBox)
				}
				if c.SessionLeft < 0 || c.SessionLeft+box.Width > c.Width {
					t.Errorf("step %d: session box at %d..%d of %d", step, c.SessionLeft, c.SessionLeft+box.Width, c.Width)
				}
				checkCluster(t, c, 100, allKeys(s.Agents))
			}
		}
	})

	t.Run("the viewport takes the height of the box of the selection", func(t *testing.T) {
		// Two clusters of different steps: the window has to end at the bottom of the box of the
		// selected session, whichever height that one has.
		l := TreeLayout{Columns: 40, ContentHeight: 20, Rows: []ClusterRow{
			{Y: 0, Height: 7, Clusters: []ClusterLayout{
				{Key: "a", Y: 0, Width: SessionWidth, Height: 7, SessionStep: SessionFull, SessionBox: SessionFull.Size(false)}}},
			{Y: 8, Height: 3, Clusters: []ClusterLayout{
				{Key: "b", Y: 8, Width: 22, Height: 3, SessionStep: SessionTiny, SessionBox: SessionTiny.Size(false)}}},
		}}
		// A body of 10 rows leaves a window of 8: the tiny box of b ends at row 10, so the
		// window starts at 2 (at the height of a full box it would be 4).
		if got := l.Viewport(10, "b", 0).Top; got != 2 {
			t.Errorf("top = %d, want 2", got)
		}
		if got := l.Viewport(10, "a", 5).Top; got != 0 {
			t.Errorf("top = %d, want 0", got)
		}
		if grid := l.NavGrid(); len(grid) != 2 || grid[0][0].Key != "a" || grid[1][0].Key != "b" {
			t.Errorf("grid = %+v", grid)
		}
	})

	t.Run("the navigation grid keeps every session of a tree of mixed steps", func(t *testing.T) {
		all := []TreeSession{{Key: "i0"}, session("b0", 2), {Key: "i1"}, session("b1", 2), {Key: "i2"}}
		want := map[string][]string{}
		for _, s := range all {
			want[s.Key] = allKeys(s.Agents)
		}
		l := NewTreeLayout(all, 100, 24, false, line)
		var keys []string
		for i, row := range l.NavGrid() {
			if len(row) == 0 {
				t.Errorf("row %d is empty", i)
			}
			for _, cell := range row {
				keys = append(keys, cell.Key)
			}
		}
		slices.Sort(keys)
		if want := []string{"b0", "b1", "i0", "i1", "i2"}; !slices.Equal(keys, want) {
			t.Errorf("grid keys = %v, want %v", keys, want)
		}
		for _, row := range l.Rows {
			for _, c := range row.Clusters {
				if c.Y != row.Y || c.Height > row.Height {
					t.Errorf("%s at row %d: y %d height %d of %d", c.Key, row.Y, c.Y, c.Height, row.Height)
				}
				checkCluster(t, c, 100, want[c.Key])
			}
		}
	})
}

// TestSessionStepsFitMoreAgents is the acceptance case: an idle session and one with three roots
// of three agents each in 100x30. A taller session box made the tree taller than the body, which
// cut the deepest agents off; the box of today fits the whole tree in the same screen.
func TestSessionStepsFitMoreAgents(t *testing.T) {
	// taller is how many rows over the box of today it takes for the body to cut agents off.
	const taller = 4
	all := []TreeSession{{Key: "idle"}, fanSession("busy", 3, 3)}
	l := NewTreeLayout(all, 100, 30, false, line)
	after := wholeBoxes(l, 30)
	before := wholeBoxesTallerSession(l, 30, taller)
	if after <= before {
		t.Errorf("agents whole in the body: %d with the box of %d rows, %d with one of %d",
			after, SessionHeight, before, SessionHeight+taller)
	}
	if want := len(allKeys(all[1].Agents)); after != want || l.ContentHeight > 30 {
		t.Errorf("agents whole in the body = %d of %d, content height %d", after, want, l.ContentHeight)
	}
	for _, row := range l.Rows {
		for _, c := range row.Clusters {
			if c.SessionStep != SessionFull {
				t.Errorf("%s: step %d, the tree fits with the full boxes", c.Key, c.SessionStep)
			}
		}
	}
}

// wholeBoxesTallerSession is how many agent boxes would stand whole in rows rows of body if every
// session box were extra rows taller. The clusters group into the same rows (the width of the box
// does not change), so every row of clusters starts extra rows lower than the one before it, and
// the agents of a row carry the boxes of the rows above plus their own.
func wholeBoxesTallerSession(l TreeLayout, rows, extra int) int {
	count := 0
	for i, row := range l.Rows {
		for _, c := range row.Clusters {
			for _, a := range c.Agents {
				if c.Y+a.Y+extra*(i+1)+a.Size().Height <= rows {
					count++
				}
			}
		}
	}
	return count
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
		counts[step] = wholeBoxes(placeClusters(s, plans, columns, sessionSteps(s, 0), false, line), rows)
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

// boxes is every box of the cluster, the session one included (that one first), each at the size
// of its own step.
func boxes(c ClusterLayout) []rect {
	out := []rect{{key: c.Key, x: c.SessionLeft, width: c.SessionBox.Width, height: c.SessionBox.Height}}
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
	all := boxes(c)
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

// downLinks are the line characters with a link going down.
var downLinks = []string{vt, dr, dl, vr, vl, hd, cx}

// busLinks are the columns of a connector that rise and the ones that come down.
func busLinks(b Connector) (ups, downs []int) {
	for i, ch := range []rune(b.Text) {
		if slices.Contains(upLinks, string(ch)) {
			ups = append(ups, b.Left+i)
		}
		if slices.Contains(downLinks, string(ch)) {
			downs = append(downs, b.Left+i)
		}
	}
	return ups, downs
}

// checkBuses asserts the barring of every parent that launched agents, the session included and
// whatever the depth: each row its children wrapped into has a connector of its own, right above
// them, that rises exactly at the stem of the parent and comes down exactly at the center of
// every child of that row. The rows a wrapped block continued into rise at the trunk instead, and
// every row but the last of such a block comes down at the trunk too, to carry it on. The first
// row hangs right under the parent (one row lower for the session, which has a stem of its own).
func checkBuses(t *testing.T, c ClusterLayout, agents []TreeAgent) {
	t.Helper()
	byKey := make(map[string]AgentBox, len(c.Agents))
	for _, b := range c.Agents {
		byKey[b.Key] = b
	}
	// Siblings share the rows of their children, so a row may carry a bus per parent: the one of
	// a block is the only one that spans every child of the block.
	busOver := func(y int, centers []int) (Connector, bool) {
		var found []Connector
		for _, b := range c.Buses {
			if b.Y != y {
				continue
			}
			span := len([]rune(b.Text))
			covers := true
			for _, x := range centers {
				covers = covers && x >= b.Left && x < b.Left+span
			}
			if covers {
				found = append(found, b)
			}
		}
		if len(found) != 1 {
			return Connector{}, false
		}
		return found[0], true
	}
	var check func(name string, anchor, firstBusY int, children []TreeAgent)
	check = func(name string, anchor, firstBusY int, children []TreeAgent) {
		defer func() {
			for _, child := range children {
				b := byKey[child.Key]
				box := b.Size()
				check(child.Key, b.X+box.Width/2, b.Y+box.Height, child.Children)
			}
		}()
		if len(children) == 0 {
			return
		}
		// The children of a row share the row of their top, so their tops are the rows the block
		// wrapped into, and the bus of a row sits right above it.
		var tops []int
		centers := map[int][]int{}
		for _, child := range children {
			b := byKey[child.Key]
			if _, seen := centers[b.Y]; !seen {
				tops = append(tops, b.Y)
			}
			centers[b.Y] = append(centers[b.Y], b.X+b.Size().Width/2)
		}
		slices.Sort(tops)
		wrapped, trunkX := len(tops) > 1, 0
		if wrapped {
			first, ok := busOver(tops[0]-1, centers[tops[0]])
			if !ok {
				t.Fatalf("%s: no bus of its own above its first row of children (row %d)", name, tops[0]-1)
			}
			// The trunk runs left of every child and of the stem, so it opens the first bus.
			trunkX = first.Left
		}
		for i, top := range tops {
			bus, ok := busOver(top-1, centers[top])
			if !ok {
				t.Errorf("%s: no bus of its own above the row of children at %d", name, top)
				continue
			}
			if i == 0 && bus.Y != firstBusY {
				t.Errorf("%s: its first bus is on row %d, want %d", name, bus.Y, firstBusY)
			}
			wantUps := []int{anchor}
			if i > 0 {
				wantUps = []int{trunkX}
			}
			wantDowns := slices.Sorted(slices.Values(centers[top]))
			if wrapped && i < len(tops)-1 {
				wantDowns = append([]int{trunkX}, wantDowns...)
			}
			ups, downs := busLinks(bus)
			if !slices.Equal(ups, wantUps) {
				t.Errorf("%s: the bus on row %d rises at %v, want %v", name, bus.Y, ups, wantUps)
			}
			if !slices.Equal(downs, wantDowns) {
				t.Errorf("%s: the bus on row %d comes down at %v, want %v", name, bus.Y, downs, wantDowns)
			}
		}
	}
	if len(agents) == 0 {
		if c.Stem != nil {
			t.Errorf("a session with no agents has the stem %+v", *c.Stem)
		}
		return
	}
	if c.Stem == nil || c.Stem.Y != c.SessionBox.Height || c.Stem.Left != c.Anchor {
		t.Errorf("stem = %+v, box of %d rows anchored at %d", c.Stem, c.SessionBox.Height, c.Anchor)
	}
	check(c.Key, c.Anchor, c.SessionBox.Height+1, agents)
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

// TestBusesBarEveryParent runs the barring invariant over every shape of tree the layout draws,
// at every width and with the steps forced so the blocks wrap: the tests that pin a bus character
// by character only cover parents whose children are leaves, and a parent with grandchildren
// shares the row of its siblings' children.
func TestBusesBarEveryParent(t *testing.T) {
	nested := TreeSession{Key: "s", Agents: []TreeAgent{
		nestedAgent("coord", nestedAgent("exec1", TreeAgent{Key: "leaf"}), TreeAgent{Key: "exec2"}),
		nestedAgent("other", TreeAgent{Key: "o1"}, TreeAgent{Key: "o2"}, TreeAgent{Key: "o3"},
			TreeAgent{Key: "o4"}, TreeAgent{Key: "o5"}),
		{Key: "solo"},
	}}
	trees := []struct {
		name string
		tree TreeSession
	}{
		{"no agents", session("s", 0)},
		{"one agent", session("s", 1)},
		{"five agents", session("s", 5)},
		{"twenty agents", session("s", 20)},
		{"three roots of three", fanSession("s", 3, 3)},
		{"eight roots of eight", fanSession("s", 8, 8)},
		{"a chain eight deep", TreeSession{Key: "s", Agents: []TreeAgent{chain("d", 8)}}},
		{"nested blocks of their own", nested},
	}
	for _, tt := range trees {
		t.Run(tt.name, func(t *testing.T) {
			for _, columns := range []int{40, 60, 80, 100, 120, 160, 200} {
				checkBuses(t, cluster(tt.tree, columns), tt.tree.Agents)
				for step := StepFull; step <= SmallestStep; step++ {
					checkBuses(t, clusterStep(tt.tree, columns, step), tt.tree.Agents)
				}
			}
		})
	}
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
		checkBuses(t, c, nested.Agents)
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
				checkBuses(t, c, wide.Agents)
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
				checkBuses(t, c, fan.Agents)
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
				checkBuses(t, c, deep.Agents)
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
			checkBuses(t, c, mixed.Agents)
			checkSteps(t, c)
		}
	})
}
