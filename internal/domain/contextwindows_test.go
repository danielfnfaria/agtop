package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var ctxNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC).UnixMilli()

func ctxPayload(over map[string]any) map[string]any {
	p := map[string]any{
		"session_id":     "sess-1",
		"model":          map[string]any{"id": "claude-opus-5-5"},
		"context_window": map[string]any{"context_window_size": 1_000_000.0, "used_percentage": 12.0},
	}
	for k, v := range over {
		if v == nil {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	return p
}

func entry(at int64) ContextEntry { return ContextEntry{"claude-opus-5-5", 1_000_000, at} }

func ctxFile(sessions map[string]ContextEntry) ContextFile {
	return ContextFile{1, ctxNow, sessions}
}

func TestExtractContextWindow(t *testing.T) {
	t.Run("reads session, full model id and size", func(t *testing.T) {
		got, ok := ExtractContextWindow(ctxPayload(nil))
		if want := (ContextReport{"sess-1", "claude-opus-5-5", 1_000_000}); !ok || got != want {
			t.Errorf("got %+v, %v", got, ok)
		}
	})

	t.Run("keeps the variant suffix", func(t *testing.T) {
		got, _ := ExtractContextWindow(ctxPayload(map[string]any{"model": map[string]any{"id": "claude-opus-5-5[1m]"}}))
		if got.Model != "claude-opus-5-5[1m]" {
			t.Errorf("got %q", got.Model)
		}
	})

	size := func(v any) map[string]any {
		return ctxPayload(map[string]any{"context_window": map[string]any{"context_window_size": v}})
	}
	rejects := []struct {
		name string
		in   any
	}{
		{"not an object", "x"},
		{"nil", nil},
		{"no session_id", ctxPayload(map[string]any{"session_id": nil})},
		{"session_id with a slash", ctxPayload(map[string]any{"session_id": "../x"})},
		{"session_id not a string", ctxPayload(map[string]any{"session_id": 7.0})},
		{"no model", ctxPayload(map[string]any{"model": nil})},
		{"model id not a string", ctxPayload(map[string]any{"model": map[string]any{"id": 5.0}})},
		{"model id with spaces", ctxPayload(map[string]any{"model": map[string]any{"id": "a b"}})},
		{"model id too long", ctxPayload(map[string]any{"model": map[string]any{"id": strings.Repeat("m", 65)}})},
		{"no context_window", ctxPayload(map[string]any{"context_window": nil})},
		{"size missing", ctxPayload(map[string]any{"context_window": map[string]any{}})},
		{"size a string", size("200000")},
		{"size fractional", size(200000.5)},
		{"size too small", size(999.0)},
		{"size too large", size(100_000_001.0)},
	}
	for _, tt := range rejects {
		if got, ok := ExtractContextWindow(tt.in); ok {
			t.Errorf("%s: got %+v, want nothing", tt.name, got)
		}
	}
}

func TestParseContextFile(t *testing.T) {
	valid := func(sessions string) string {
		return fmt.Sprintf(`{"version":1,"updatedAt":%d,"sessions":%s}`, ctxNow, sessions)
	}
	entryJSON := fmt.Sprintf(`{"model":"claude-opus-5-5","size":1000000,"at":%d}`, ctxNow)

	t.Run("accepts a valid file (round trip)", func(t *testing.T) {
		in := valid(`{"a":` + entryJSON + `}`)
		parsed, ok := UnmarshalContextFile([]byte(in))
		if !ok || parsed.Sessions["a"] != entry(ctxNow) {
			t.Fatalf("got %+v, %v", parsed, ok)
		}
		out, err := MarshalContextFile(parsed)
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		_ = json.Unmarshal([]byte(in), &a)
		_ = json.Unmarshal(out, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("round trip: %s", out)
		}
	})

	t.Run("accepts a session id that looks like a prototype key", func(t *testing.T) {
		parsed, ok := UnmarshalContextFile([]byte(`{"version":1,"updatedAt":1,"sessions":{"__proto__":{"model":"m","size":200000,"at":1}}}`))
		if !ok || parsed.Sessions["__proto__"].Size != 200_000 {
			t.Errorf("got %+v, %v", parsed, ok)
		}
	})

	many := map[string]string{}
	for i := 0; i <= MaxContextSessions; i++ {
		many[fmt.Sprintf("s%d", i)] = entryJSON
	}
	manyJSON, _ := json.Marshal(many)
	rejects := []struct{ name, in string }{
		{"not an object", `[]`},
		{"wrong version", `{"version":2,"updatedAt":1000,"sessions":{}}`},
		{"unknown top-level key", `{"version":1,"updatedAt":1000,"sessions":{},"extra":1}`},
		{"bad updatedAt", `{"version":1,"updatedAt":"x","sessions":{}}`},
		{"sessions not an object", `{"version":1,"updatedAt":1000,"sessions":[]}`},
		{"unknown entry key", valid(`{"a":{"model":"m","size":200000,"at":1,"cwd":"/x"}}`)},
		{"bad entry model", valid(`{"a":{"model":"bad model","size":200000,"at":1}}`)},
		{"bad entry size", valid(`{"a":{"model":"m","size":5,"at":1}}`)},
		{"bad entry at", valid(`{"a":{"model":"m","size":200000,"at":-1}}`)},
		{"bad session id", valid(`{"a/b":` + entryJSON + `}`)},
		{"too many sessions", valid(string(manyJSON))},
	}
	for _, tt := range rejects {
		if _, ok := UnmarshalContextFile([]byte(tt.in)); ok {
			t.Errorf("%s: want rejected", tt.name)
		}
	}
}

func TestUpsertContextEntry(t *testing.T) {
	report := ContextReport{"a", "claude-opus-5-5", 1_000_000}
	keys := func(f ContextFile) []string {
		out := make([]string, 0, len(f.Sessions))
		for k := range f.Sessions {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}

	t.Run("creates the file", func(t *testing.T) {
		next, ok := UpsertContextEntry(nil, report, ctxNow)
		if !ok || next.UpdatedAt != ctxNow || next.Sessions["a"] != entry(ctxNow) {
			t.Errorf("got %+v, %v", next, ok)
		}
	})

	t.Run("unchanged and recent: nothing to write; old or changed: rewritten", func(t *testing.T) {
		cur := ctxFile(map[string]ContextEntry{"a": entry(ctxNow)})
		if _, ok := UpsertContextEntry(&cur, report, ctxNow+ContextRewriteAfterMs-1); ok {
			t.Error("recent: want nothing")
		}
		if next, ok := UpsertContextEntry(&cur, report, ctxNow+ContextRewriteAfterMs); !ok || next.Sessions["a"].At != ctxNow+ContextRewriteAfterMs {
			t.Errorf("old: got %+v, %v", next, ok)
		}
		if next, ok := UpsertContextEntry(&cur, ContextReport{"a", report.Model, 200_000}, ctxNow+1); !ok || next.Sessions["a"].Size != 200_000 {
			t.Errorf("size: got %+v, %v", next, ok)
		}
		if next, ok := UpsertContextEntry(&cur, ContextReport{"a", "other", report.Size}, ctxNow+1); !ok || next.Sessions["a"].Model != "other" {
			t.Errorf("model: got %+v, %v", next, ok)
		}
		future := ctxFile(map[string]ContextEntry{"a": entry(ctxNow + 5_000)})
		if _, ok := UpsertContextEntry(&future, report, ctxNow); !ok {
			t.Error("entry from the future: want rewritten")
		}
	})

	t.Run("keeps the other sessions", func(t *testing.T) {
		cur := ctxFile(map[string]ContextEntry{"b": entry(ctxNow - 1000)})
		next, _ := UpsertContextEntry(&cur, report, ctxNow)
		if want := []string{"a", "b"}; !reflect.DeepEqual(keys(next), want) {
			t.Errorf("got %v", keys(next))
		}
	})

	t.Run("drops entries older than 30 days", func(t *testing.T) {
		cur := ctxFile(map[string]ContextEntry{
			"old":  entry(ctxNow - ContextEntryMaxAgeMs - 1),
			"edge": entry(ctxNow - ContextEntryMaxAgeMs),
		})
		next, _ := UpsertContextEntry(&cur, report, ctxNow)
		if want := []string{"a", "edge"}; !reflect.DeepEqual(keys(next), want) {
			t.Errorf("got %v", keys(next))
		}
	})

	t.Run("keeps at most 64 sessions, dropping the oldest", func(t *testing.T) {
		many := map[string]ContextEntry{}
		for i := 0; i < MaxContextSessions; i++ {
			many[fmt.Sprintf("s%d", i)] = entry(ctxNow - 1000*int64(i+1))
		}
		cur := ctxFile(many)
		next, _ := UpsertContextEntry(&cur, report, ctxNow)
		last := fmt.Sprintf("s%d", MaxContextSessions-1)
		if len(next.Sessions) != MaxContextSessions {
			t.Errorf("size %d", len(next.Sessions))
		}
		if _, ok := next.Sessions["a"]; !ok {
			t.Error("the written entry must survive")
		}
		if _, ok := next.Sessions[last]; ok {
			t.Error("oldest must go")
		}
		if _, ok := next.Sessions["s0"]; !ok {
			t.Error("newest other must stay")
		}
		b, _ := MarshalContextFile(next)
		if _, ok := UnmarshalContextFile(b); !ok {
			t.Error("result must parse")
		}
	})
}

func TestContextReports(t *testing.T) {
	reports := NewContextReports(ctxFile(map[string]ContextEntry{
		"a": {"claude-opus-5-5", 1_000_000, ctxNow},
		"b": {"claude-opus-5-5", 500_000, ctxNow + 5},
		"c": {"claude-haiku-4-5", 200_000, ctxNow + 9},
	}))

	t.Run("ForSession: the own entry, only for the same full model id", func(t *testing.T) {
		tests := []struct {
			session, model string
			want           int
		}{
			{"a", "claude-opus-5-5", 1_000_000},
			{"a", "claude-opus-5-5[1m]", 0},
			{"a", "claude-haiku-4-5", 0},
			{"zz", "claude-opus-5-5", 0},
			{"a", "", 0},
		}
		for _, tt := range tests {
			if got := reports.ForSession(tt.session, tt.model); got != tt.want {
				t.Errorf("ForSession(%q, %q) = %d, want %d", tt.session, tt.model, got, tt.want)
			}
		}
	})

	t.Run("ForModel: the most recent entry of the model", func(t *testing.T) {
		tests := []struct {
			model string
			want  int
		}{
			{"claude-opus-5-5", 500_000}, {"claude-opus-5-5[1m]", 0}, {"claude-haiku-4-5", 200_000},
			{"claude-sonnet-4-5", 0}, {"", 0},
		}
		for _, tt := range tests {
			if got := reports.ForModel(tt.model); got != tt.want {
				t.Errorf("ForModel(%q) = %d, want %d", tt.model, got, tt.want)
			}
		}
	})

	t.Run("NoReports reports nothing", func(t *testing.T) {
		if NoReports.ForSession("a", "m") != 0 || NoReports.ForModel("m") != 0 {
			t.Error("want 0")
		}
	})
}

func TestSameContextWindows(t *testing.T) {
	e := func(model string, size int, at int64) ContextEntry { return ContextEntry{model, size, at} }
	a := ctxFile(map[string]ContextEntry{
		"x": e("claude-opus-5-5", 1_000_000, ctxNow),
		"y": e("claude-haiku-4-5", 200_000, ctxNow+5),
	})

	t.Run("ignores order, at and updatedAt", func(t *testing.T) {
		b := ctxFile(map[string]ContextEntry{
			"y": e("claude-haiku-4-5", 200_000, ctxNow+50),
			"x": e("claude-opus-5-5", 1_000_000, ctxNow+40),
		})
		b.UpdatedAt = ctxNow + 99
		if !SameContextWindows(a, b) {
			t.Error("want same")
		}
	})

	diffs := []struct {
		name string
		b    ContextFile
	}{
		{"missing session", ctxFile(map[string]ContextEntry{"x": e("claude-opus-5-5", 1_000_000, ctxNow)})},
		{"different size", ctxFile(map[string]ContextEntry{"x": e("claude-opus-5-5", 500_000, ctxNow), "y": e("claude-haiku-4-5", 200_000, ctxNow)})},
		{"different model", ctxFile(map[string]ContextEntry{"x": e("claude-opus-5-5[1m]", 1_000_000, ctxNow), "y": e("claude-haiku-4-5", 200_000, ctxNow)})},
	}
	for _, tt := range diffs {
		if SameContextWindows(a, tt.b) {
			t.Errorf("%s: want different", tt.name)
		}
	}

	t.Run("detects a change of which entry is the most recent of a model", func(t *testing.T) {
		p := ctxFile(map[string]ContextEntry{"x": e("claude-opus-5-5", 1, ctxNow), "y": e("claude-opus-5-5", 2, ctxNow+1)})
		q := ctxFile(map[string]ContextEntry{"x": e("claude-opus-5-5", 1, ctxNow+2), "y": e("claude-opus-5-5", 2, ctxNow+1)})
		if SameContextWindows(p, q) {
			t.Error("want different")
		}
	})
}
