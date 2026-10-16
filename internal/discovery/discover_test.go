package discovery

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
)

func discoverDeps() Deps {
	alive := map[int]bool{100: true, 101: true, 200: true, 300: true}
	return Deps{
		FS:                 OSFS{},
		IsPidAlive:         func(pid int) bool { return alive[pid] },
		Now:                func() int64 { return 1_234 },
		UptimeMs:           func() int64 { return 0 },
		LocateCache:        NewCache[LocateEntry](),
		InactiveFilesCache: NewCache[SessionFiles](),
	}
}

func TestDiscoverLiveSessions(t *testing.T) {
	root := makeTmp(t)
	a := makeProfile(t, root, "work")
	b := makeProfile(t, root, "home")
	c := makeProfile(t, root, "old") // without sessions/

	// Profile a: live with transcript, sub-agents and title; dead; non-interactive.
	projA := filepath.Join(a.Dir, "projects", "-repo")
	writeJSON(t, filepath.Join(a.Dir, "sessions", "100.json"), registryEntry(100, "live-1", nil))
	writeFile(t, filepath.Join(projA, "live-1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(projA, "live-1", "subagents", "agent-q1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(projA, "live-1", "subagents", "agent-q1.meta.json"), map[string]any{})
	writeJSON(t, filepath.Join(projA, "live-1", "custom-title.json"), map[string]any{"customTitle": "Titled"})
	writeJSON(t, filepath.Join(a.Dir, "sessions", "150.json"), registryEntry(150, "dead-1", nil))
	writeFile(t, filepath.Join(projA, "dead-1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(a.Dir, "sessions", "101.json"), registryEntry(101, "print-1", map[string]any{"kind": "sdk-cli", "status": "busy"}))
	writeFile(t, filepath.Join(a.Dir, "sessions", "100.deadbeef.key"), "synthetic")
	writeFile(t, filepath.Join(a.Dir, "sessions", "999.json"), "{broken")

	// Profile b: same sessionId on two live pids (the most recent stays) + missing transcript.
	writeJSON(t, filepath.Join(b.Dir, "sessions", "200.json"), registryEntry(200, "live-1", map[string]any{"updatedAt": 2_000}))
	writeJSON(t, filepath.Join(b.Dir, "sessions", "300.json"), registryEntry(300, "live-1", map[string]any{"updatedAt": 1_000}))

	result := DiscoverLiveSessions([]config.ProfileDir{a, b, c}, discoverDeps())

	if result.ScannedAt != 1_234 || len(result.Profiles) != 3 {
		t.Fatalf("result = %+v", result)
	}
	ra, rb, rc := result.Profiles[0], result.Profiles[1], result.Profiles[2]

	var keys []string
	for _, s := range ra.Live {
		keys = append(keys, s.Key)
	}
	if !reflect.DeepEqual(keys, []string{"work:live-1", "work:print-1"}) {
		t.Fatalf("live keys = %v", keys)
	}
	live1, print1 := ra.Live[0], ra.Live[1]
	wantFiles := SessionFiles{
		TranscriptPath: filepath.Join(projA, "live-1.jsonl"),
		SessionDir:     filepath.Join(projA, "live-1"),
		SubagentsDir:   filepath.Join(projA, "live-1", "subagents"),
		CustomTitle:    "Titled",
	}
	if live1.Profile != a || live1.PID != 100 || !live1.Alive || live1.Activity != ActivityIdle ||
		live1.Kind != "interactive" || live1.Files != wantFiles {
		t.Errorf("live-1 = %+v", live1)
	}
	if print1.Kind != "sdk-cli" || print1.Activity != ActivityBusy || print1.Files != (SessionFiles{}) {
		t.Errorf("print-1 = %+v", print1)
	}
	if len(ra.Dead) != 1 || ra.Dead[0].Key != "work:dead-1" || ra.Dead[0].Alive ||
		ra.Dead[0].Reason != InactiveDeadPid || ra.Dead[0].Files.TranscriptPath != filepath.Join(projA, "dead-1.jsonl") {
		t.Errorf("dead = %+v", ra.Dead)
	}
	wantDiag := Diagnostics{SkippedEntries: 1, Issues: []RegistryIssue{{File: "999.json", Reason: IssueInvalidJSON}}}
	if !reflect.DeepEqual(ra.Diagnostics, wantDiag) {
		t.Errorf("diagnostics = %+v", ra.Diagnostics)
	}

	agents := ListSubagents(OSFS{}, live1.Files.SubagentsDir, nil)
	wantAgents := []SubagentFiles{{
		AgentID:        "q1",
		TranscriptPath: filepath.Join(live1.Files.SubagentsDir, "agent-q1.jsonl"),
		MetaPath:       filepath.Join(live1.Files.SubagentsDir, "agent-q1.meta.json"),
	}}
	if !reflect.DeepEqual(agents, wantAgents) {
		t.Errorf("agents = %+v", agents)
	}

	// The same sessionId in different profiles: distinct keys.
	if len(rb.Live) != 1 || rb.Live[0].Key != "home:live-1" || rb.Live[0].PID != 200 || rb.Live[0].Files.TranscriptPath != "" {
		t.Errorf("home live = %+v", rb.Live)
	}
	if len(rb.Dead) != 1 || rb.Dead[0].Key != "home:live-1" || rb.Dead[0].PID != 300 || !rb.Dead[0].Alive ||
		rb.Dead[0].Reason != InactiveDuplicate {
		t.Errorf("home dead = %+v", rb.Dead)
	}

	if rc.Profile != c || len(rc.Live) != 0 || len(rc.Dead) != 0 ||
		!reflect.DeepEqual(rc.Diagnostics, Diagnostics{SessionsDirMissing: true, Issues: []RegistryIssue{}}) {
		t.Errorf("old = %+v", rc)
	}
}

func TestDiscoverReusedPidAfterBoot(t *testing.T) {
	root := makeTmp(t)
	a := makeProfile(t, root, "work")
	sessions := filepath.Join(a.Dir, "sessions")
	boot := int64(1_700_000_050_000) // after registryEntry's startedAt
	writeJSON(t, filepath.Join(sessions, "100.json"), registryEntry(100, "stale", nil))
	writeJSON(t, filepath.Join(sessions, "101.json"), registryEntry(101, "fresh", map[string]any{"startedAt": boot + 1_000}))
	d := discoverDeps()
	d.Now = func() int64 { return boot + 60_000 }
	d.UptimeMs = func() int64 { return 60_000 }
	asked := map[int]bool{}
	inner := d.IsPidAlive
	d.IsPidAlive = func(pid int) bool { asked[pid] = true; return inner(pid) }

	ra := DiscoverLiveSessions([]config.ProfileDir{a}, d).Profiles[0]

	if len(ra.Live) != 1 || ra.Live[0].SessionID != "fresh" {
		t.Errorf("live = %+v", ra.Live)
	}
	if len(ra.Dead) != 1 || ra.Dead[0].SessionID != "stale" || ra.Dead[0].Alive || ra.Dead[0].Reason != InactivePreBoot {
		t.Errorf("dead = %+v", ra.Dead)
	}
	if asked[100] {
		t.Error("isPidAlive(100) must not be asked for a pre-boot record")
	}
}

func TestDiscoverLocatesDeadSessionsOnce(t *testing.T) {
	root := makeTmp(t)
	a := makeProfile(t, root, "work")
	sessions := filepath.Join(a.Dir, "sessions")
	projects := filepath.Join(a.Dir, "projects")
	mkdir(t, filepath.Join(projects, "-x"))
	mkdir(t, filepath.Join(projects, "-y"))
	writeJSON(t, filepath.Join(sessions, "150.json"), registryEntry(150, "gone", nil))
	rec := &recordingFS{}
	d := discoverDeps()
	d.FS = rec
	scope := []config.ProfileDir{a}

	first := DiscoverLiveSessions(scope, d)
	dead := first.Profiles[0].Dead
	if len(dead) != 1 || dead[0].SessionID != "gone" || dead[0].Reason != InactiveDeadPid || dead[0].Files != (SessionFiles{}) {
		t.Fatalf("dead = %+v", dead)
	}
	afterFirst := rec.touchedUnder(projects)
	if afterFirst == 0 {
		t.Fatal("the first scan must touch projects/")
	}

	DiscoverLiveSessions(scope, d)
	DiscoverLiveSessions(scope, d)
	if got := rec.touchedUnder(projects); got != afterFirst {
		t.Errorf("projects touched again: %d -> %d", afterFirst, got)
	}
	if !reflect.DeepEqual(d.InactiveFilesCache.Keys(), []string{a.Dir + "\x00gone"}) {
		t.Errorf("keys = %v", d.InactiveFilesCache.Keys())
	}

	removeAll(t, filepath.Join(sessions, "150.json"))
	DiscoverLiveSessions(scope, d)
	if d.InactiveFilesCache.Len() != 0 {
		t.Errorf("cache not pruned: %v", d.InactiveFilesCache.Keys())
	}
}

func TestDiscoverPrunesLocateCache(t *testing.T) {
	root := makeTmp(t)
	a := makeProfile(t, root, "work")
	sessions := filepath.Join(a.Dir, "sessions")
	writeFile(t, filepath.Join(a.Dir, "projects", "-p", "live.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(sessions, "100.json"), registryEntry(100, "live", nil))
	d := discoverDeps()
	// An entry of another profile is not touched.
	d.LocateCache.Set("/other\x00x", LocateEntry{Kind: LocateFound, Project: "-p"})

	DiscoverLiveSessions([]config.ProfileDir{a}, d)
	if e, _ := d.LocateCache.Get(a.Dir + "\x00live"); e != (LocateEntry{Kind: LocateFound, Project: "-p"}) {
		t.Errorf("entry = %+v", e)
	}

	removeAll(t, filepath.Join(sessions, "100.json"))
	DiscoverLiveSessions([]config.ProfileDir{a}, d)
	if !reflect.DeepEqual(d.LocateCache.Keys(), []string{"/other\x00x"}) {
		t.Errorf("keys = %v", d.LocateCache.Keys())
	}
}

func TestDiscoverDuplicatesShareLocation(t *testing.T) {
	root := makeTmp(t)
	a := makeProfile(t, root, "work")
	sessions := filepath.Join(a.Dir, "sessions")
	projects := filepath.Join(a.Dir, "projects")
	writeFile(t, filepath.Join(projects, "-p", "live-1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(sessions, "200.json"), registryEntry(200, "live-1", map[string]any{"updatedAt": 2}))
	writeJSON(t, filepath.Join(sessions, "300.json"), registryEntry(300, "live-1", map[string]any{"updatedAt": 1}))
	writeJSON(t, filepath.Join(sessions, "150.json"), registryEntry(150, "live-1", nil))
	rec := &recordingFS{}
	d := discoverDeps()
	d.FS = rec

	ra := DiscoverLiveSessions([]config.ProfileDir{a}, d).Profiles[0]

	if len(ra.Live) != 1 || ra.Live[0].PID != 200 {
		t.Errorf("live = %+v", ra.Live)
	}
	type row struct {
		pid    int
		reason InactiveReason
	}
	var dead []row
	for _, s := range ra.Dead {
		dead = append(dead, row{s.PID, s.Reason})
	}
	if !reflect.DeepEqual(dead, []row{{300, InactiveDuplicate}, {150, InactiveDeadPid}}) {
		t.Errorf("dead = %v", dead)
	}
	transcript := filepath.Join(projects, "-p", "live-1.jsonl")
	all := append([]DiscoveredSession{}, ra.Live...)
	for _, s := range ra.Dead {
		all = append(all, s.DiscoveredSession)
	}
	for _, s := range all {
		if s.Files.TranscriptPath != transcript {
			t.Errorf("pid %d transcript = %q", s.PID, s.Files.TranscriptPath)
		}
	}
	if n := rec.count("readdir", projects); n != 1 {
		t.Errorf("projects scanned %d times", n)
	}
}

func TestDiscoverProfileThroughSymlink(t *testing.T) {
	root := makeTmp(t)
	real := makeProfile(t, root, "real-profile")
	link := filepath.Join(root, "linked-profile")
	symlink(t, real.Dir, link)
	profile := config.ProfileDir{Label: "linked", Dir: link, Source: config.SourceConfig}
	proj := filepath.Join(real.Dir, "projects", "-repo")
	writeJSON(t, filepath.Join(real.Dir, "sessions", "100.json"), registryEntry(100, "live-1", nil))
	writeFile(t, filepath.Join(proj, "live-1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, "live-1", "subagents", "agent-q1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(proj, "live-1", "custom-title.json"), map[string]any{"customTitle": "Titled"})

	rp := DiscoverLiveSessions([]config.ProfileDir{profile}, discoverDeps()).Profiles[0]

	if len(rp.Live) != 1 {
		t.Fatalf("live = %+v", rp.Live)
	}
	live1 := rp.Live[0]
	want := SessionFiles{
		TranscriptPath: filepath.Join(proj, "live-1.jsonl"),
		SessionDir:     filepath.Join(proj, "live-1"),
		SubagentsDir:   filepath.Join(proj, "live-1", "subagents"),
		CustomTitle:    "Titled",
	}
	if live1.Key != "linked:live-1" || live1.Files != want {
		t.Errorf("live-1 = %+v", live1)
	}
	agents := ListSubagents(OSFS{}, live1.Files.SubagentsDir, nil)
	if !reflect.DeepEqual(agents, []SubagentFiles{{AgentID: "q1", TranscriptPath: filepath.Join(live1.Files.SubagentsDir, "agent-q1.jsonl")}}) {
		t.Errorf("agents = %+v", agents)
	}
}

func TestSessionKey(t *testing.T) {
	if got := SessionKey(config.ProfileDir{Label: "claude-x", Dir: "/c", Source: config.SourceConfig}, "s1"); got != "claude-x:s1" {
		t.Errorf("got %q", got)
	}
}
