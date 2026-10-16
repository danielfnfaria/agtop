package domain

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var resetMs = time.Date(2026, 10, 15, 17, 30, 0, 0, time.UTC).UnixMilli()

func TestNormalizeResetsAt(t *testing.T) {
	ok := []struct {
		name string
		in   any
	}{
		{"epoch seconds", float64(resetMs / 1000)},
		{"epoch ms", float64(resetMs)},
		{"ISO Z", "2026-10-15T17:30:00Z"},
		{"ISO offset with fraction", "2026-10-15T17:30:00.000+00:00"},
		{"numeric string seconds", strconv.FormatInt(resetMs/1000, 10)},
		{"padded numeric string ms", " " + strconv.FormatInt(resetMs, 10) + " "},
		{"fractional seconds are rounded", float64(resetMs)/1000 + 0.0004},
	}
	for _, tt := range ok {
		if got, found := NormalizeResetsAt(tt.in); !found || got != resetMs {
			t.Errorf("%s: got %d, %v; want %d", tt.name, got, found, resetMs)
		}
	}
	bad := []any{nil, "", "soon", "Thu Oct 15 2026", math.NaN(), math.Inf(1), -5.0, 0.0, true, map[string]any{}, []any{}, 9e15}
	for _, v := range bad {
		if got, found := NormalizeResetsAt(v); found {
			t.Errorf("%v: got %d, want none", v, got)
		}
	}
}

