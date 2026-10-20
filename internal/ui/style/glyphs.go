package style

// GlyphPresets are the preset names, the first being the default.
var GlyphPresets = []string{"unicode", "ascii"}

// SlotWidth is the number of columns every single-value glyph slot (and each spinner frame)
// must take.
const SlotWidth = 1

// GlyphSlots are the single-value slots a user may override, in documentation order.
var GlyphSlots = []string{
	"barFull", "barEmpty", "dot", "pointer", "left", "up", "right", "down", "middot", "dash",
	"expanded", "collapsed", "ellipsis", "enter", "gear", "cross", "reset",
}

// Box is the set of characters of a box border.
type Box struct {
	TopLeft, Top, TopRight, Left, Right, BottomLeft, Bottom, BottomRight string
}

// TopEdge is the corners and line of a top border that carries a title.
type TopEdge struct{ Left, Right, Line string }

// Lines are the connectors of the tree.
type Lines struct {
	Horizontal, Vertical, DownRight, DownLeft, UpRight, UpLeft, VerticalRight, VerticalLeft,
	HorizontalDown, HorizontalUp, Cross string
}

// Icons say what an agent is doing.
type Icons struct {
	Writing, Reading, Searching, Web, Delegating, Tests, Building, Git, Installing, Bash, Tool,
	Thinking, Starting string
}

// Glyphs is everything the UI draws with, resolved. Every single-value slot and frame is one
// column wide.
type Glyphs struct {
	BarFull, BarEmpty, Dot, Pointer, Left, Up, Right, Down, Middot, Dash, Expanded, Collapsed,
	Ellipsis, Enter, Gear, Cross, Reset string

	Spinner []string
	// Spark are the sparkline levels 0-7.
	Spark                   []string
	Icons                   Icons
	Line                    Lines
	TopRound, TopBold       TopEdge
	BorderRound, BorderBold Box
	// ProviderSymbol replaces the provider symbols (which are not ASCII); empty keeps them.
	ProviderSymbol string
}

// Slot returns a pointer to the named single-value slot (one of GlyphSlots), or nil.
func (g *Glyphs) Slot(name string) *string {
	switch name {
	case "barFull":
		return &g.BarFull
	case "barEmpty":
		return &g.BarEmpty
	case "dot":
		return &g.Dot
	case "pointer":
		return &g.Pointer
	case "left":
		return &g.Left
	case "up":
		return &g.Up
	case "right":
		return &g.Right
	case "down":
		return &g.Down
	case "middot":
		return &g.Middot
	case "dash":
		return &g.Dash
	case "expanded":
		return &g.Expanded
	case "collapsed":
		return &g.Collapsed
	case "ellipsis":
		return &g.Ellipsis
	case "enter":
		return &g.Enter
	case "gear":
		return &g.Gear
	case "cross":
		return &g.Cross
	case "reset":
		return &g.Reset
	}
	return nil
}

// ProviderMark returns the provider's symbol, or the preset's replacement for it.
func (g *Glyphs) ProviderMark(symbol string) string {
	if g.ProviderSymbol != "" {
		return g.ProviderSymbol
	}
	return symbol
}

// UnicodeGlyphs is the default preset.
var UnicodeGlyphs = Glyphs{
	BarFull: "█", BarEmpty: "░", Dot: "●", Pointer: "›",
	Left: "←", Up: "↑", Right: "→", Down: "↓", Middot: "·",
	Dash: "—", Expanded: "▾", Collapsed: "▸", Ellipsis: "…",
	Enter: "↵", Gear: "⚙", Cross: "✗", Reset: "↻",
	Spinner: []string{
		"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇",
		"⠏",
	},
	Spark: []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
	Icons: Icons{
		Writing: "✎", Reading: "≡", Searching: "⌕", Web: "☁",
		Delegating: "⇢", Tests: "✓", Building: "⚒", Git: "±",
		Installing: "⇣", Bash: "▸", Tool: "▸", Thinking: "…",
		Starting: "◌",
	},
	Line: Lines{
		Horizontal: "─", Vertical: "│", DownRight: "┌", DownLeft: "┐",
		UpRight: "└", UpLeft: "┘", VerticalRight: "├", VerticalLeft: "┤",
		HorizontalDown: "┬", HorizontalUp: "┴", Cross: "┼",
	},
	TopRound: TopEdge{Left: "╭", Right: "╮", Line: "─"},
	TopBold:  TopEdge{Left: "┏", Right: "┓", Line: "━"},
	BorderRound: Box{
		TopLeft: "╭", Top: "─", TopRight: "╮", Left: "│", Right: "│",
		BottomLeft: "╰", Bottom: "─", BottomRight: "╯",
	},
	BorderBold: Box{
		TopLeft: "┏", Top: "━", TopRight: "┓", Left: "┃", Right: "┃",
		BottomLeft: "┗", Bottom: "━", BottomRight: "┛",
	},
}

// ASCIIGlyphs draws with printable ASCII only.
var ASCIIGlyphs = Glyphs{
	BarFull: "#", BarEmpty: ".", Dot: "*", Pointer: ">", Left: "<", Up: "^", Right: ">",
	Down: "v", Middot: "-", Dash: "-", Expanded: "v", Collapsed: ">", Ellipsis: "~",
	Enter: "<", Gear: "&", Cross: "x", Reset: "@", ProviderSymbol: "*",
	Spinner: []string{"-", "\\", "|", "/"},
	Spark:   []string{"_", ".", ":", "-", "=", "+", "*", "#"},
	Icons: Icons{
		Writing: "/", Reading: "=", Searching: "?", Web: "@", Delegating: ">", Tests: "+",
		Building: "%", Git: "g", Installing: "v", Bash: "$", Tool: ">", Thinking: ".",
		Starting: "o",
	},
	Line: Lines{
		Horizontal: "-", Vertical: "|", DownRight: "+", DownLeft: "+", UpRight: "+", UpLeft: "+",
		VerticalRight: "+", VerticalLeft: "+", HorizontalDown: "+", HorizontalUp: "+", Cross: "+",
	},
	TopRound: TopEdge{Left: "+", Right: "+", Line: "-"},
	TopBold:  TopEdge{Left: "#", Right: "#", Line: "="},
	BorderRound: Box{
		TopLeft: "+", Top: "-", TopRight: "+", Left: "|", Right: "|", BottomLeft: "+",
		Bottom: "-", BottomRight: "+",
	},
	BorderBold: Box{
		TopLeft: "#", Top: "=", TopRight: "#", Left: "#", Right: "#", BottomLeft: "#",
		Bottom: "=", BottomRight: "#",
	},
}

// GlyphsConfig is what the config file may set: a preset and per-slot overrides.
type GlyphsConfig struct {
	Preset  string
	Slots   map[string]string
	Spinner []string
}

// ResolveGlyphs applies the overrides over the preset (unicode by default).
func ResolveGlyphs(c GlyphsConfig) Glyphs {
	g := UnicodeGlyphs
	if c.Preset == "ascii" {
		g = ASCIIGlyphs
	}
	g.Spinner = append([]string(nil), g.Spinner...)
	g.Spark = append([]string(nil), g.Spark...)
	for name, v := range c.Slots {
		if p := g.Slot(name); p != nil {
			*p = v
		}
	}
	if c.Spinner != nil {
		g.Spinner = append([]string(nil), c.Spinner...)
	}
	return g
}
