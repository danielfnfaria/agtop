package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (discovery.Deps, config.ProfileDir) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := config.ProfileDir{Label: "claude", Dir: filepath.Join(root, "claude"), Source: config.SourceConfig}
	deps := discovery.NewDeps()
	deps.IsPidAlive = func(pid int) bool { return pid == 100 }
	deps.UptimeMs = func() int64 { return 0 }
	return deps, profile
}

func TestSourceDiscoversAndListsAgents(t *testing.T) {
	deps, profile := setup(t)
	src := New(deps)
	if src.Provider() != domain.ClaudeCode {
		t.Errorf("provider = %q", src.Provider())
	}
	if _, ok := src.RegistryRoot(profile); ok {
		t.Error("RegistryRoot must be absent before sessions/ exists")
	}

	writeFile(t, filepath.Join(profile.Dir, "sessions", "100.json"),
		`{"pid":100,"sessionId":"live-1","status":"busy","cwd":"/r/p"}`)
	proj := filepath.Join(profile.Dir, "projects", "-r-p")
	writeFile(t, filepath.Join(proj, "live-1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, "live-1", "subagents", "agent-q1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, "live-1", "subagents", "agent-q1.meta.json"), `{"agentType":"x"}`)

	if root, ok := src.RegistryRoot(profile); !ok || root != filepath.Join(profile.Dir, "sessions") {
		t.Errorf("RegistryRoot = %q, %v", root, ok)
	}
	res, err := src.Discover([]config.ProfileDir{profile})
	if err != nil {
		t.Fatal(err)
	}
	live := res.Profiles[0].Live
	if len(live) != 1 || live[0].Files.TranscriptPath != filepath.Join(proj, "live-1.jsonl") {
		t.Fatalf("live = %+v", live)
	}

	agents := src.ListAgents(live[0].Files.SubagentsDir, map[string]string{})
	if len(agents) != 1 || agents[0].AgentID != "q1" {
		t.Fatalf("agents = %+v", agents)
	}
	if meta := src.ReadAgentMeta(agents[0].MetaPath); !meta.OK || meta.Meta.AgentType != "x" {
		t.Errorf("meta = %+v", meta)
	}
	if sig, ok := src.FileSignature(agents[0].TranscriptPath); !ok || sig == "" {
		t.Errorf("signature = %q, %v", sig, ok)
	}
	if got := src.ParseLine("not json"); len(got) != 1 || got[0].EventKind() != "parse_error" {
		t.Errorf("ParseLine = %+v", got)
	}
}

func TestForgetSessionDropsOnlyFoundLocations(t *testing.T) {
	deps, profile := setup(t)
	src := New(deps)
	found := discovery.LocateCacheKey(profile.Dir, "found")
	missing := discovery.LocateCacheKey(profile.Dir, "missing")
	deps.LocateCache.Set(found, discovery.LocateEntry{Kind: discovery.LocateFound, Project: "-p"})
	deps.LocateCache.Set(missing, discovery.LocateEntry{Kind: discovery.LocateMissing, Until: 5})

	src.ForgetSession(profile, "found")
	src.ForgetSession(profile, "missing")

	if got := deps.LocateCache.Keys(); !reflect.DeepEqual(got, []string{missing}) {
		t.Errorf("keys = %v", got)
	}
}
