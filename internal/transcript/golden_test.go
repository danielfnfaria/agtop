package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

const testdata = "../../testdata"

var fixtureNames = []string{"session-main", "session-resume", "subagent-handback", "subagent-resume"}

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, "fixtures", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

func fixtureEvents(t *testing.T, name string) []domain.TranscriptEvent {
	t.Helper()
	var out []domain.TranscriptEvent
	for _, line := range fixtureLines(t, name) {
		out = append(out, NormalizeLine(line)...)
	}
	return out
}

// eventJSON is the event as the TypeScript implementation serializes it: its fields plus the
// "kind" discriminator, which Go keeps in the type (EventKind).
func eventJSON(t *testing.T, event domain.TranscriptEvent) map[string]any {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["kind"] = event.EventKind()
	return m
}

func eventsJSON(t *testing.T, events []domain.TranscriptEvent) []any {
	t.Helper()
	out := []any{}
	for _, e := range events {
		out = append(out, eventJSON(t, e))
	}
	return out
}

func TestGoldenEvents(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(testdata, "golden", "events", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want []any
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			got := eventsJSON(t, fixtureEvents(t, name))
			if len(got) != len(want) {
				t.Fatalf("got %d events, want %d", len(got), len(want))
			}
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					g, _ := json.Marshal(got[i])
					w, _ := json.Marshal(want[i])
					t.Errorf("event %d:\n got  %s\n want %s", i, g, w)
				}
			}
		})
	}
}
