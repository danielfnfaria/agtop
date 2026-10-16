package config

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

func colorHint() string {
	return "use a name (" + strings.Join(style.ToneNames, ", ") + ") or #rgb/#rrggbb"
}

func oneOf(v any, allowed []string, key, file string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", errorf("%s: %s must be a string", file, key)
	}
	if !contains(allowed, s) {
		return "", errorf("%s: %s = \"%s\" is not valid (expected %s)", file, key, s, strings.Join(allowed, " | "))
	}
	return s, nil
}

func color(v any, key, file string) (style.Color, error) {
	s, ok := v.(string)
	if !ok {
		return "", errorf("%s: %s must be a string", file, key)
	}
	if !style.IsColor(s) {
		return "", errorf("%s: %s = \"%s\" is not a color (%s)", file, key, s, colorHint())
	}
	return s, nil
}

func boolean(v any, key, file string) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, errorf("%s: %s must be true or false", file, key)
	}
	return b, nil
}

// nonPrintable reports any control, format, surrogate, private-use or unassigned code point
// (ESC, BEL, OSC...).
func nonPrintable(r rune) bool {
	return unicode.In(r, unicode.C) || !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z, unicode.C)
}

func isEmoji(s string) bool {
	for _, r := range s {
		if r == 0xFE0F || unicode.Is(emojiPresentation, r) {
			return true
		}
	}
	return false
}

// glyph validates a glyph of exactly width columns and no control characters (they could inject
// escapes).
func glyph(v any, width int, key, file string) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", errorf("%s: %s must be a string", file, key)
	}
	if strings.IndexFunc(s, nonPrintable) >= 0 {
		return "", errorf("%s: %s must not contain control characters", file, key)
	}
	if isEmoji(s) {
		return "", errorf("%s: %s must not be an emoji (terminals disagree on its width)", file, key)
	}
	if actual := ansi.StringWidth(s); actual != width {
		plural := "s"
		if width == 1 {
			plural = ""
		}
		return "", errorf("%s: %s = \"%s\" must be %d column%s wide (it is %d)", file, key, s, width, plural, actual)
	}
	return s, nil
}

func unknownKey(key, section string, expected []string, file string) error {
	return errorf("%s: unknown key \"%s\" in [%s] (expected %s)", file, key, section, strings.Join(expected, ", "))
}

func list(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok && len(l) > 0
}

func parseTheme(t map[string]any, order keyOrder, file string) (*style.ThemeConfig, error) {
	out := &style.ThemeConfig{}
	colors := map[string]style.Color{}
	for _, key := range order.keys("theme", t) {
		value, where := t[key], "theme."+key
		switch {
		case key == "preset":
			s, err := oneOf(value, style.ThemePresets, where, file)
			if err != nil {
				return nil, err
			}
			out.Preset = s
		case key == "profiles":
			l, ok := list(value)
			if !ok {
				return nil, errorf("%s: %s must be a non-empty list of colors", file, where)
			}
			for i, c := range l {
				col, err := color(c, fmt.Sprintf("%s[%d]", where, i), file)
				if err != nil {
					return nil, err
				}
				out.Profiles = append(out.Profiles, col)
			}
		case contains(style.ThemeRoles, key):
			c, err := color(value, where, file)
			if err != nil {
				return nil, err
			}
			colors[key] = c
		default:
			expected := append(append([]string{"preset"}, style.ThemeRoles...), "profiles")
			return nil, unknownKey(key, "theme", expected, file)
		}
	}
	if len(colors) > 0 {
		out.Colors = colors
	}
	return out, nil
}

func parseGlyphs(g map[string]any, order keyOrder, file string) (*style.GlyphsConfig, error) {
	out := &style.GlyphsConfig{}
	slots := map[string]string{}
	for _, key := range order.keys("glyphs", g) {
		value, where := g[key], "glyphs."+key
		switch {
		case key == "preset":
			s, err := oneOf(value, style.GlyphPresets, where, file)
			if err != nil {
				return nil, err
			}
			out.Preset = s
		case key == "spinner":
			l, ok := list(value)
			if !ok {
				return nil, errorf("%s: %s must be a non-empty list of glyphs", file, where)
			}
			for i, f := range l {
				s, err := glyph(f, style.SlotWidth, fmt.Sprintf("%s[%d]", where, i), file)
				if err != nil {
					return nil, err
				}
				out.Spinner = append(out.Spinner, s)
			}
		case contains(style.GlyphSlots, key):
			s, err := glyph(value, style.SlotWidth, where, file)
			if err != nil {
				return nil, err
			}
			slots[key] = s
		default:
			expected := append(append([]string{"preset"}, style.GlyphSlots...), "spinner")
			return nil, unknownKey(key, "glyphs", expected, file)
		}
	}
	if len(slots) > 0 {
		out.Slots = slots
	}
	return out, nil
}

func parseLayout(l map[string]any, order keyOrder, file string) (*style.LayoutConfig, error) {
	out := &style.LayoutConfig{}
	for _, key := range order.keys("layout", l) {
		value, where := l[key], "layout."+key
		switch key {
		case "top_bar", "sparkline":
			b, err := boolean(value, where, file)
			if err != nil {
				return nil, err
			}
			if key == "top_bar" {
				out.TopBar = &b
			} else {
				out.Sparkline = &b
			}
		case "time":
			s, err := oneOf(value, style.TimeStyles, where, file)
			if err != nil {
				return nil, err
			}
			out.Time = s
		default:
			return nil, unknownKey(key, "layout", []string{"top_bar", "time", "sparkline"}, file)
		}
	}
	return out, nil
}

// parseAppearance validates the [theme], [glyphs] and [layout] tables of the decoded document.
// It returns nil when none is present.
func parseAppearance(doc map[string]any, order keyOrder, file string) (*style.AppearanceConfig, error) {
	theme, err := table(doc["theme"], "theme", file)
	if err != nil {
		return nil, err
	}
	glyphs, err := table(doc["glyphs"], "glyphs", file)
	if err != nil {
		return nil, err
	}
	layout, err := table(doc["layout"], "layout", file)
	if err != nil {
		return nil, err
	}
	out := &style.AppearanceConfig{}
	present := false
	if theme != nil {
		if out.Theme, err = parseTheme(theme, order, file); err != nil {
			return nil, err
		}
		present = true
	}
	if glyphs != nil {
		if out.Glyphs, err = parseGlyphs(glyphs, order, file); err != nil {
			return nil, err
		}
		present = true
	}
	if layout != nil {
		if out.Layout, err = parseLayout(layout, order, file); err != nil {
			return nil, err
		}
		present = true
	}
	if !present {
		return nil, nil
	}
	return out, nil
}
