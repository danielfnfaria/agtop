package domain

import (
	"math"
	"testing"
)

func TestContextWindowFor(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want ContextWindow
	}{
		{"no model", "", ContextWindow{200_000, SourceDefault}},
		{"[1m] suffix", "claude-opus-4-5[1m]", ContextWindow{1_000_000, SourceSuffix}},
		{"[1M] suffix", "modelo-novo[1M]", ContextWindow{1_000_000, SourceSuffix}},
		{"suffix beats a generation without 1M", "claude-sonnet-4-6[1m]", ContextWindow{1_000_000, SourceSuffix}},
		{"outside the generation pattern", "claude-3-5-sonnet-20241022", ContextWindow{200_000, SourceDefault}},
		{"unknown gpt-x", "gpt-x", ContextWindow{200_000, SourceDefault}},
		{"unknown claude-test-1", "claude-test-1", ContextWindow{200_000, SourceDefault}},
	}
	for _, tt := range tests {
		if got := ContextWindowFor(tt.id, nil, ReportedWindows{}); got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}

	table := []struct {
		id   string
		size int
	}{
		{"claude-opus-4-5-20251101", 200_000}, {"claude-opus-4-1-20250805", 200_000}, {"claude-opus-4-20250514", 200_000},
		{"claude-opus-4-5", 200_000}, {"claude-opus-4-6", 1_000_000}, {"claude-sonnet-4-5-20250929", 200_000},
		{"claude-sonnet-4-6", 1_000_000}, {"claude-haiku-4-5", 200_000}, {"claude-haiku-5-5", 1_000_000},
		{"claude-fable-1", 200_000}, {"claude-fable-5-1", 1_000_000}, {"claude-opus-5-5", 1_000_000},
		{"claude-sonnet-5-5", 1_000_000},
	}
	for _, tt := range table {
		if got := ContextWindowFor(tt.id, nil, ReportedWindows{}); got != (ContextWindow{tt.size, SourceTable}) {
			t.Errorf("table %s: got %+v", tt.id, got)
		}
	}
}

func TestModelIDBase(t *testing.T) {
	if ModelIDBase("a[1m]") != "a" || ModelIDBase("a") != "a" {
		t.Error("only the final suffix goes")
	}
}

