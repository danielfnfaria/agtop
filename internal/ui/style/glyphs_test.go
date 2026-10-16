package style

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestGlyphPresets(t *testing.T) {
	presets := []struct {
		name string
		g    Glyphs
	}{{"unicode", UnicodeGlyphs}, {"ascii", ASCIIGlyphs}}
	for _, p := range presets {
		t.Run(p.name+"/every slot has the required width", func(t *testing.T) {
			for _, slot := range GlyphSlots {
				if w := ansi.StringWidth(*p.g.Slot(slot)); w != SlotWidth {
					t.Errorf("%s: width %d", slot, w)
				}
			}
		})
		t.Run(p.name+"/frames, levels, icons, lines and edges are 1 column", func(t *testing.T) {
			if len(p.g.Spinner) == 0 || len(p.g.Spark) != 8 {
				t.Fatalf("spinner %d, spark %d", len(p.g.Spinner), len(p.g.Spark))
			}
			all := append(append([]string{}, p.g.Spinner...), p.g.Spark...)
			for _, v := range []any{p.g.Icons, p.g.Line, p.g.TopRound, p.g.TopBold, p.g.BorderRound, p.g.BorderBold} {
				rv := reflect.ValueOf(v)
				for i := range rv.NumField() {
					all = append(all, rv.Field(i).String())
				}
			}
			for _, s := range all {
				if w := ansi.StringWidth(s); w != 1 {
					t.Errorf("%q: width %d", s, w)
				}
			}
		})
	}
}

func TestResolveGlyphs(t *testing.T) {
	t.Run("defaults to the unicode preset", func(t *testing.T) {
		if !reflect.DeepEqual(ResolveGlyphs(GlyphsConfig{}), UnicodeGlyphs) {
			t.Error("not unicode")
		}
	})
	t.Run("applies the preset, then the slot and spinner overrides", func(t *testing.T) {
		g := ResolveGlyphs(GlyphsConfig{Preset: "ascii", Slots: map[string]string{"dot": "o"}, Spinner: []string{"a", "b"}})
		if g.Dot != "o" || g.BarFull != ASCIIGlyphs.BarFull || !reflect.DeepEqual(g.Spinner, []string{"a", "b"}) {
			t.Errorf("got %+v", g)
		}
		if g.BorderRound != ASCIIGlyphs.BorderRound {
			t.Error("border not from the preset")
		}
	})
	t.Run("does not alias the preset", func(t *testing.T) {
		g := ResolveGlyphs(GlyphsConfig{})
		g.Spinner[0] = "x"
		if UnicodeGlyphs.Spinner[0] == "x" {
			t.Error("mutated preset")
		}
	})
	t.Run("provider mark", func(t *testing.T) {
		if ASCIIGlyphs.ProviderMark("✦") != "*" || UnicodeGlyphs.ProviderMark("✦") != "✦" {
			t.Error("provider mark")
		}
	})
}
