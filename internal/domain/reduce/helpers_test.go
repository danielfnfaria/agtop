package reduce

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

const testdata = "../../../testdata"

// t0 is 2026-01-01T10:00:00.000Z.
const t0 int64 = 1767261600000

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, "fixtures", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

func normalize(lines ...string) []domain.TranscriptEvent {
	var out []domain.TranscriptEvent
	for _, line := range lines {
		out = append(out, transcript.NormalizeLine(line)...)
	}
	return out
}

func fixtureEvents(t *testing.T, name string) []domain.TranscriptEvent {
	t.Helper()
	return normalize(fixtureLines(t, name)...)
}

func mainSession(t *testing.T) domain.SessionState {
	t.Helper()
	return ReduceSessionEvents(NewSessionState("sess-0001"), fixtureEvents(t, "session-main"))
}

func agentOf(t *testing.T, s domain.SessionState, key string) domain.AgentState {
	t.Helper()
	a, ok := s.Agents[key]
	if !ok {
		t.Fatalf("agent %s missing", key)
	}
	return a
}

func toJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func serialized(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// matches reports whether every field of want (a JSON object) has the same value in got, like
// toMatchObject: fields of got missing from want are not compared.
func matches(got, want any) bool {
	wm, ok := want.(map[string]any)
	if !ok {
		return reflect.DeepEqual(got, want)
	}
	gm, ok := got.(map[string]any)
	if !ok {
		return false
	}
	for k, wv := range wm {
		gv, ok := gm[k]
		if !ok || !matches(gv, wv) {
			return false
		}
	}
	return true
}

func expectMatch(t *testing.T, got any, want map[string]any) {
	t.Helper()
	g := toJSON(t, got)
	w := toJSON(t, want)
	if !matches(g, w) {
		gs, _ := json.Marshal(g)
		ws, _ := json.Marshal(w)
		t.Errorf("no match:\n got  %s\n want %s", gs, ws)
	}
}

func expectEqual[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func histogram(s domain.SessionState) domain.ActivityHistogram { return s.ActivityHistogram }

func ptr[T any](v T) *T { return &v }
