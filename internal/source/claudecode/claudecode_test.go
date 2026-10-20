package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

func appendFile(t *testing.T, p, content string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

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
	src := New(deps, func(p string) (Tailer, error) { return tailer.New(p) })
	if src.Provider() != domain.ClaudeCode {
		t.Errorf("provider = %q", src.Provider())
	}
	if _, ok := src.RegistryWatch(profile); ok {
		t.Error("RegistryWatch must be absent before sessions/ exists")
	}

	writeFile(t, filepath.Join(profile.Dir, "sessions", "100.json"),
		`{"pid":100,"sessionId":"live-1","status":"busy","cwd":"/r/p"}`)
	proj := filepath.Join(profile.Dir, "projects", "-r-p")
	writeFile(t, filepath.Join(proj, "live-1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, "live-1", "subagents", "agent-q1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, "live-1", "subagents", "agent-q1.meta.json"), `{"agentType":"x"}`)

	wantRegistry := watcher.Target{Kind: watcher.KindSessions, Root: filepath.Join(profile.Dir, "sessions")}
	if target, ok := src.RegistryWatch(profile); !ok || target != wantRegistry {
		t.Errorf("RegistryWatch = %+v, %v", target, ok)
	}
	res, err := src.Discover(context.Background(), []config.ProfileDir{profile})
	if err != nil {
		t.Fatal(err)
	}
	live := res.Profiles[0].Live
	if len(live) != 1 || live[0].Files.TranscriptPath != filepath.Join(proj, "live-1.jsonl") {
		t.Fatalf("live = %+v", live)
	}

	agents := src.ListAgents(context.Background(), live[0].Files.SubagentsDir, map[string]string{})
	if len(agents) != 1 || agents[0].AgentID != "q1" {
		t.Fatalf("agents = %+v", agents)
	}
	if meta := src.ReadAgentMeta(agents[0].MetaPath); !meta.OK || meta.Meta.AgentType != "x" {
		t.Errorf("meta = %+v", meta)
	}
	if sig, ok := src.FileSignature(agents[0].TranscriptPath); !ok || sig == "" {
		t.Errorf("signature = %q, %v", sig, ok)
	}
	wantSession := watcher.Target{Kind: watcher.KindSession, TranscriptPath: "/t.jsonl", SubagentsDir: "/t/subagents"}
	if target, ok := src.SessionWatch("/t.jsonl", "/t/subagents"); !ok || target != wantSession {
		t.Errorf("SessionWatch = %+v, %v", target, ok)
	}
	if _, ok := src.SessionWatch("", ""); ok {
		t.Error("SessionWatch must be absent without paths")
	}
	reader, err := src.OpenTranscript(context.Background(), live[0].Files.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if got, err := reader.Read(context.Background()); err != nil || got.Reset || len(got.Events) != 1 {
		t.Errorf("first Read = %+v, %v", got, err)
	}
	appendFile(t, live[0].Files.TranscriptPath, "not json\n")
	if got, err := reader.Read(context.Background()); err != nil || len(got.Events) != 1 || got.Events[0].EventKind() != "parse_error" {
		t.Errorf("Read = %+v, %v", got, err)
	}
}

func TestForgetSessionDropsOnlyFoundLocations(t *testing.T) {
	deps, profile := setup(t)
	src := New(deps, func(p string) (Tailer, error) { return tailer.New(p) })
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

func TestFileReaderResetAndWarnings(t *testing.T) {
	deps, _ := setup(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "t.jsonl")
	src := New(deps, func(p string) (Tailer, error) { return tailer.New(p, tailer.WithMaxLineBytes(50)) })

	writeFile(t, file, "{\"a\":1}\n"+strings.Repeat("x", 100)+"\n")
	reader, err := src.OpenTranscript(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := reader.Read(context.Background())
	if err != nil || got.Reset || len(got.Events) != 1 ||
		!reflect.DeepEqual(got.Warnings, []string{"oversized line dropped: " + file}) {
		t.Fatalf("Read = %+v, %v", got, err)
	}

	// Truncated: the reader reports the reset and nothing else.
	writeFile(t, file, "")
	got, err = reader.Read(context.Background())
	if err != nil || !got.Reset || len(got.Events) != 0 || len(got.Warnings) != 0 {
		t.Errorf("truncated Read = %+v, %v", got, err)
	}

	// Rotated: removed and recreated.
	writeFile(t, file, "{\"a\":1}\n{\"b\":2}\n")
	if _, err := reader.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Read(context.Background())
	if err != nil || !got.Reset {
		t.Errorf("removed Read = %+v, %v", got, err)
	}
}
