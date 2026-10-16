package domain

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Plan usage limits (the 5-hour and 7-day windows) as Claude Code reports them to the status
// line command, and the small file `agtop statusline-hook` keeps them in. Only numbers go in
// or out (never session, cwd, transcript or model data).

const (
	// UsageDirName is the directory, inside a Claude Code config directory, where agtop keeps
	// its own files.
	UsageDirName = "agtop"
	// UsageFileName is the usage file, inside UsageDirName.
	UsageFileName = "usage.json"
	// MaxUsageFileBytes is the size limit when reading the usage file.
	MaxUsageFileBytes = 4096
	// UsageFileVersion is the version of the usage file format.
	UsageFileVersion = 1
	// UsageStaleMs: the usage file is stale once its last write is older than this.
	UsageStaleMs = 10 * 60_000
)

// UsageWindow is the usage of one window: percent used (0-100) and when it resets (epoch ms,
// 0 if unknown).
type UsageWindow struct {
	Percent  float64 `json:"percent"`
	ResetsAt int64   `json:"resetsAt,omitempty"`
}

// UsageFile is the content of <configDir>/agtop/usage.json.
type UsageFile struct {
	Version int `json:"version"`
	// UpdatedAt is the epoch ms of the last write.
	UpdatedAt int64        `json:"updatedAt"`
	FiveHour  *UsageWindow `json:"fiveHour,omitempty"`
	SevenDay  *UsageWindow `json:"sevenDay,omitempty"`
}

// RateLimits are the limits found in a status line payload.
type RateLimits struct {
	FiveHour *UsageWindow
	SevenDay *UsageWindow
}

var (
	isoDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)
	numericRE = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
)

func secondsOrMs(n float64) float64 {
	if n < msThreshold {
		return n * 1000
	}
	return n
}

// NormalizeResetsAt reads `resets_at` as epoch ms: a number (or numeric string) in seconds or
// milliseconds, or an ISO 8601 date. It returns false for anything else or out of range.
func NormalizeResetsAt(value any) (int64, bool) {
	var ms float64
	switch v := value.(type) {
	case string:
		s := strings.TrimFunc(v, isSpace)
		switch {
		case numericRE.MatchString(s):
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, false
			}
			ms = secondsOrMs(n)
		case isoDateRE.MatchString(s):
			var ok bool
			var v int64
			if v, ok = ParseDate(s); !ok {
				return 0, false
			}
			ms = float64(v)
		default:
			return 0, false
		}
	default:
		n, ok := asNumber(value)
		if !ok {
			return 0, false
		}
		ms = secondsOrMs(n)
	}
	if ms <= 0 || ms > maxEpochMs {
		return 0, false
	}
	return int64(math.Floor(ms + 0.5)), true
}

// NormalizePercent reads `used_percentage` as a finite number clamped to 0-100.
func NormalizePercent(value any) (float64, bool) {
	n, ok := asNumber(value)
	if !ok {
		return 0, false
	}
	return math.Min(100, math.Max(0, n)), true
}

func windowFrom(value any) *UsageWindow {
	m, ok := asRecord(value)
	if !ok {
		return nil
	}
	percent, ok := NormalizePercent(m["used_percentage"])
	if !ok {
		return nil
	}
	w := UsageWindow{Percent: percent}
	if resetsAt, ok := NormalizeResetsAt(m["resets_at"]); ok {
		w.ResetsAt = resetsAt
	}
	return &w
}

// ExtractRateLimits reads `rate_limits.five_hour` and `rate_limits.seven_day` of a status line
// payload (already decoded JSON) and nothing else. It returns false when neither window is there.
func ExtractRateLimits(payload any) (RateLimits, bool) {
	p, ok := asRecord(payload)
	if !ok {
		return RateLimits{}, false
	}
	rl, ok := asRecord(p["rate_limits"])
	if !ok {
		return RateLimits{}, false
	}
	limits := RateLimits{FiveHour: windowFrom(rl["five_hour"]), SevenDay: windowFrom(rl["seven_day"])}
	if limits.FiveHour == nil && limits.SevenDay == nil {
		return RateLimits{}, false
	}
	return limits, true
}

