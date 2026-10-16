package domain

import (
	"math"
	"testing"
)

func TestParseDate(t *testing.T) {
	// Expected values from Date.parse in Node (only UTC/offset inputs: no local time).
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"2026-01-01T10:00:00.000Z", 1767261600000, true},
		{"2026-01-01", 1767225600000, true},
		{"2026-02-30T00:00:00Z", 1772409600000, true},
		{"2026-01-01T24:00:00Z", 1767312000000, true},
		{"2026-01-01t10:00:00z", 1767261600000, true},
		{"2026-01-01T10:00:00.123456789Z", 1767261600123, true},
		{"2026-01-01T10:00:00+0100", 1767258000000, true},
		{"2026-01-01T10:00:00+01:00", 1767258000000, true},
		{"2026-01-01 10:00:00Z", 1767261600000, true},
		{"+002026-01-01T00:00:00Z", 1767225600000, true},
		{"2026", 1767225600000, true},
		{"2026-01-01T10:00Z", 1767261600000, true},
		{"2026-01-01T10:00+0530", 1767241800000, true},
		{"2026-01-01T10:00:00-0530", 1767281400000, true},
		{"2026-13-01T00:00:00Z", 0, false},
		{"2026-01-01T10:00:00.Z", 0, false},
		{"not a date", 0, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := ParseDate(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("ParseDate(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestJSWhitespaceRegexps(t *testing.T) {
	if !UsesGh("gh pr") || !UsesGh("gh pr") {
		t.Error("UsesGh should accept ascii and unicode white space")
	}
	if got := ClassifyCommand("npm\vtest"); got != CmdTests {
		t.Errorf("ClassifyCommand(npm\\vtest) = %q, want tests", got)
	}
}

func TestSafeInt(t *testing.T) {
	if SafeInt(1e300) != MaxSafeInt || SafeInt(-1e300) != -MaxSafeInt || SafeInt(math.NaN()) != 0 ||
		SafeNonNegInt(-5) != 0 || SafeNonNegInt(3.9) != 3 {
		t.Error("SafeInt clamping wrong")
	}
	if SatAdd(MaxSafeInt, MaxSafeInt) != MaxSafeInt || SatAdd(1, -5) != 0 {
		t.Error("SatAdd wrong")
	}
}
