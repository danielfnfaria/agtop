package discovery

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
)

func TestReadAgentMetaValid(t *testing.T) {
	file := filepath.Join(makeTmp(t), "agent-a1.meta.json")
	writeJSON(t, file, map[string]any{
		"agentType": esc + "[1mexecutor", "description": "Synthetic task", "toolUseId": "toolu_1",
		"spawnDepth": 1, "model": "claude-haiku-4-5", "stoppedByUser": true, "prompt": "SECRET",
	})
	depth := 1
	want := AgentMetaResult{OK: true, Meta: domain.AgentMeta{
		AgentType: "executor", Description: "Synthetic task", ToolUseID: "toolu_1", SpawnDepth: &depth,
		Model: "claude-haiku-4-5", StoppedByUser: true,
	}}
	if got := ReadAgentMeta(OSFS{}, file); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestReadAgentMetaFailures(t *testing.T) {
	root := makeTmp(t)
	write := func(name, content string) string {
		p := filepath.Join(root, name)
		writeFile(t, p, content)
		return p
	}
	tests := []struct {
		name, file, want string
	}{
		{"invalid json", write("broken.meta.json", `{"agentType": "SECRET`), "INVALID_JSON"},
		{"not an object", write("array.meta.json", "null"), "NOT_OBJECT"},
		{"too large", write("big.meta.json", `{"description":"`+strings.Repeat("x", MaxAgentMetaBytes)+`"}`), "TOO_LARGE"},
		{"missing", filepath.Join(root, "nope.meta.json"), "ENOENT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReadAgentMeta(OSFS{}, tt.file)
			if got.OK || got.Code != tt.want || !reflect.DeepEqual(got.Meta, domain.AgentMeta{}) {
				t.Errorf("got %+v, want code %s", got, tt.want)
			}
		})
	}
}

type failingFS struct {
	OSFS
	err error
}

func (f failingFS) ReadFile(string, int) (string, error) { return "", f.err }

func TestReadAgentMetaErrorWithoutErrno(t *testing.T) {
	got := ReadAgentMeta(failingFS{err: errors.New("SECRET")}, "/x/agent-a.meta.json")
	if got.OK || got.Code != "UNKNOWN" {
		t.Errorf("got %+v", got)
	}
}
