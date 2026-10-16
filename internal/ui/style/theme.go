package style

import "github.com/danielfnfaria/agtop/internal/domain"

// ThemeRoles are the roles the UI paints with, in documentation order.
var ThemeRoles = []string{
	"muted", "accent", "busy", "shell", "waiting", "idle", "done", "failed", "ok", "warn",
	"critical", "link",
}

// ThemePresets are the preset names, the first being the default.
var ThemePresets = []string{"dark", "light"}

// Theme holds a color per role. Profiles is the palette of the profiles, by position (cycling
// after the last one).
type Theme struct {
	Muted, Accent, Busy, Shell, Waiting, Idle, Done, Failed, OK, Warn, Critical, Link Color
	Profiles                                                                          []Color
}

// Role returns a pointer to the color of the named role (one of ThemeRoles), or nil.
func (t *Theme) Role(name string) *Color {
	switch name {
	case "muted":
		return &t.Muted
	case "accent":
		return &t.Accent
	case "busy":
		return &t.Busy
	case "shell":
		return &t.Shell
	case "waiting":
		return &t.Waiting
	case "idle":
		return &t.Idle
	case "done":
		return &t.Done
	case "failed":
		return &t.Failed
	case "ok":
		return &t.OK
	case "warn":
		return &t.Warn
	case "critical":
		return &t.Critical
	case "link":
		return &t.Link
	}
	return nil
}

// DarkTheme is for a dark terminal background.
var DarkTheme = Theme{
	Muted: "gray", Accent: "cyan", Busy: "yellow", Shell: "cyan", Waiting: "red", Idle: "green",
	Done: "green", Failed: "red", OK: "green", Warn: "yellow", Critical: "red", Link: "blue",
	Profiles: []Color{"magenta", "blue", "cyanBright", "magentaBright", "blueBright", "whiteBright"},
}

// LightTheme is for a light terminal background: darker hues (yellow and cyan vanish on white).
var LightTheme = Theme{
	Muted: "#6c6c6c", Accent: "#005f87", Busy: "#af5f00", Shell: "#008787", Waiting: "#d70000",
	Idle: "#008700", Done: "#008700", Failed: "#d70000", OK: "#008700", Warn: "#af5f00",
	Critical: "#d70000", Link: "#0000d7",
	Profiles: []Color{"#af00af", "#0000d7", "#005f87", "#870087", "#5f5fd7", "#000000"},
}

// ThemeConfig is what the config file may set: a preset and overrides by role.
type ThemeConfig struct {
	Preset   string
	Colors   map[string]Color
	Profiles []Color
}

// ResolveTheme applies the overrides over the preset (dark by default).
func ResolveTheme(c ThemeConfig) Theme {
	base := DarkTheme
	if c.Preset == "light" {
		base = LightTheme
	}
	t := base
	t.Profiles = append([]Color(nil), base.Profiles...)
	for role, color := range c.Colors {
		if p := t.Role(role); p != nil {
			*p = color
		}
	}
	if c.Profiles != nil {
		t.Profiles = append([]Color(nil), c.Profiles...)
	}
	return t
}

// Activity is what a session is doing; the UI paints it by role.
type Activity string

const (
	ActivityBusy    Activity = "busy"
	ActivityShell   Activity = "shell"
	ActivityWaiting Activity = "waiting"
	ActivityIdle    Activity = "idle"
	ActivityUnknown Activity = "unknown"
)

// StatusColor is the color of an agent status.
func StatusColor(s domain.AgentStatus, t Theme) Color {
	switch s {
	case domain.StatusLaunched, domain.StatusRunning:
		return t.Busy
	case domain.StatusHandedBack, domain.StatusCompleted:
		return t.Done
	case domain.StatusFailed:
		return t.Failed
	}
	return t.Muted
}

// ActivityColor is the color of the session activity.
func ActivityColor(a Activity, t Theme) Color {
	switch a {
	case ActivityBusy:
		return t.Busy
	case ActivityShell:
		return t.Shell
	case ActivityWaiting:
		return t.Waiting
	case ActivityIdle:
		return t.Idle
	}
	return t.Muted
}

// SessionBorderColor is the color of a session box: the activity color, or muted once the
// session is stale.
func SessionBorderColor(a Activity, liveness string, t Theme) Color {
	if liveness == "stale" {
		return t.Muted
	}
	return ActivityColor(a, t)
}

// ActivityText is the text of the activity: an unknown registry status shows as is.
func ActivityText(a Activity, rawStatus string) string {
	if a == ActivityUnknown && rawStatus != "" {
		return rawStatus
	}
	return string(a)
}

// Usage thresholds (percent) from which a bar turns yellow and red.
const (
	ContextWarnPercent     = 60
	ContextCriticalPercent = 85
	UsageWarnPercent       = 60
	UsageCriticalPercent   = 85
)

// ContextColor is the color of the context bar for a usage already clamped to 0-100.
func ContextColor(percent float64, t Theme) Color {
	return thresholdColor(percent, ContextWarnPercent, ContextCriticalPercent, t)
}

// UsageColor is the color of a plan usage window in the top bar.
func UsageColor(percent float64, t Theme) Color {
	return thresholdColor(percent, UsageWarnPercent, UsageCriticalPercent, t)
}

func thresholdColor(p, warn, critical float64, t Theme) Color {
	switch {
	case p >= critical:
		return t.Critical
	case p >= warn:
		return t.Warn
	}
	return t.OK
}
