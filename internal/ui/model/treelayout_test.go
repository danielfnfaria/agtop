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
		s.AgentKeys = append(s.AgentKeys, fmt.Sprintf("%s-a%d", key, i))
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

func cluster(s TreeSession, maxWidth int) ClusterLayout {
	return NewClusterLayout(s, maxWidth, SessionHeight, line)
}

func layoutOf(s []TreeSession, columns int) TreeLayout {
	return NewTreeLayout(s, columns, SessionHeight, line)
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

func TestClusterLayout(t *testing.T) {
	t.Run("no active agents: only the box", func(t *testing.T) {
		c := cluster(session("s", 0), 140)
		if c.Width != SessionWidth || c.Height != SessionHeight || c.SessionLeft != 0 || c.Stem != nil || len(c.Lines) != 0 {
			t.Errorf("got %+v", c)
		}
	})

	t.Run("one agent: straight stem and connector", func(t *testing.T) {
		c := cluster(session("s", 1), 140)
		if c.Width != SessionWidth || c.Anchor != 17 {
			t.Errorf("width %d anchor %d", c.Width, c.Anchor)
		}
		if want := (Connector{Y: SessionHeight, Left: 17, Text: vt}); *c.Stem != want {
			t.Errorf("stem = %+v", *c.Stem)
		}
		l := c.Lines[0]
		if len(c.Lines) != 1 || l.Y != SessionHeight+2 || l.Left != 4 || !slices.Equal(l.Keys, []string{"s-a0"}) {
			t.Errorf("line = %+v", l)
		}
		if want := (Connector{Y: SessionHeight + 1, Left: 17, Text: vt}); l.Connector != want {
			t.Errorf("connector = %+v", l.Connector)
		}
		if c.Height != SessionHeight+2+AgentHeight {
			t.Errorf("height = %d", c.Height)
		}
	})

	t.Run("two agents", func(t *testing.T) {
		c := cluster(session("s", 2), 140)
		if c.Width != 54 || c.SessionLeft != 10 || c.Anchor != 27 {
			t.Errorf("width %d left %d anchor %d", c.Width, c.SessionLeft, c.Anchor)
		}
		want := Connector{Y: SessionHeight + 1, Left: 13, Text: dr + rep(hz, 13) + hu + rep(hz, 13) + dl}
		if c.Lines[0].Connector != want {
			t.Errorf("connector = %+v", c.Lines[0].Connector)
		}
	})

	t.Run("three agents: cross under the session", func(t *testing.T) {
		c := cluster(session("s", 3), 140)
		if c.Width != 82 || c.Lines[0].Connector.Text != dr+rep(hz, 27)+cx+rep(hz, 27)+dl {
			t.Errorf("width %d connector %q", c.Width, c.Lines[0].Connector.Text)
		}
	})

	t.Run("agents that do not fit wrap into rows joined by a trunk", func(t *testing.T) {
		// 110 columns: 4 agents fit, but with the trunk (2 columns) 3 per line; 5 become 3 + 2.
		if AgentsPerLine(110) != 4 || AgentsPerLine(108) != 3 {
			t.Fatalf("agents per line: %d %d", AgentsPerLine(110), AgentsPerLine(108))
		}
		c := cluster(session("s", 5), 110)
		if c.Width != 84 || c.Anchor != 43 || c.SessionLeft != 26 {
			t.Errorf("width %d anchor %d left %d", c.Width, c.Anchor, c.SessionLeft)
		}
		if got := []int{len(c.Lines[0].Keys), len(c.Lines[1].Keys)}; !slices.Equal(got, []int{3, 2}) {
			t.Fatalf("rows = %v", got)
		}
		first, second := c.Lines[0], c.Lines[1]
		if first.Y != SessionHeight+2 || second.Connector.Y != first.Y+AgentHeight || second.Y != second.Connector.Y+1 {
			t.Errorf("rows at %d, %d (bus %d)", first.Y, second.Y, second.Connector.Y)
		}
		if first.Left != 2 || !first.HasTrunk || first.Trunk != 0 {
			t.Errorf("first = %+v", first)
		}
		wantFirst := Connector{Y: SessionHeight + 1, Left: 0, Text: dr + rep(hz, 14) + hd + rep(hz, 27) + cx + rep(hz, 27) + dl}
		if first.Connector != wantFirst {
			t.Errorf("first connector = %+v", first.Connector)
		}
		if second.Left != 16 || second.HasTrunk {
			t.Errorf("second = %+v", second)
		}
		if second.Connector.Left != 0 || second.Connector.Text != ur+rep(hz, 28)+hd+rep(hz, 27)+dl {
			t.Errorf("second connector = %+v", second.Connector)
		}
		if c.Height != second.Y+AgentHeight {
			t.Errorf("height = %d", c.Height)
		}
	})

	t.Run("seven agents in three rows: continuous trunk, nothing rises between the agents", func(t *testing.T) {
		c := cluster(session("s", 7), 110)
		var counts, lefts, connLefts []int
		var trunks []string
		var firsts []string
		for _, l := range c.Lines {
			counts = append(counts, len(l.Keys))
			lefts = append(lefts, l.Left)
			connLefts = append(connLefts, l.Connector.Left)
			firsts = append(firsts, string([]rune(l.Connector.Text)[:1]))
			trunks = append(trunks, fmt.Sprint(l.HasTrunk, l.Trunk))
		}
		if !slices.Equal(counts, []int{3, 3, 1}) || !slices.Equal(lefts, []int{2, 2, 30}) ||
			!slices.Equal(connLefts, []int{0, 0, 0}) || !slices.Equal(firsts, []string{dr, vr, ur}) ||
			!slices.Equal(trunks, []string{"true 0", "true 0", "false 0"}) {
			t.Fatalf("rows %v lefts %v connLefts %v firsts %v trunks %v", counts, lefts, connLefts, firsts, trunks)
		}
		if got := c.Lines[1].Connector.Text; got != vr+rep(hz, 14)+hd+rep(hz, 27)+hd+rep(hz, 27)+dl {
			t.Errorf("second connector = %q", got)
		}
		if got := c.Lines[2].Connector.Text; got != ur+rep(hz, 42)+dl {
			t.Errorf("third connector = %q", got)
		}
		for i, l := range c.Lines {
			var upAt []int
			for k, ch := range []rune(l.Connector.Text) {
				if slices.Contains(upLinks, string(ch)) {
					upAt = append(upAt, l.Connector.Left+k)
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
		if c.Width > 110 {
			t.Errorf("width %d", c.Width)
		}
	})

	t.Run("narrow: one agent per row, hanging from the trunk", func(t *testing.T) {
		c := cluster(session("s", 3), 40)
		if c.Width != SessionWidth || c.Anchor != 17 {
			t.Errorf("width %d anchor %d", c.Width, c.Anchor)
		}
		var lefts, trunks []int
		var conns []Connector
		for _, l := range c.Lines {
			lefts = append(lefts, l.Left)
			conns = append(conns, l.Connector)
			trunks = append(trunks, map[bool]int{true: l.Trunk, false: -1}[l.HasTrunk])
		}
		if !slices.Equal(lefts, []int{4, 4, 4}) || !slices.Equal(trunks, []int{2, 2, -1}) {
			t.Errorf("lefts %v trunks %v", lefts, trunks)
		}
		want := []Connector{
			{Y: SessionHeight + 1, Left: 2, Text: dr + rep(hz, 14) + vl},
			{Y: SessionHeight + 1 + 1 + AgentHeight, Left: 2, Text: vr + rep(hz, 14) + dl},
			{Y: SessionHeight + 1 + 2*(1+AgentHeight), Left: 2, Text: ur + rep(hz, 14) + dl},
		}
		if !reflect.DeepEqual(conns, want) {
			t.Errorf("connectors = %+v", conns)
		}
		if c.Height != SessionHeight+1+3*(1+AgentHeight) {
			t.Errorf("height = %d", c.Height)
		}
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

func TestSessionBoxHeight(t *testing.T) {
	if SessionBoxHeight(false) != SessionHeight || SessionBoxHeight(true) != SessionHeightMulti || SessionHeightMulti != SessionHeight+1 {
		t.Error("box heights")
	}
	h := SessionHeightMulti
	c := NewClusterLayout(session("s", 1), 140, h, line)
	if c.Stem.Y != h || c.Lines[0].Y != h+2 || c.Lines[0].Connector.Y != h+1 || c.Height != h+2+AgentHeight {
		t.Errorf("cluster = %+v", c)
	}
	if got := NewClusterLayout(session("t", 0), 140, h, line).Height; got != h {
		t.Errorf("empty cluster height = %d", got)
	}
	l := NewTreeLayout(sessions(4, 0), 140, h, line)
	if l.SessionHeight != h || l.Rows[1].Y != h+1 || l.ContentHeight != 2*h+1 {
		t.Errorf("layout = %+v", l)
	}

	// The viewport keeps the whole taller box of the selection visible.
	six := NewTreeLayout(sessions(6, 0), 60, h, line) // 6 rows of 8 + 5 gaps = 53
	first := six.Viewport(22, "s0", 0)
	if six.ContentHeight != 53 || first.Top != 0 || first.ViewRows != 20 {
		t.Errorf("first = %+v (%d)", first, six.ContentHeight)
	}
	// s2 starts at row 18 and ends at 26: the 20-row window ends at 26.
	if got := six.Viewport(22, "s2", first.Top).Top; got != 6 {
		t.Errorf("top = %d", got)
	}
	one := NewTreeLayout(sessions(1, 0), 60, h, line)
	if vp := one.Viewport(20, "s0", 0); vp.Scrolling || vp.OffsetY != 6 {
		t.Errorf("one = %+v", vp)
	}
}
