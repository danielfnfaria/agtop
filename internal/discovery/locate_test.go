package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
)

const sid = "aaaa-1111"

func locateDeps(fs FS, cache *Cache[LocateEntry], now int64) Deps {
	return Deps{FS: fs, LocateCache: cache, Now: func() int64 { return now }}
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}

func TestLocateSessionFilesFindsEverything(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	projects := filepath.Join(profile.Dir, "projects")
	writeFile(t, filepath.Join(projects, "-other-project", "unrelated.jsonl"), "{}\n")
	proj := filepath.Join(projects, "-synthetic-project")
	writeFile(t, filepath.Join(proj, sid+".jsonl"), "{}\n")
	writeFile(t, filepath.Join(proj, sid, "subagents", "agent-a1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(proj, sid, "custom-title.json"), map[string]any{"customTitle": esc + "[1mMy\ttitle" + esc + "[0m"})

	got := LocateSessionFiles(profile, sid, locateDeps(OSFS{}, nil, 0), LocateOptions{})

	want := SessionFiles{
		TranscriptPath: filepath.Join(proj, sid+".jsonl"),
		SessionDir:     filepath.Join(proj, sid),
		SubagentsDir:   filepath.Join(proj, sid, "subagents"),
		CustomTitle:    "My title",
	}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestLocateSessionFilesWithoutTranscript(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	proj := filepath.Join(profile.Dir, "projects", "-p")
	mkdir(t, filepath.Join(proj, sid))
	deps := locateDeps(OSFS{}, nil, 0)

	if got := LocateSessionFiles(profile, sid, deps, LocateOptions{}); got != (SessionFiles{SessionDir: filepath.Join(proj, sid)}) {
		t.Errorf("got %+v", got)
	}
	if got := LocateSessionFiles(profile, "nope", deps, LocateOptions{}); got != (SessionFiles{}) {
		t.Errorf("got %+v", got)
	}
}

func TestLocateSessionFilesMissingProjects(t *testing.T) {
	profile := config.ProfileDir{Label: "x", Dir: filepath.Join(makeTmp(t), "missing"), Source: config.SourceConfig}
	if got := LocateSessionFiles(profile, sid, locateDeps(OSFS{}, nil, 0), LocateOptions{}); got != (SessionFiles{}) {
		t.Errorf("got %+v", got)
	}
}

func TestLocateSessionFilesUnsafeIDTouchesNothing(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	rec := &recordingFS{}
	for _, id := range []string{"../x", "..", "/etc/passwd", "a/b", `a\b`, "", "a.b"} {
		if got := LocateSessionFiles(profile, id, locateDeps(rec, nil, 0), LocateOptions{}); got != (SessionFiles{}) {
			t.Errorf("%q: got %+v", id, got)
		}
	}
	if len(rec.calls) != 0 {
		t.Errorf("calls = %v", rec.calls)
	}
}

func TestLocateSessionFilesRejectsSymlinkEscapes(t *testing.T) {
	root := makeTmp(t)
	profile := makeProfile(t, root, "claude")
	projects := filepath.Join(profile.Dir, "projects")
	outside := filepath.Join(root, "outside")
	writeFile(t, filepath.Join(outside, sid+".jsonl"), "{}\n")
	writeJSON(t, filepath.Join(outside, sid, "custom-title.json"), map[string]any{"customTitle": "outside"})
	writeFile(t, filepath.Join(outside, sid, "subagents", "agent-x.jsonl"), "{}\n")
	writeFile(t, filepath.Join(outside, "loose.jsonl"), "{}\n")
	// The whole project as a symlink to the outside.
	symlink(t, outside, filepath.Join(projects, "-evil-project"))
	// A single transcript as a symlink to the outside.
	other := "bbbb-2222"
	mkdir(t, filepath.Join(projects, "-p"))
	symlink(t, filepath.Join(outside, "loose.jsonl"), filepath.Join(projects, "-p", other+".jsonl"))
	rec := &recordingFS{}

	for _, id := range []string{sid, other} {
		if got := LocateSessionFiles(profile, id, locateDeps(rec, nil, 0), LocateOptions{}); got != (SessionFiles{}) {
			t.Errorf("%s: got %+v", id, got)
		}
	}
	if n := rec.touchedUnder(outside + string(filepath.Separator)); n != 0 {
		t.Errorf("touched %d paths outside", n)
	}
	if reads := rec.reads(); len(reads) != 0 {
		t.Errorf("reads = %v", reads)
	}
}

func TestLocateSessionFilesCustomTitle(t *testing.T) {
	root := makeTmp(t)
	profile := makeProfile(t, root, "claude")
	proj := filepath.Join(profile.Dir, "projects", "-p")
	writeJSON(t, filepath.Join(root, "outside-title.json"), map[string]any{"customTitle": "outside"})
	cases := []struct {
		sid  string
		make func(file string)
	}{
		{"s-link", func(f string) { symlink(t, filepath.Join(root, "outside-title.json"), f) }},
		{"s-big", func(f string) {
			writeJSON(t, f, map[string]any{"customTitle": strings.Repeat("x", MaxCustomTitleBytes)})
		}},
		{"s-bad", func(f string) { writeFile(t, f, "{not json") }},
		{"s-num", func(f string) { writeJSON(t, f, map[string]any{"customTitle": 42}) }},
		{"s-empty", func(f string) { writeJSON(t, f, map[string]any{"customTitle": esc + "[0m"}) }},
	}
	for _, c := range cases {
		writeFile(t, filepath.Join(proj, c.sid+".jsonl"), "{}\n")
		mkdir(t, filepath.Join(proj, c.sid))
		c.make(filepath.Join(proj, c.sid, "custom-title.json"))
	}
	for _, c := range cases {
		t.Run(c.sid, func(t *testing.T) {
			rec := &recordingFS{}
			got := LocateSessionFiles(profile, c.sid, locateDeps(rec, nil, 0), LocateOptions{})
			if got.TranscriptPath != filepath.Join(proj, c.sid+".jsonl") || got.CustomTitle != "" {
				t.Errorf("got %+v", got)
			}
			for _, r := range rec.reads() {
				if strings.Contains(r.path, "outside") {
					t.Errorf("read %s", r.path)
				}
			}
		})
	}
}

func TestLocateSessionFilesCache(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	projects := filepath.Join(profile.Dir, "projects")
	for _, p := range []string{"-a", "-b", "-c"} {
		mkdir(t, filepath.Join(projects, p))
	}
	writeFile(t, filepath.Join(projects, "-b", sid+".jsonl"), "{}\n")
	rec := &recordingFS{}
	deps := locateDeps(rec, NewCache[LocateEntry](), 0)
	scans := func() int { return rec.count("readdir", projects) }

	first := LocateSessionFiles(profile, sid, deps, LocateOptions{})
	if first.TranscriptPath != filepath.Join(projects, "-b", sid+".jsonl") || scans() != 1 {
		t.Fatalf("first = %+v, scans = %d", first, scans())
	}
	LocateSessionFiles(profile, sid, deps, LocateOptions{})
	if scans() != 1 {
		t.Errorf("cached lookup scanned again: %d", scans())
	}
	rename(t, filepath.Join(projects, "-b", sid+".jsonl"), filepath.Join(projects, "-c", sid+".jsonl"))
	moved := LocateSessionFiles(profile, sid, deps, LocateOptions{})
	if moved.TranscriptPath != filepath.Join(projects, "-c", sid+".jsonl") || scans() != 2 {
		t.Errorf("moved = %+v, scans = %d", moved, scans())
	}
}

func TestLocateSessionFilesCwdHint(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	projects := projectsDir(profile)
	mkdir(t, filepath.Join(projects, "-a"))
	proj := filepath.Join(projects, "-synthetic-my-repo")
	writeFile(t, filepath.Join(proj, sid+".jsonl"), "{}\n")
	rec := &recordingFS{}
	cache := NewCache[LocateEntry]()

	got := LocateSessionFiles(profile, sid, locateDeps(rec, cache, 0), LocateOptions{Cwd: "/synthetic/my.repo"})

	if got.TranscriptPath != filepath.Join(proj, sid+".jsonl") {
		t.Errorf("got %+v", got)
	}
	if rec.count("readdir", projects) != 0 {
		t.Error("the hint should avoid scanning projects/")
	}
	if e, _ := cache.Get(profile.Dir + "\x00" + sid); e != (LocateEntry{Kind: LocateFound, Project: "-synthetic-my-repo"}) {
		t.Errorf("cache = %+v", e)
	}
}

func projectsDir(p config.ProfileDir) string { return filepath.Join(p.Dir, "projects") }

func TestLocateSessionFilesWrongHintFallsBackToScan(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	writeFile(t, filepath.Join(projectsDir(profile), "-real", sid+".jsonl"), "{}\n")
	rec := &recordingFS{}

	got := LocateSessionFiles(profile, sid, locateDeps(rec, nil, 0), LocateOptions{Cwd: "/somewhere/else"})

	if got.TranscriptPath != filepath.Join(projectsDir(profile), "-real", sid+".jsonl") ||
		rec.count("readdir", projectsDir(profile)) != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestLocateSessionFilesNegativeCache(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	pr := projectsDir(profile)
	mkdir(t, filepath.Join(pr, "-p", sid))
	mkdir(t, filepath.Join(pr, "-q"))
	rec := &recordingFS{}
	cache := NewCache[LocateEntry]()
	now := int64(50_000)
	deps := Deps{FS: rec, LocateCache: cache, Now: func() int64 { return now }}
	withFolder := SessionFiles{SessionDir: filepath.Join(pr, "-p", sid)}

	if got := LocateSessionFiles(profile, sid, deps, LocateOptions{}); got != withFolder || rec.count("readdir", pr) != 1 {
		t.Fatalf("got %+v", got)
	}
	// The transcript appears in a project only the scan would find: inside the TTL it is not seen.
	writeFile(t, filepath.Join(pr, "-q", sid+".jsonl"), "{}\n")
	now += LocateMissTTLMs - 1
	if got := LocateSessionFiles(profile, sid, deps, LocateOptions{}); got != withFolder || rec.count("readdir", pr) != 1 {
		t.Fatalf("inside TTL: got %+v, scans %d", got, rec.count("readdir", pr))
	}
	// TTL expired: scans again and finds it.
	now++
	got := LocateSessionFiles(profile, sid, deps, LocateOptions{})
	if got.TranscriptPath != filepath.Join(pr, "-q", sid+".jsonl") || rec.count("readdir", pr) != 2 {
		t.Errorf("after TTL: got %+v, scans %d", got, rec.count("readdir", pr))
	}
}

func TestLocateSessionFilesNegativeCacheKeepsHint(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	pr := projectsDir(profile)
	mkdir(t, filepath.Join(pr, "-repo"))
	rec := &recordingFS{}
	deps := locateDeps(rec, NewCache[LocateEntry](), 0)
	opts := LocateOptions{Cwd: "/repo"}

	if got := LocateSessionFiles(profile, sid, deps, opts); got != (SessionFiles{}) || rec.count("readdir", pr) != 1 {
		t.Fatalf("got %+v", got)
	}
	writeFile(t, filepath.Join(pr, "-repo", sid+".jsonl"), "{}\n")
	got := LocateSessionFiles(profile, sid, deps, opts)
	if got.TranscriptPath != filepath.Join(pr, "-repo", sid+".jsonl") || rec.count("readdir", pr) != 1 {
		t.Errorf("got %+v, scans %d", got, rec.count("readdir", pr))
	}
}

func TestProjectDirHint(t *testing.T) {
	tests := []struct {
		cwd, want string
		ok        bool
	}{
		{"/Users/x/my_repo.v2", "-Users-x-my-repo-v2", true},
		{`C:\work\repo`, "C--work-repo", true},
		{"/", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ProjectDirHint(tt.cwd)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ProjectDirHint(%q) = %q, %v", tt.cwd, got, ok)
		}
	}
}

func TestListSubagentsGroupsAndIgnores(t *testing.T) {
	root := makeTmp(t)
	dir := filepath.Join(root, "subagents")
	writeFile(t, filepath.Join(dir, "agent-a1.jsonl"), "{}\n")
	writeJSON(t, filepath.Join(dir, "agent-a1.meta.json"), map[string]any{"agentType": "x"})
	writeJSON(t, filepath.Join(dir, "agent-b2.meta.json"), map[string]any{})
	writeFile(t, filepath.Join(dir, "agent-c_3-z.jsonl"), "{}\n")
	writeFile(t, filepath.Join(dir, "agent-bad.id.jsonl"), "{}\n")
	writeFile(t, filepath.Join(dir, "agent-.jsonl"), "{}\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "x")
	mkdir(t, filepath.Join(dir, "agent-dir.jsonl"))
	writeFile(t, filepath.Join(root, "outside.jsonl"), "{}\n")
	symlink(t, filepath.Join(root, "outside.jsonl"), filepath.Join(dir, "agent-evil.jsonl"))
	rec := &recordingFS{}

	got := ListSubagents(rec, dir, nil)

	want := []SubagentFiles{
		{AgentID: "a1", TranscriptPath: filepath.Join(dir, "agent-a1.jsonl"), MetaPath: filepath.Join(dir, "agent-a1.meta.json")},
		{AgentID: "b2", MetaPath: filepath.Join(dir, "agent-b2.meta.json")},
		{AgentID: "c_3-z", TranscriptPath: filepath.Join(dir, "agent-c_3-z.jsonl")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if len(rec.reads()) != 0 || rec.touchedUnder(filepath.Join(root, "outside.jsonl")) != 0 {
		t.Errorf("unexpected reads or outside access: %v", rec.calls)
	}
}

func TestListSubagentsMissingDir(t *testing.T) {
	if got := ListSubagents(OSFS{}, filepath.Join(makeTmp(t), "nope"), nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestListSubagentsResolvedMap(t *testing.T) {
	root := makeTmp(t)
	dir := filepath.Join(root, "subagents")
	a1 := filepath.Join(dir, "agent-a1.jsonl")
	writeFile(t, a1, "{}\n")
	writeJSON(t, filepath.Join(dir, "agent-a1.meta.json"), map[string]any{})
	known := map[string]string{}
	rec := &recordingFS{}
	perFile := func() int {
		n := 0
		for _, c := range rec.calls {
			if c.op != "readdir" && strings.HasPrefix(filepath.Base(c.path), "agent-") {
				n++
			}
		}
		return n
	}
	keys := func() []string {
		var k []string
		for name := range known {
			k = append(k, name)
		}
		if len(k) == 2 && k[0] > k[1] {
			k[0], k[1] = k[1], k[0]
		}
		return k
	}

	ListSubagents(rec, dir, known)
	if perFile() == 0 || !reflect.DeepEqual(keys(), []string{"agent-a1.jsonl", "agent-a1.meta.json"}) {
		t.Fatalf("first: perFile %d, known %v", perFile(), known)
	}

	rec.reset()
	again := ListSubagents(rec, dir, known)
	if perFile() != 0 {
		t.Errorf("known names must not pay realpath + stat again: %v", rec.calls)
	}
	if want := []SubagentFiles{{AgentID: "a1", TranscriptPath: a1, MetaPath: filepath.Join(dir, "agent-a1.meta.json")}}; !reflect.DeepEqual(again, want) {
		t.Errorf("again = %+v", again)
	}

	rec.reset()
	b2 := filepath.Join(dir, "agent-b2.jsonl")
	writeFile(t, b2, "{}\n")
	removeAll(t, filepath.Join(dir, "agent-a1.meta.json"))
	third := ListSubagents(rec, dir, known)
	for _, c := range rec.calls {
		if base := filepath.Base(c.path); c.op != "readdir" && strings.HasPrefix(base, "agent-") && base != "agent-b2.jsonl" {
			t.Errorf("only the new name should be resolved, got %v", c)
		}
	}
	if want := []SubagentFiles{{AgentID: "a1", TranscriptPath: a1}, {AgentID: "b2", TranscriptPath: b2}}; !reflect.DeepEqual(third, want) {
		t.Errorf("third = %+v", third)
	}
	if !reflect.DeepEqual(keys(), []string{"agent-a1.jsonl", "agent-b2.jsonl"}) {
		t.Errorf("known = %v", known)
	}
}

func TestLocateCacheKey(t *testing.T) {
	if got := LocateCacheKey("/p/.claude", "s1"); got != "/p/.claude\x00s1" {
		t.Errorf("got %q", got)
	}
	if !strings.HasPrefix(LocateCacheKey("/p/.claude", "s1"), LocateCacheKey("/p/.claude", "")) {
		t.Error("the empty-session key must be a per-profile prefix")
	}
}