func TestNormalizePercent(t *testing.T) {
	good := []struct{ in, want float64 }{{79.4, 79.4}, {0, 0}, {-3, 0}, {101, 100}}
	for _, tt := range good {
		if got, ok := NormalizePercent(tt.in); !ok || got != tt.want {
			t.Errorf("%v: got %v, %v", tt.in, got, ok)
		}
	}
	for _, v := range []any{"79", math.NaN(), math.Inf(1), nil, map[string]any{}} {
		if _, ok := NormalizePercent(v); ok {
			t.Errorf("%v: want none", v)
		}
	}
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtractRateLimits(t *testing.T) {
	t.Run("only the two windows, only percent and reset", func(t *testing.T) {
		payload := map[string]any{
			"session_id":      "SECRET-session",
			"cwd":             "/SECRET/cwd",
			"transcript_path": "/SECRET/transcript.jsonl",
			"model":           map[string]any{"id": "SECRET-model", "display_name": "SECRET"},
			"rate_limits": map[string]any{
				"five_hour": map[string]any{"used_percentage": 79.0, "resets_at": float64(resetMs / 1000), "note": "SECRET"},
				"seven_day": map[string]any{"used_percentage": 23.5, "resets_at": "2026-10-20T09:00:00Z"},
				"other":     map[string]any{"used_percentage": 1.0},
			},
		}
		got, ok := ExtractRateLimits(payload)
		want := RateLimits{
			FiveHour: &UsageWindow{79, resetMs},
			SevenDay: &UsageWindow{23.5, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli()},
		}
		if !ok || !SameRateLimits(got, want) {
			t.Fatalf("got %+v %+v, %v", got.FiveHour, got.SevenDay, ok)
		}
		if b, _ := json.Marshal(got); strings.Contains(string(b), "SECRET") {
			t.Error("leaked")
		}
	})

	t.Run("a window without a valid percent is left out", func(t *testing.T) {
		if _, ok := ExtractRateLimits(decode(t, `{"rate_limits":{"five_hour":{"used_percentage":"x"},"seven_day":{}}}`)); ok {
			t.Error("want none")
		}
		got, ok := ExtractRateLimits(decode(t, `{"rate_limits":{"seven_day":{"used_percentage":5,"resets_at":"bad"}}}`))
		if !ok || got.FiveHour != nil || got.SevenDay == nil || *got.SevenDay != (UsageWindow{Percent: 5}) {
			t.Errorf("got %+v %+v", got.FiveHour, got.SevenDay)
		}
		for _, s := range []string{`null`, `[]`, `"x"`, `{}`, `{"rate_limits":[]}`, `{"rate_limits":null}`} {
			if _, ok := ExtractRateLimits(decode(t, s)); ok {
				t.Errorf("%s: want none", s)
			}
		}
	})

	t.Run("SameRateLimits compares percent and reset of both windows", func(t *testing.T) {
		a := RateLimits{FiveHour: &UsageWindow{1, 2}}
		if !SameRateLimits(a, RateLimits{FiveHour: &UsageWindow{1, 2}}) {
			t.Error("equal")
		}
		if SameRateLimits(a, RateLimits{FiveHour: &UsageWindow{Percent: 1}}) {
			t.Error("different reset")
		}
		if SameRateLimits(a, RateLimits{FiveHour: a.FiveHour, SevenDay: &UsageWindow{Percent: 1}}) {
			t.Error("extra window")
		}
	})
}

func TestParseUsageFile(t *testing.T) {
	ok := UsageFile{1, resetMs - 60_000, &UsageWindow{79, resetMs}, &UsageWindow{Percent: 23}}

	t.Run("accepts the exact format", func(t *testing.T) {
		b, err := MarshalUsageFile(ok)
		if err != nil {
			t.Fatal(err)
		}
		got, found := UnmarshalUsageFile(b)
		if !found || !reflect.DeepEqual(got, ok) {
			t.Errorf("got %+v, %v", got, found)
		}
		min, found := ParseUsageFile(map[string]any{"version": 1, "updatedAt": 5})
		if !found || min != (UsageFile{Version: 1, UpdatedAt: 5}) {
			t.Errorf("minimal: got %+v, %v", min, found)
		}
	})

	t.Run("rejects unknown keys, wrong version, types and ranges", func(t *testing.T) {
		base := func(extra string) string {
			return `{"version":1,"updatedAt":1000,` + extra + `}`
		}
		bad := []string{
			`null`, `[]`,
			base(`"extra":"SECRET"`),
			`{"version":2,"updatedAt":1000}`,
			`{"version":1,"updatedAt":"1"}`,
			`{"version":1,"updatedAt":-1}`,
			base(`"fiveHour":{"percent":101}`),
			base(`"fiveHour":{"percent":-1}`),
			base(`"fiveHour":{"percent":"5"}`),
			base(`"fiveHour":{"percent":5,"resetsAt":"soon"}`),
			base(`"fiveHour":{"percent":5,"cwd":"/x"}`),
			base(`"sevenDay":[]`),
			base(`"sevenDay":null`),
		}
		for _, s := range bad {
			if _, found := ParseUsageFile(decode(t, s)); found {
				t.Errorf("%s: want rejected", s)
			}
		}
	})
}

func TestNewUsageView(t *testing.T) {
	file := UsageFile{1, 1_000_000, &UsageWindow{79, 2_000_000}, &UsageWindow{23, 9_000_000}}
	pct := func(p float64) *float64 { return &p }

	t.Run("fresh: the windows as recorded, not stale", func(t *testing.T) {
		want := UsageView{
			FiveHour:  &UsageWindowView{pct(79), 2_000_000},
			SevenDay:  &UsageWindowView{pct(23), 9_000_000},
			UpdatedAt: 1_000_000,
		}
		if got := NewUsageView(file, 1_000_000+60_000); !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("stale once the last write is older than 10 minutes", func(t *testing.T) {
		if NewUsageView(file, 1_000_000+UsageStaleMs).Stale {
			t.Error("at the limit: not stale")
		}
		old := NewUsageView(file, 1_000_000+UsageStaleMs+1)
		if !old.Stale || old.FiveHour.Percent == nil || *old.FiveHour.Percent != 79 {
			t.Errorf("got %+v", old)
		}
	})

	t.Run("a window whose reset has passed: percent unknown, and stale", func(t *testing.T) {
		f := file
		f.UpdatedAt = 1_900_000
		v := NewUsageView(f, 2_000_000)
		if !v.Stale || !reflect.DeepEqual(v.FiveHour, &UsageWindowView{}) ||
			!reflect.DeepEqual(v.SevenDay, &UsageWindowView{pct(23), 9_000_000}) {
			t.Errorf("got %+v %+v", v.FiveHour, v.SevenDay)
		}
	})
}
