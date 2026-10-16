package domain

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

const B = ActivityBucketMs

var t0 = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC).UnixMilli()

func assistant(model string, toolUses ...ToolUseInfo) AssistantMessageEvent {
	return AssistantMessageEvent{EventBase: EventBase{Timestamp: t0}, Model: model, ToolUses: toolUses}
}

func sortedKeys(h ActivityHistogram) []int64 {
	keys := []int64{}
	for k := range h {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sameHistogram(a, b ActivityHistogram) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func TestActivityBucket(t *testing.T) {
	if B != 30_000 || ActivityWindowBuckets != 120 {
		t.Fatal("constants changed")
	}
	tests := []struct{ in, want int64 }{
		{t0, t0}, {t0 + 29_999, t0}, {t0 + 30_000, t0 + B}, {t0 - 1, t0 - B},
	}
	for _, tt := range tests {
		if got := ActivityBucket(tt.in); got != tt.want {
			t.Errorf("ActivityBucket(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestActivityWeight(t *testing.T) {
	three := []ToolUseInfo{{ID: "a", Name: "Read"}, {ID: "b", Name: "Grep"}, {ID: "c", Name: "Bash"}}
	tests := []struct {
		name        string
		event       AssistantMessageEvent
		newResponse bool
		newToolUses int
		want        int
	}{
		{"synthetic weighs 0", assistant(SyntheticModel), true, 0, 0},
		{"synthetic with tools weighs 0", assistant(SyntheticModel, ToolUseInfo{ID: "t", Name: "Bash"}), true, 1, 0},
		{"new response without tools", assistant("claude-x"), true, 0, 1},
		{"repeated response", assistant(""), false, 0, 0},
		{"one per tool plus new response", assistant("", three...), true, 3, 4},
		{"one per tool, repeated response", assistant("", three...), false, 3, 3},
		{"only the new tools count", assistant("", three...), true, 1, 2},
		{"no new tools, repeated", assistant("", three...), false, 0, 0},
		{"synthetic with 3 new tools", assistant(SyntheticModel, three...), true, 3, 0},
	}
	for _, tt := range tests {
		if got := ActivityWeight(tt.event, tt.newResponse, tt.newToolUses); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestAddActivity(t *testing.T) {
	t.Run("adds to the bucket without mutating the input", func(t *testing.T) {
		empty := ActivityHistogram{}
		a := AddActivity(empty, t0+5_000, 2)
		b := AddActivity(a, t0+10_000, 1)
		if len(empty) != 0 || !reflect.DeepEqual(a, ActivityHistogram{t0: 2}) || !reflect.DeepEqual(b, ActivityHistogram{t0: 3}) {
			t.Errorf("empty=%v a=%v b=%v", empty, a, b)
		}
		if sameHistogram(a, b) {
			t.Error("want a new map")
		}
	})

	t.Run("weight <= 0 returns the same map", func(t *testing.T) {
		h := ActivityHistogram{t0: 1}
		if !sameHistogram(AddActivity(h, t0, 0), h) {
			t.Error("want the same map")
		}
	})

	t.Run("prunes what falls outside the window of the newest bucket", func(t *testing.T) {
		h := AddActivity(ActivityHistogram{}, t0, 1)
		h = AddActivity(h, t0+(ActivityWindowBuckets-1)*B, 1)
		if want := []int64{t0, t0 + 119*B}; !reflect.DeepEqual(sortedKeys(h), want) {
			t.Errorf("got %v, want %v", sortedKeys(h), want)
		}
		h = AddActivity(h, t0+ActivityWindowBuckets*B, 1)
		if want := []int64{t0 + 119*B, t0 + 120*B}; !reflect.DeepEqual(sortedKeys(h), want) {
			t.Errorf("got %v, want %v", sortedKeys(h), want)
		}
	})

	t.Run("an old event outside the window does not enter", func(t *testing.T) {
		h := AddActivity(ActivityHistogram{t0 + 200*B: 1}, t0, 1)
		if want := (ActivityHistogram{t0 + 200*B: 1}); !reflect.DeepEqual(h, want) {
			t.Errorf("got %v, want %v", h, want)
		}
	})
}

func TestPruneActivity(t *testing.T) {
	h := ActivityHistogram{t0: 1, t0 + B: 2, t0 + 2*B: 3}
	if !sameHistogram(PruneActivity(h, t0), h) {
		t.Error("nothing pruned: want the same map")
	}
	p := PruneActivity(h, t0+B)
	if want := (ActivityHistogram{t0 + B: 2, t0 + 2*B: 3}); !reflect.DeepEqual(p, want) {
		t.Errorf("got %v, want %v", p, want)
	}
	if len(h) != 3 {
		t.Error("input mutated")
	}
}

func TestMergeActivity(t *testing.T) {
	a := ActivityHistogram{t0: 1, t0 + B: 2}
	b := ActivityHistogram{t0 + B: 3, t0 + 2*B: 4}
	want := ActivityHistogram{t0: 1, t0 + B: 5, t0 + 2*B: 4}
	if m := MergeActivity(a, b); !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
	if a[t0+B] != 2 || b[t0+B] != 3 {
		t.Error("input mutated")
	}
	if !sameHistogram(MergeActivity(a, ActivityHistogram{}), a) || !sameHistogram(MergeActivity(ActivityHistogram{}, b), b) {
		t.Error("empty side: want the other map")
	}
}

func TestActivityLevel(t *testing.T) {
	table := [][2]int{{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 3}, {7, 3}, {8, 4}, {15, 4}, {16, 5}, {31, 5}, {32, 6}, {63, 6}, {64, 7}, {1000, 7}, {-1, 0}}
	for _, tt := range table {
		if got := ActivityLevel(tt[0]); got != tt[1] {
			t.Errorf("ActivityLevel(%d) = %d, want %d", tt[0], got, tt[1])
		}
	}
}

func TestActivityCells(t *testing.T) {
	prof := func(label string, h ActivityHistogram) ProfileHistogram { return ProfileHistogram{label, h} }

	t.Run("no data: empty cells without profile", func(t *testing.T) {
		got := ActivityCells([]ProfileHistogram{prof("a", ActivityHistogram{})}, t0, 4)
		if !reflect.DeepEqual(got, make([]ActivityCell, 4)) {
			t.Errorf("got %v", got)
		}
		if got := ActivityCells(nil, t0, 2); !reflect.DeepEqual(got, make([]ActivityCell, 2)) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("oldest first; level from the sum; dominant profile", func(t *testing.T) {
		by := []ProfileHistogram{
			prof("a", ActivityHistogram{t0 - 2*B: 1, t0: 1}),
			prof("b", ActivityHistogram{t0: 5}),
		}
		want := []ActivityCell{{1, "a"}, {}, {3, "b"}}
		if got := ActivityCells(by, t0, 3); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("tie goes to the first profile", func(t *testing.T) {
		by := []ProfileHistogram{prof("b", ActivityHistogram{t0: 2}), prof("a", ActivityHistogram{t0: 2})}
		if got, want := ActivityCells(by, t0, 1), []ActivityCell{{3, "b"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("buckets after nowBucket and before the window are left out", func(t *testing.T) {
		by := []ProfileHistogram{prof("a", ActivityHistogram{t0 - 2*B: 9, t0: 1, t0 + B: 64})}
		if got, want := ActivityCells(by, t0, 2), []ActivityCell{{}, {1, "a"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if got, want := ActivityCells(by, t0+1_000, 1), []ActivityCell{{1, "a"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("misaligned: got %v, want %v", got, want)
		}
	})
}
