package style

import (
	"reflect"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

func TestContextColor(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{0, "green"}, {ContextWarnPercent - 0.1, "green"}, {ContextWarnPercent, "yellow"},
		{ContextCriticalPercent - 0.1, "yellow"}, {ContextCriticalPercent, "red"}, {100, "red"},
	}
	for _, tt := range tests {
		if got := ContextColor(tt.pct, DarkTheme); got != tt.want {
			t.Errorf("ContextColor(%v) = %q, want %q", tt.pct, got, tt.want)
		}
		if got := UsageColor(tt.pct, DarkTheme); got != tt.want {
			t.Errorf("UsageColor(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestActivityColor(t *testing.T) {
	tests := []struct {
		a    Activity
		want string
	}{{ActivityBusy, "yellow"}, {ActivityShell, "cyan"}, {ActivityWaiting, "red"}, {ActivityIdle, "green"}, {ActivityUnknown, "gray"}}
	for _, tt := range tests {
		if got := ActivityColor(tt.a, DarkTheme); got != tt.want {
			t.Errorf("ActivityColor(%q) = %q, want %q", tt.a, got, tt.want)
		}
	}
}

func TestActivityText(t *testing.T) {
	tests := []struct {
		name string
		a    Activity
		raw  string
		want string
	}{
		{"known as is", ActivityShell, "", "shell"},
		{"waiting", ActivityWaiting, "", "waiting"},
		{"unknown with raw status", ActivityUnknown, "paused", "paused"},
		{"unknown without", ActivityUnknown, "", "unknown"},
	}
	for _, tt := range tests {
		if got := ActivityText(tt.a, tt.raw); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestStatusAndBorderColor(t *testing.T) {
	if StatusColor(domain.StatusRunning, DarkTheme) != "yellow" ||
		StatusColor(domain.StatusCompleted, DarkTheme) != "green" ||
		StatusColor(domain.StatusFailed, DarkTheme) != "red" ||
		StatusColor(domain.StatusUnknown, DarkTheme) != "gray" {
		t.Error("status colors")
	}
	if SessionBorderColor(ActivityBusy, "stale", DarkTheme) != "gray" ||
		SessionBorderColor(ActivityBusy, "live", DarkTheme) != "yellow" {
		t.Error("border colors")
	}
}

func TestResolveTheme(t *testing.T) {
	th := ResolveTheme(ThemeConfig{Preset: "light", Colors: map[string]Color{"busy": "yellow"}, Profiles: []Color{"red"}})
	if th.Busy != "yellow" || th.Idle != "#008700" || !reflect.DeepEqual(th.Profiles, []Color{"red"}) {
		t.Errorf("got %+v", th)
	}
	if !reflect.DeepEqual(ResolveTheme(ThemeConfig{}), DarkTheme) {
		t.Error("default is not dark")
	}
}

func TestPendingRole(t *testing.T) {
	if DarkTheme.Pending != "#8a8a8a" || LightTheme.Pending != "#9e9e9e" {
		t.Errorf("pending colors: dark=%q light=%q", DarkTheme.Pending, LightTheme.Pending)
	}
	th := ResolveTheme(ThemeConfig{Colors: map[string]Color{"pending": "blue"}})
	if th.Pending != "blue" || *th.Role("pending") != "blue" {
		t.Errorf("pending override = %q", th.Pending)
	}
}

func TestANSIIndex(t *testing.T) {
	tests := []struct {
		name string
		want int
		ok   bool
	}{
		{"black", 0, true}, {"white", 7, true}, {"gray", 8, true}, {"blackBright", 8, true},
		{"redBright", 9, true}, {"whiteBright", 15, true}, {"nope", 0, false}, {"Bright", 0, false},
	}
	for _, tt := range tests {
		if got, ok := ANSIIndex(tt.name); got != tt.want || ok != tt.ok {
			t.Errorf("ANSIIndex(%q) = %d,%v", tt.name, got, ok)
		}
	}
	for _, n := range ToneNames {
		if _, ok := ANSIIndex(n); !ok {
			t.Errorf("tone %q has no index", n)
		}
	}
}

func TestColorValidation(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"red", true}, {"gray", true}, {"#f00", true}, {"#00FF00", true}, {"x", false},
		{"#12", false}, {"#12345g", false}, {"rgb(1,2,3)", false}, {"", false},
	}
	for _, tt := range tests {
		if IsColor(tt.in) != tt.want {
			t.Errorf("IsColor(%q) != %v", tt.in, tt.want)
		}
	}
	if h, ok := ExpandHex("#F0a"); !ok || h != "#ff00aa" {
		t.Errorf("ExpandHex = %q", h)
	}
}
