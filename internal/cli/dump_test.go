package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
)

const goldenDir = "../../testdata/golden/dump"

// volatileDumpFields change on every run (time of the scan) and are left out of the comparison.
var volatileDumpFields = []string{"scannedAt"}

type dumpCaseSession struct {
	SessionID  string            `json:"sessionId"`
	PID        string            `json:"pid"`
	Registry   map[string]any    `json:"registry"`
	Transcript string            `json:"transcript"`
	Agents     map[string]string `json:"agents"`
}

type dumpCaseProfile struct {
	Label         string            `json:"label"`
	Sessions      []dumpCaseSession `json:"sessions"`
	Files         map[string]string `json:"files"`
	NoSessionsDir bool              `json:"noSessionsDir"`
}

// buildDumpProfiles lays out the profiles of a case under root, as the golden generator did.
func buildDumpProfiles(t *testing.T, root string, profiles []dumpCaseProfile) []config.ProfileDir {
	t.Helper()
	write := func(p string, data []byte) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixture := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("../../testdata/fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	now := time.Now().UnixMilli()
	var out []config.ProfileDir
	for _, p := range profiles {
		dir := filepath.Join(root, p.Label)
		project := filepath.Join(dir, "projects", "-home-dev-projects-demo-app")
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		if !p.NoSessionsDir {
			if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range p.Sessions {
			pid := os.Getpid()
			if s.PID == "parent" {
				pid = os.Getppid()
			}
			reg := map[string]any{"pid": pid, "sessionId": s.SessionID, "cwd": "/home/dev/projects/demo-app",
				"startedAt": now, "updatedAt": now}
			for k, v := range s.Registry {
				reg[k] = v
			}
			data, _ := json.Marshal(reg)
			write(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"), data)
			if s.Transcript != "" {
				write(filepath.Join(project, s.SessionID+".jsonl"), fixture(s.Transcript))
			}
			for id, f := range s.Agents {
				write(filepath.Join(project, s.SessionID, "subagents", "agent-"+id+".jsonl"), fixture(f))
			}
		}
		for name, content := range p.Files {
			write(filepath.Join(dir, "sessions", name), []byte(content))
		}
		out = append(out, config.ProfileDir{Label: p.Label, Dir: dir, Source: config.SourceConfig})
	}
	return out
}

func decodeDump(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid dump JSON: %v", err)
	}
	for _, f := range volatileDumpFields {
		delete(v, f)
	}
	return v
}

func TestDumpMatchesGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(goldenDir, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string][]dumpCaseProfile
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profiles := buildDumpProfiles(t, root, cases[name])
			var out bytes.Buffer
			if code := Dump(profiles, &out); code != 0 {
				t.Fatalf("exit code %d", code)
			}
			want, err := os.ReadFile(filepath.Join(goldenDir, name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			got := decodeDump(t, out.Bytes())
			if !reflect.DeepEqual(got, decodeDump(t, want)) {
				t.Errorf("dump differs from the golden\ngot:\n%s\nwant:\n%s", out.String(), want)
			}
			if _, ok := decodeDumpRaw(t, out.Bytes())["scannedAt"]; !ok {
				t.Error("scannedAt missing")
			}
		})
	}
}

func decodeDumpRaw(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
