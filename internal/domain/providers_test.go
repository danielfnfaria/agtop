package domain

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var hexColorRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func TestProviderClaudeCode(t *testing.T) {
	want := Provider{ID: "claude-code", DisplayName: "Claude Code", Symbol: "\u2733", Color: "#D97757"}
	if got := ProviderInfo(ClaudeCode); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestProvidersSymbolsAndColors(t *testing.T) {
	for id, p := range Providers {
		if p.ID != id {
			t.Errorf("%s: id %s", id, p.ID)
		}
		if n := len([]rune(p.Symbol)); n != 1 {
			t.Errorf("%s: symbol has %d characters", id, n)
		}
		if w := ansi.StringWidth(p.Symbol); w != 1 {
			t.Errorf("%s: symbol width %d", id, w)
		}
		if strings.ContainsRune(p.Symbol, 0xfe0f) {
			t.Errorf("%s: symbol has the emoji variation selector", id)
		}
		if !hexColorRE.MatchString(p.Color) {
			t.Errorf("%s: color %q", id, p.Color)
		}
	}
}

func TestProviderInfoUnknown(t *testing.T) {
	p := ProviderInfo("test-provider")
	if p.ID != "test-provider" || p.DisplayName != "test-provider" {
		t.Errorf("got %+v", p)
	}
	if w := ansi.StringWidth(p.Symbol); w != 1 {
		t.Errorf("symbol width %d", w)
	}
	if !hexColorRE.MatchString(p.Color) {
		t.Errorf("color %q", p.Color)
	}
}
