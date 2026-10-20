package reduce

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// The goldens hold the final state of each fixture reduced by the TypeScript implementation:
// a session transcript through reduceSessionEvents, an agent transcript (one whose envelope has
// an agentId) through reduceSessionAgentEvents, both from createSessionState("golden"). Maps
// are objects, sets arrays, and pendingTools an array of {id, ...tool} in insertion order: the
// same JSON domain.SessionState marshals to.
func readGolden(t *testing.T, name string) any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, "golden", "state", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	return want
}

func expectGolden(t *testing.T, s domain.SessionState, name string) {
	t.Helper()
	if got := toJSON(t, s); !reflect.DeepEqual(got, readGolden(t, name)) {
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("state differs from the golden %s:\n%s", name, g)
	}
}

func TestGoldenState(t *testing.T) {
	for _, name := range []string{"session-main", "session-resume", "subagent-handback", "subagent-resume"} {
		t.Run(name, func(t *testing.T) {
			events := fixtureEvents(t, name)
			agentID := ""
			for _, e := range events {
				if env, ok := e.(domain.EnvelopeInfoEvent); ok && env.AgentID != "" {
					agentID = env.AgentID
					break
				}
			}
			s := NewSessionState("golden")
			if agentID != "" {
				s = SessionAgentEvents(s, agentID, events)
			} else {
				s = SessionEvents(s, events)
			}
			expectGolden(t, s, name)
		})
	}
}

// TestGoldenStateReadOrders reads a session and one of its agent transcripts in both orders.
func TestGoldenStateReadOrders(t *testing.T) {
	pairs := []struct{ session, agent, agentID string }{
		{"session-main", "subagent-handback", "a-bg-001"},
		{"session-resume", "subagent-resume", "a-res-001"},
	}
	for _, p := range pairs {
		name := p.session + "+" + p.agent
		main, sub := fixtureEvents(t, p.session), fixtureEvents(t, p.agent)
		t.Run(name+"/session-first", func(t *testing.T) {
			s := SessionAgentEvents(SessionEvents(NewSessionState("golden"), main), p.agentID, sub)
			expectGolden(t, s, name+".session-first")
		})
		t.Run(name+"/agent-first", func(t *testing.T) {
			s := SessionEvents(SessionAgentEvents(NewSessionState("golden"), p.agentID, sub), main)
			expectGolden(t, s, name+".agent-first")
		})
	}
}
