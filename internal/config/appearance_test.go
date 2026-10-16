package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/ui/style"
)

const testFile = "/home/u/.config/agtop/config.toml"

func parseAppearanceText(t *testing.T, text string) *style.AppearanceConfig {
	t.Helper()
	d, err := ParseFile(text, testFile, nil, "/home/u")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return d.Appearance
}

// wantFails asserts that text is rejected with an *Error containing msg.
func wantFails(t *testing.T, text, msg string) {
	t.Helper()
	_, err := ParseFile(text, testFile, nil, "/home/u")
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("%q: want *Error, got %v", text, err)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("%q:\n got  %s\n want contains %s", text, err, msg)
	}
}

func TestParseAppearance(t *testing.T) {
	t.Run("no sections: no appearance, and resolving gives the defaults", func(t *testing.T) {
		for _, text := range []string{"", "[profiles]\na = \"/x\""} {
			if got := parseAppearanceText(t, text); got != nil {
				t.Errorf("%q: got %+v", text, got)
			}
		}
		if !reflect.DeepEqual(style.ResolveAppearance(nil), style.DefaultAppearance()) {
			t.Error("defaults differ")
		}
	})

	t.Run("reads the whole example", func(t *testing.T) {
		out := parseAppearanceText(t, `
[theme]
preset = "light"
busy = "yellow"
waiting = "#ff5f5f"
profiles = ["magenta", "blue", "#5fafff"]

[glyphs]
preset = "ascii"
dot = "*"
spinner = ["-", "\\", "|", "/"]

[layout]
top_bar = false
time = "absolute"
sparkline = false
`)
		f := false
		want := &style.AppearanceConfig{
			Theme: &style.ThemeConfig{
				Preset:   "light",
				Colors:   map[string]string{"busy": "yellow", "waiting": "#ff5f5f"},
				Profiles: []string{"magenta", "blue", "#5fafff"},
			},
			Glyphs: &style.GlyphsConfig{Preset: "ascii", Slots: map[string]string{"dot": "*"}, Spinner: []string{"-", "\\", "|", "/"}},
			Layout: &style.LayoutConfig{TopBar: &f, Time: "absolute", Sparkline: &f},
		}
		if !reflect.DeepEqual(out, want) {
			t.Fatalf("got %+v", out)
		}
		r := style.ResolveAppearance(out)
		if r.Theme.Busy != "yellow" || r.Theme.Idle != "#008700" || !reflect.DeepEqual(r.Theme.Profiles, []string{"magenta", "blue", "#5fafff"}) {
			t.Errorf("theme %+v", r.Theme)
		}
		if r.Glyphs.Dot != "*" || r.Glyphs.BarFull != "#" {
			t.Errorf("glyphs %+v", r.Glyphs)
		}
		if r.Layout != (style.Layout{TopBar: false, Time: "absolute", Sparkline: false}) {
			t.Errorf("layout %+v", r.Layout)
		}
	})

	t.Run("sections are independent of [profiles] and of each other", func(t *testing.T) {
		out := parseAppearanceText(t, "[layout]\ntime = \"relative\"")
		if out.Layout == nil || out.Layout.Time != "relative" || out.Theme != nil || out.Glyphs != nil {
			t.Errorf("got %+v", out)
		}
		out = parseAppearanceText(t, "[theme]")
		if out == nil || out.Theme == nil || out.Theme.Preset != "" || out.Theme.Colors != nil {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("accepts #rgb and #rrggbb colors", func(t *testing.T) {
		out := parseAppearanceText(t, "[theme]\nbusy = \"#f00\"\nidle = \"#00FF00\"")
		want := map[string]string{"busy": "#f00", "idle": "#00FF00"}
		if !reflect.DeepEqual(out.Theme.Colors, want) {
			t.Errorf("got %v", out.Theme.Colors)
		}
	})

	t.Run("accepts the pending role", func(t *testing.T) {
		out := parseAppearanceText(t, "[theme]\npending = \"magenta\"")
		if !reflect.DeepEqual(out.Theme.Colors, map[string]string{"pending": "magenta"}) {
			t.Errorf("got %v", out.Theme.Colors)
		}
	})

	t.Run("accepts a plain unicode glyph", func(t *testing.T) {
		out := parseAppearanceText(t, "[glyphs]\ndot = \"\\u25cf\"")
		if !reflect.DeepEqual(out.Glyphs.Slots, map[string]string{"dot": "\u25cf"}) {
			t.Errorf("got %v", out.Glyphs.Slots)
		}
	})
}

func TestParseAppearanceErrors(t *testing.T) {
	f := testFile + ": "
	tests := []struct {
		name, text, want string
	}{
		{"unknown theme preset", "[theme]\npreset = \"mono\"", f + `theme.preset = "mono" is not valid`},
		{"unknown glyphs preset", "[glyphs]\npreset = \"emoji\"", f + `glyphs.preset = "emoji" is not valid`},
		{"unknown time", "[layout]\ntime = \"never\"", f + `layout.time = "never" is not valid`},
		{"color not in the allow-list", "[theme]\nbusy = \"x\"", f + `theme.busy = "x" is not a color (`},
		{"short hex", "[theme]\nbusy = \"#12\"", "is not a color"},
		{"bad hex digit", "[theme]\nbusy = \"#12345g\"", "is not a color"},
		{"rgb()", "[theme]\nbusy = \"rgb(1,2,3)\"", "is not a color"},
		{"profile palette entry", "[theme]\nprofiles = [\"magenta\", \"nope\"]", f + `theme.profiles[1] = "nope"`},
		{"glyph too wide", "[glyphs]\ndot = \"ab\"", f + `glyphs.dot = "ab" must be 1 column wide (it is 2)`},
		{"glyph empty", "[glyphs]\ndot = \"\"", "must be 1 column wide (it is 0)"},
		{"glyph CJK", "[glyphs]\ndot = \"\\u4e2d\"", "must be 1 column wide (it is 2)"},
		{"glyph OSC", "[glyphs]\ndot = \"\\u001b]8;;x\\u0007\"", f + "glyphs.dot must not contain control characters"},
		{"glyph newline", "[glyphs]\ndot = \"\\n\"", "control characters"},
		{"glyph DEL", "[glyphs]\ndot = \"\\u007f\"", "control characters"},
		{"glyph format char", "[glyphs]\ndot = \"\\u200b\"", "control characters"},
		{"spinner control", "[glyphs]\nspinner = [\"-\", \"\\u001b\"]", "glyphs.spinner[1] must not contain control"},
		{"spinner wide", "[glyphs]\nspinner = [\"-\", \"ab\"]", `glyphs.spinner[1] = "ab" must be 1 column wide`},
		{"emoji with VS16", "[glyphs]\ndot = \"\\u2764\\uFE0F\"", "must not be an emoji"},
		{"emoji astral", "[glyphs]\ndot = \"\\U0001F600\"", "must not be an emoji"},
		{"emoji in spinner", "[glyphs]\nspinner = [\"\\u231B\"]", "glyphs.spinner[0] must not be an emoji"},
		{"empty spinner", "[glyphs]\nspinner = []", f + "glyphs.spinner must be a non-empty list"},
		{"color not a string", "[theme]\nbusy = 3", f + "theme.busy must be a string"},
		{"profiles not a list", "[theme]\nprofiles = \"magenta\"", "theme.profiles must be a non-empty list"},
		{"profiles empty", "[theme]\nprofiles = []", "theme.profiles must be a non-empty list"},
		{"glyph not a string", "[glyphs]\ndot = 1", "glyphs.dot must be a string"},
		{"spinner not a list", "[glyphs]\nspinner = \"-\"", "glyphs.spinner must be a non-empty list"},
		{"bool as string", "[layout]\ntop_bar = \"yes\"", f + "layout.top_bar must be true or false"},
		{"bool as number", "[layout]\nsparkline = 1", "layout.sparkline must be true or false"},
		{"theme not a table", "theme = \"dark\"", f + "[theme] must be a table"},
		{"layout not a table", "layout = 1", f + "[layout] must be a table"},
		{"glyphs date", "glyphs = 1979-05-27", f + "[glyphs] must be a table"},
		{"unknown theme key", "[theme]\nfoo = \"red\"", f + `unknown key "foo" in [theme]`},
		{"unknown glyphs key", "[glyphs]\nfoo = \"x\"", f + `unknown key "foo" in [glyphs]`},
		{"unknown layout key", "[layout]\ncompact = true", f + `unknown key "compact" in [layout]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { wantFails(t, tt.text, tt.want) })
	}
}

func TestUnknownKeyListsExpected(t *testing.T) {
	_, err := ParseFile("[layout]\nx = 1", testFile, nil, "/home/u")
	want := testFile + `: unknown key "x" in [layout] (expected top_bar, time, sparkline)`
	if err == nil || err.Error() != want {
		t.Errorf("got %v", err)
	}
}

func TestFirstErrorFollowsFileOrder(t *testing.T) {
	// zzz comes before aaa in the file, and must be the one reported.
	wantFails(t, "[layout]\nzzz = 1\naaa = 2", `unknown key "zzz"`)
}