func sameWindow(x, y *UsageWindow) bool {
	if x == nil || y == nil {
		return x == y
	}
	return *x == *y
}

// SameRateLimits reports whether both have the same windows (percent and reset).
func SameRateLimits(a, b RateLimits) bool {
	return sameWindow(a.FiveHour, b.FiveHour) && sameWindow(a.SevenDay, b.SevenDay)
}

// parseUsageWindow is the strict window check: only percent (0-100) and an optional epoch
// resetsAt. present is false when the key is absent; ok is false when it is invalid.
func parseUsageWindow(m map[string]any, key string) (w *UsageWindow, ok bool) {
	v, present := m[key]
	if !present {
		return nil, true
	}
	rec, isRec := asRecord(v)
	if !isRec || !onlyKeys(rec, "percent", "resetsAt") {
		return nil, false
	}
	percent, okP := asNumber(rec["percent"])
	if !okP || percent < 0 || percent > 100 {
		return nil, false
	}
	out := UsageWindow{Percent: percent}
	if r, present := rec["resetsAt"]; present {
		resetsAt, okR := asEpoch(r)
		if !okR {
			return nil, false
		}
		out.ResetsAt = resetsAt
	}
	return &out, true
}

// ParseUsageFile strictly validates the usage file (already decoded JSON): the known keys only,
// with the expected types and ranges.
func ParseUsageFile(value any) (UsageFile, bool) {
	m, ok := asRecord(value)
	if !ok || !onlyKeys(m, "version", "updatedAt", "fiveHour", "sevenDay") {
		return UsageFile{}, false
	}
	version, okV := asNumber(m["version"])
	updatedAt, okU := asEpoch(m["updatedAt"])
	if !okV || version != UsageFileVersion || !okU {
		return UsageFile{}, false
	}
	five, ok5 := parseUsageWindow(m, "fiveHour")
	seven, ok7 := parseUsageWindow(m, "sevenDay")
	if !ok5 || !ok7 {
		return UsageFile{}, false
	}
	return UsageFile{UsageFileVersion, updatedAt, five, seven}, true
}

// UnmarshalUsageFile decodes and validates the bytes of a usage file.
func UnmarshalUsageFile(data []byte) (UsageFile, bool) {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return UsageFile{}, false
	}
	return ParseUsageFile(v)
}

// MarshalUsageFile is the JSON form of file.
func MarshalUsageFile(file UsageFile) ([]byte, error) {
	return json.Marshal(file)
}

// UsageWindowView is a window as the UI shows it: Percent is nil once ResetsAt has passed (the
// window reset after the last write, so the old percent no longer holds).
type UsageWindowView struct {
	Percent  *float64
	ResetsAt int64
}

// UsageView is the usage of a profile, ready to show.
type UsageView struct {
	FiveHour  *UsageWindowView
	SevenDay  *UsageWindowView
	UpdatedAt int64
	// Stale: the file was last written more than UsageStaleMs ago, or a window's ResetsAt has
	// already passed (that window's percent is then unknown).
	Stale bool
}

func windowView(w *UsageWindow, now int64) (view *UsageWindowView, reset bool) {
	if w == nil {
		return nil, false
	}
	if w.ResetsAt != 0 && w.ResetsAt <= now {
		return &UsageWindowView{}, true
	}
	percent := w.Percent
	return &UsageWindowView{Percent: &percent, ResetsAt: w.ResetsAt}, false
}

// NewUsageView returns what the UI shows of a usage file at now (see UsageView.Stale).
func NewUsageView(file UsageFile, now int64) UsageView {
	five, fiveReset := windowView(file.FiveHour, now)
	seven, sevenReset := windowView(file.SevenDay, now)
	return UsageView{
		FiveHour:  five,
		SevenDay:  seven,
		UpdatedAt: file.UpdatedAt,
		Stale:     now-file.UpdatedAt > UsageStaleMs || fiveReset || sevenReset,
	}
}