func TestContextPercent(t *testing.T) {
	tests := []struct{ used, size, want float64 }{
		{50_000, 200_000, 25}, {300_000, 200_000, 150},
		{10, 0, 0}, {10, -1, 0}, {0, 100, 0}, {math.NaN(), 100, 0},
	}
	for _, tt := range tests {
		if got := ContextPercent(tt.used, tt.size); got != tt.want {
			t.Errorf("ContextPercent(%v, %v) = %v, want %v", tt.used, tt.size, got, tt.want)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeContextUsage(t *testing.T) {
	none := ReportedWindows{}
	tests := []struct {
		name     string
		id       string
		used     int
		reported ReportedWindows
		size     int
		source   ContextWindowSource
		percent  float64
	}{
		{"within the table window", "claude-opus-4-5", 50_000, none, 200_000, SourceTable, 25},
		{"above the table window: inferred 1M", "claude-opus-4-5", 215_000, none, 1_000_000, SourceInferred, 21.5},
		{"above the default, unknown model", "modelo-novo", 300_000, none, 1_000_000, SourceInferred, 30},
		{"above the default, no model", "", 300_000, none, 1_000_000, SourceInferred, 30},
		{"exactly at the window does not promote", "claude-opus-4-5", 200_000, none, 200_000, SourceTable, 100},
		{"never above 100% (suffix)", "claude-opus-4-5[1m]", 1_200_000, none, 1_000_000, SourceSuffix, 100},
		{"never above 100% (inferred)", "claude-opus-4-5", 1_500_000, none, 1_000_000, SourceInferred, 100},
		{"reported is exact: no promotion", "claude-opus-4-5", 250_000, ReportedWindows{Session: 200_000}, 200_000, SourceReported, 100},
		{"model report is exact too", "claude-opus-4-5", 250_000, ReportedWindows{Model: 200_000}, 200_000, SourceModel, 100},
	}
	for _, tt := range tests {
		got := ComputeContextUsage(tt.id, tt.used, nil, tt.reported)
		if got.Size != tt.size || got.Source != tt.source || !near(got.Percent, tt.percent) {
			t.Errorf("%s: got %+v, want %d %s %v", tt.name, got, tt.size, tt.source, tt.percent)
		}
	}
}

func TestLearnedWindows(t *testing.T) {
	learned := LearnedContextWindows{"claude-opus-4-5": 1_000_000}

	t.Run("applies to the id without suffix, as inferred", func(t *testing.T) {
		if got := ContextWindowFor("claude-opus-4-5", learned, ReportedWindows{}); got != (ContextWindow{1_000_000, SourceInferred}) {
			t.Errorf("got %+v", got)
		}
		u := ComputeContextUsage("claude-opus-4-5", 59_000, learned, ReportedWindows{})
		if u.Size != 1_000_000 || u.Source != SourceInferred || !near(u.Percent, 5.9) {
			t.Errorf("got %+v", u)
		}
	})

	t.Run("suffix beats learned; another model is unaffected", func(t *testing.T) {
		if got := ContextWindowFor("claude-opus-4-5[1m]", learned, ReportedWindows{}).Source; got != SourceSuffix {
			t.Errorf("got %s", got)
		}
		if got := ContextWindowFor("claude-sonnet-4-5", learned, ReportedWindows{}).Source; got != SourceTable {
			t.Errorf("got %s", got)
		}
	})

	t.Run("LearnContextWindow teaches only when use passes the table/default", func(t *testing.T) {
		if base, size, ok := LearnContextWindow("claude-opus-4-5", 279_000, ReportedWindows{}); !ok || base != "claude-opus-4-5" || size != 1_000_000 {
			t.Errorf("got %q %d %v", base, size, ok)
		}
		if base, size, ok := LearnContextWindow("modelo-novo", 250_000, ReportedWindows{}); !ok || base != "modelo-novo" || size != 1_000_000 {
			t.Errorf("got %q %d %v", base, size, ok)
		}
		for _, c := range []struct {
			id   string
			used int
		}{{"claude-opus-4-5", 200_000}, {"claude-opus-4-5[1m]", 1_200_000}, {"", 300_000}} {
			if _, _, ok := LearnContextWindow(c.id, c.used, ReportedWindows{}); ok {
				t.Errorf("%+v: want nothing", c)
			}
		}
	})
}

func TestReportedWindows(t *testing.T) {
	learned := LearnedContextWindows{"claude-opus-4-5": 1_000_000}

	t.Run("order: session, model, suffix, learned, table, default", func(t *testing.T) {
		id := "claude-opus-4-5[1m]"
		tests := []struct {
			id       string
			learned  LearnedContextWindows
			reported ReportedWindows
			want     ContextWindow
		}{
			{id, learned, ReportedWindows{Session: 500_000, Model: 300_000}, ContextWindow{500_000, SourceReported}},
			{id, learned, ReportedWindows{Model: 300_000}, ContextWindow{300_000, SourceModel}},
			{id, learned, ReportedWindows{}, ContextWindow{1_000_000, SourceSuffix}},
			{"claude-opus-4-5", learned, ReportedWindows{}, ContextWindow{1_000_000, SourceInferred}},
			{"claude-opus-4-5", nil, ReportedWindows{}, ContextWindow{200_000, SourceTable}},
			{"gpt-x", nil, ReportedWindows{}, ContextWindow{200_000, SourceDefault}},
		}
		for _, tt := range tests {
			if got := ContextWindowFor(tt.id, tt.learned, tt.reported); got != tt.want {
				t.Errorf("%s %+v: got %+v, want %+v", tt.id, tt.reported, got, tt.want)
			}
		}
	})

	t.Run("without a model nothing reported applies", func(t *testing.T) {
		if got := ContextWindowFor("", nil, ReportedWindows{Session: 500_000}).Source; got != SourceDefault {
			t.Errorf("got %s", got)
		}
	})

	t.Run("LearnContextWindow does not learn from a reported window", func(t *testing.T) {
		if _, _, ok := LearnContextWindow("claude-opus-4-5", 279_000, ReportedWindows{Session: 200_000}); ok {
			t.Error("session")
		}
		if _, _, ok := LearnContextWindow("claude-opus-4-5", 279_000, ReportedWindows{Model: 200_000}); ok {
			t.Error("model")
		}
		if _, _, ok := LearnContextWindow("claude-opus-4-5", 279_000, ReportedWindows{}); !ok {
			t.Error("none reported: want learned")
		}
	})
}
