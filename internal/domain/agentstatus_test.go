package domain

import (
	"reflect"
	"testing"
)

var allStatuses = []AgentStatus{StatusUnknown, StatusLaunched, StatusRunning, StatusHandedBack, StatusCompleted, StatusFailed}

func only(pred func(AgentStatus) bool) []AgentStatus {
	var out []AgentStatus
	for _, s := range allStatuses {
		if pred(s) {
			out = append(out, s)
		}
	}
	return out
}

func TestStatusPredicates(t *testing.T) {
	tests := []struct {
		name string
		pred func(AgentStatus) bool
		want []AgentStatus
	}{
		{"IsFinal", IsFinal, []AgentStatus{StatusCompleted, StatusFailed}},
		{"IsSettled", IsSettled, []AgentStatus{StatusHandedBack, StatusCompleted, StatusFailed}},
		{"IsActive", IsActive, []AgentStatus{StatusLaunched, StatusRunning}},
	}
	for _, tt := range tests {
		if got := only(tt.pred); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func agent(status AgentStatus, startedAt int64) AgentState {
	return AgentState{Status: status, StartedAt: startedAt}
}

func TestCompareByStart(t *testing.T) {
	early, late, none := agent("", 10), agent("", 20), agent("", 0)
	k := func(key string, a AgentState) KeyedAgent[AgentState] { return KeyedAgent[AgentState]{key, a} }
	tests := []struct {
		name string
		a, b KeyedAgent[AgentState]
		sign int
	}{
		{"startedAt ascending", k("b", early), k("a", late), -1},
		{"unknown start last", k("a", none), k("b", late), 1},
		{"ties by key", k("a", early), k("b", early), -1},
		{"unknown starts tie by key", k("b", none), k("a", none), 1},
		{"equal", k("a", none), k("a", none), 0},
	}
	for _, tt := range tests {
		got := CompareByStart(tt.a, tt.b)
		if (got < 0 && tt.sign >= 0) || (got > 0 && tt.sign <= 0) || (got == 0 && tt.sign != 0) {
			t.Errorf("%s: got %d, want sign %d", tt.name, got, tt.sign)
		}
	}
}

func TestSortedAgentsAndCounts(t *testing.T) {
	agents := map[string]AgentState{
		"b": agent(StatusRunning, 20),
		"a": agent(StatusLaunched, 10),
		"c": agent(StatusCompleted, 0),
		"d": agent(StatusHandedBack, 5),
	}
	var keys []string
	for _, ka := range SortedAgents(agents) {
		keys = append(keys, ka.Key)
	}
	if want := []string{"d", "a", "b", "c"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("order %v, want %v", keys, want)
	}
	if active, total := AgentCounts(agents); active != 2 || total != 4 {
		t.Errorf("counts %d/%d", active, total)
	}
	if active, total := AgentCounts(nil); active != 0 || total != 0 {
		t.Errorf("empty counts %d/%d", active, total)
	}
}
