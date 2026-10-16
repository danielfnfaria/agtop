package style

// TimeStyles are how times are shown, the first being the default.
var TimeStyles = []string{"relative", "absolute"}

// LayoutConfig is what the config file may set in [layout]; nil means unset.
type LayoutConfig struct {
	TopBar    *bool
	Time      string
	Sparkline *bool
}

// Layout is the resolved layout.
type Layout struct {
	TopBar    bool
	Time      string
	Sparkline bool
}

// DefaultLayout is the layout without any configuration.
var DefaultLayout = Layout{TopBar: true, Time: "relative", Sparkline: true}

// Resolve applies the set values over DefaultLayout.
func (c LayoutConfig) Resolve() Layout {
	l := DefaultLayout
	if c.TopBar != nil {
		l.TopBar = *c.TopBar
	}
	if c.Time != "" {
		l.Time = c.Time
	}
	if c.Sparkline != nil {
		l.Sparkline = *c.Sparkline
	}
	return l
}

// AppearanceConfig is the validated [theme], [glyphs] and [layout] sections; each is nil when
// absent from the file.
type AppearanceConfig struct {
	Theme  *ThemeConfig
	Glyphs *GlyphsConfig
	Layout *LayoutConfig
}

// Appearance is everything the config file can change in how the UI looks, resolved.
type Appearance struct {
	Theme  Theme
	Glyphs Glyphs
	Layout Layout
}

// DefaultAppearance returns the look without any configuration.
func DefaultAppearance() Appearance {
	return ResolveAppearance(nil)
}

// ResolveAppearance applies the sections of the config file (or none) over the defaults.
func ResolveAppearance(c *AppearanceConfig) Appearance {
	var cfg AppearanceConfig
	if c != nil {
		cfg = *c
	}
	a := Appearance{
		Theme:  ResolveTheme(derefTheme(cfg.Theme)),
		Glyphs: ResolveGlyphs(derefGlyphs(cfg.Glyphs)),
		Layout: DefaultLayout,
	}
	if cfg.Layout != nil {
		a.Layout = cfg.Layout.Resolve()
	}
	return a
}

func derefTheme(c *ThemeConfig) ThemeConfig {
	if c == nil {
		return ThemeConfig{}
	}
	return *c
}

func derefGlyphs(c *GlyphsConfig) GlyphsConfig {
	if c == nil {
		return GlyphsConfig{}
	}
	return *c
}
