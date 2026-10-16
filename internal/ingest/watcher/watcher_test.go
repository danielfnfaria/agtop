package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/fdcount"
)

func j(parts ...string) string {
	return filepath.Join(append([]string{string(filepath.Separator), "r", "projects"}, parts...)...)
}

func TestIgnoreInSession(t *testing.T) {
	target := Target{
		Kind:           KindSession,
		TranscriptPath: j("esc", "sid.jsonl"),
		SubagentsDir:   j("esc", "sid", "subagents"),
	}
	tests := []struct {
		name string
		p    string
		kind EntryKind
		want bool
	}{
		{"transcript (file)", j("esc", "sid.jsonl"), KindFile, false},
		{"transcript (unknown)", j("esc", "sid.jsonl"), KindUnknown, false},
		{"subagents dir", j("esc", "sid", "subagents"), KindDir, false},
		{"agent transcript", j("esc", "sid", "subagents", "agent-a1.jsonl"), KindFile, false},
		{"agent meta", j("esc", "sid", "subagents", "agent-a1.meta.json"), KindFile, false},
		{"other transcript", j("esc", "other.jsonl"), KindFile, true},
		{"project dir", j("esc"), KindDir, true},
		{"custom title", j("esc", "sid", "custom-title.json"), KindFile, true},
		{"other file in subagents", j("esc", "sid", "subagents", "other.json"), KindFile, true},
		{"agent-named dir", j("esc", "sid", "subagents", "agent-x"), KindDir, true},
		{"nested agent file", j("esc", "sid", "subagents", "agent-x", "agent-y.jsonl"), KindUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IgnoreInSession(target, tt.p, tt.kind); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIgnoreInSessionWithoutSubagentsDir(t *testing.T) {
	target := Target{Kind: KindSession, TranscriptPath: j("esc", "sid.jsonl")}
	if IgnoreInSession(target, j("esc", "sid.jsonl"), KindFile) {
		t.Error("the transcript must pass")
	}
	if !IgnoreInSession(target, j("esc", "sid", "subagents", "agent-a1.jsonl"), KindFile) {
		t.Error("agent files must be ignored without subagents/")
	}
}

func TestIsRegistryFileName(t *testing.T) {
	tests := map[string]bool{"123.json": true, "123.abc.key": false, "sub": false}
	for name, want := range tests {
		if got := IsRegistryFileName(name); got != want {
			t.Errorf("IsRegistryFileName(%q) = %v", name, got)
		}
	}
}

func tmpDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openWatcher(t *testing.T, target Target) Watcher {
	t.Helper()
	w, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	<-w.Ready()
	return w
}

// collector gathers the paths a watcher reports.
type collector struct {
	mu   sync.Mutex
	seen []string
}

func collect(w Watcher) *collector {
	c := &collector{}
	go func() {
		for ev := range w.Events() {
			c.mu.Lock()
			c.seen = append(c.seen, ev.Path)
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *collector) has(p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.seen {
		if s == p {
			return true
		}
	}
	return false
}

func (c *collector) others(except ...string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
next:
	for _, s := range c.seen {
		for _, e := range except {
			if s == e {
				continue next
			}
		}
		out = append(out, s)
	}
	return out
}

// waitNudging waits for pred, touching file (utimes) every second without an answer: file
// watchers can lose events under load.
func waitNudging(t *testing.T, file string, pred func() bool) {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if pred() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		now := time.Now()
		_ = os.Chtimes(file, now, now)
	}
	if !pred() {
		t.Fatal("timeout waiting for a watcher event")
	}
}

func TestSessionWatcherSeesTranscriptAndAgentFiles(t *testing.T) {
	root := tmpDir(t)
	transcript := filepath.Join(root, "esc", "sid.jsonl")
	sub := filepath.Join(root, "esc", "sid", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, transcript, "")
	w := openWatcher(t, Target{Kind: KindSession, TranscriptPath: transcript, SubagentsDir: sub})
	c := collect(w)

	agent := filepath.Join(sub, "agent-a1.jsonl")
	mustWrite(t, filepath.Join(sub, "ignored.txt"), "x")
	mustWrite(t, filepath.Join(root, "esc", "sid", "custom-title.json"), "{}")
	mustWrite(t, filepath.Join(root, "esc", "other.jsonl"), "{}\n")
	mustWrite(t, agent, "{}\n")
	waitNudging(t, agent, func() bool { return c.has(agent) })
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{}\n")
	_ = f.Close()
	waitNudging(t, transcript, func() bool { return c.has(transcript) })

	if other := c.others(agent, transcript); len(other) != 0 {
		t.Errorf("unexpected events: %v", other)
	}
}

func TestSessionWatcherPathCountDoesNotGrowWithOtherSessions(t *testing.T) {
	root := tmpDir(t)
	project := filepath.Join(root, "esc")
	for i := 0; i < 50; i++ {
		mustWrite(t, filepath.Join(project, fmt.Sprintf("dead-%d.jsonl", i)), "{}\n")
		mustWrite(t, filepath.Join(project, fmt.Sprintf("dead-%d", i), "subagents", "agent-x.jsonl"), "{}\n")
	}
	transcript := filepath.Join(project, "live.jsonl")
	sub := filepath.Join(project, "live", "subagents")
	mustWrite(t, transcript, "{}\n")
	mustWrite(t, filepath.Join(sub, "agent-a.jsonl"), "{}\n")
	mustWrite(t, filepath.Join(sub, "agent-a.meta.json"), "{}")

	w := openWatcher(t, Target{Kind: KindSession, TranscriptPath: transcript, SubagentsDir: sub})

	// The transcript file and the subagents/ directory; the directory watch covers its entries.
	if got := w.WatchedPathCount(); got != 2 {
		t.Errorf("WatchedPathCount = %d, want 2", got)
	}
}

func TestSessionsWatcherReportsOnePathAndJSON(t *testing.T) {
	root := tmpDir(t)
	for i := 0; i < 50; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("%d.json", 1000+i)), "{}")
	}
	w := openWatcher(t, Target{Kind: KindSessions, Root: root})
	c := collect(w)
	if got := w.WatchedPathCount(); got != 1 {
		t.Errorf("WatchedPathCount = %d, want 1", got)
	}
	mustWrite(t, filepath.Join(root, "42.abc.key"), "x")
	file := filepath.Join(root, "42.json")
	mustWrite(t, file, "{}")
	waitNudging(t, file, func() bool { return c.has(file) })
	if other := c.others(file); len(other) != 0 && other[0] == filepath.Join(root, "42.abc.key") {
		t.Errorf("non-json name reported: %v", other)
	}
}

func TestUsageWatcherReportsJSON(t *testing.T) {
	root := tmpDir(t)
	w := openWatcher(t, Target{Kind: KindUsage, Root: root})
	c := collect(w)
	file := filepath.Join(root, "usage.json")
	mustWrite(t, file, "{}")
	waitNudging(t, file, func() bool { return c.has(file) })
}

func TestRewatchAfterReplacement(t *testing.T) {
	root := tmpDir(t)
	transcript := filepath.Join(root, "sid.jsonl")
	mustWrite(t, transcript, "a\n")
	w := openWatcher(t, Target{Kind: KindSession, TranscriptPath: transcript})
	c := collect(w)

	other := filepath.Join(root, "other.jsonl")
	mustWrite(t, other, "b\n")
	if err := os.Rename(other, transcript); err != nil {
		t.Fatal(err)
	}
	waitNudging(t, transcript, func() bool { return c.has(transcript) })
	if err := w.Rewatch(); err != nil {
		t.Fatal(err)
	}
	if got := w.WatchedPathCount(); got != 1 {
		t.Errorf("WatchedPathCount = %d, want 1", got)
	}
	c.mu.Lock()
	before := len(c.seen)
	c.mu.Unlock()
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("c\n")
	_ = f.Close()
	waitNudging(t, transcript, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.seen) > before
	})
}

func TestCloseIsIdempotent(t *testing.T) {
	root := tmpDir(t)
	w, err := New(Target{Kind: KindSessions, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Ready()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := w.WatchedPathCount(); got != 0 {
		t.Errorf("WatchedPathCount = %d after Close", got)
	}
	if _, ok := <-w.Events(); ok {
		t.Error("Events must be closed after Close")
	}

	file := filepath.Join(root, "x.jsonl")
	mustWrite(t, file, "")
	s, err := New(Target{Kind: KindSession, TranscriptPath: file})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Ready()
	_ = s.Close()
	_ = s.Close()
}

func TestCloseBeforeSettleReleasesReady(t *testing.T) {
	w, err := New(Target{Kind: KindSessions, Root: tmpDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	select {
	case <-w.Ready():
	case <-time.After(time.Second):
		t.Error("Ready must be released by Close")
	}
}

func TestSessionsDirMissingFails(t *testing.T) {
	if w, err := New(Target{Kind: KindSessions, Root: filepath.Join(tmpDir(t), "missing")}); err == nil {
		_ = w.Close()
		t.Error("want an error for a missing directory")
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	n, err := fdcount.Open()
	if err != nil {
		t.Skipf("cannot count descriptors: %v", err)
	}
	return n
}

// fdCost is how many descriptors a watcher over target holds once ready.
func fdCost(t *testing.T, target Target) int {
	t.Helper()
	before := openFDs(t)
	w, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	<-w.Ready()
	cost := openFDs(t) - before
	_ = w.Close()
	return cost
}

func TestDirWatchDescriptorsDoNotGrowWithEntries(t *testing.T) {
	empty, full := tmpDir(t), tmpDir(t)
	for i := 0; i < 300; i++ {
		mustWrite(t, filepath.Join(full, fmt.Sprintf("%d.json", 1000+i)), "{}")
		mustWrite(t, filepath.Join(full, fmt.Sprintf("other-%d.txt", i)), "x")
	}
	base := fdCost(t, Target{Kind: KindSessions, Root: empty})
	for _, target := range []Target{
		{Kind: KindSessions, Root: full},
		{Kind: KindUsage, Root: full},
		{Kind: KindSession, SubagentsDir: full},
	} {
		if got := fdCost(t, target); got > base+1 {
			t.Errorf("kind %d: %d descriptors for 600 entries, %d for none", target.Kind, got, base)
		}
	}
	if base > 8 {
		t.Errorf("%d descriptors for one directory watch", base)
	}
}

func TestCloseReleasesDescriptors(t *testing.T) {
	root := tmpDir(t)
	transcript := filepath.Join(root, "sid.jsonl")
	sub := filepath.Join(root, "sid", "subagents")
	mustWrite(t, transcript, "")
	for i := 0; i < 20; i++ {
		mustWrite(t, filepath.Join(sub, fmt.Sprintf("agent-%d.jsonl", i)), "{}\n")
	}
	before := openFDs(t)
	w, err := New(Target{Kind: KindSession, TranscriptPath: transcript, SubagentsDir: sub})
	if err != nil {
		t.Fatal(err)
	}
	<-w.Ready()
	_ = w.Rewatch()
	_ = w.Close()
	// fsnotify closes its kqueue from its own goroutine, shortly after Close.
	deadline := time.Now().Add(2 * time.Second)
	after := openFDs(t)
	for after > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = openFDs(t)
	}
	if after > before {
		t.Errorf("descriptors: %d before, %d after Close", before, after)
	}
}

func TestSessionWatcherSeesAppendsAndRemovalsOfExistingAgentFiles(t *testing.T) {
	root := tmpDir(t)
	sub := filepath.Join(root, "sid", "subagents")
	agent := filepath.Join(sub, "agent-a1.jsonl")
	meta := filepath.Join(sub, "agent-a1.meta.json")
	mustWrite(t, agent, "{}\n")
	mustWrite(t, meta, "{}")
	w := openWatcher(t, Target{Kind: KindSession, SubagentsDir: sub})
	var mu sync.Mutex
	var seen []Event
	go func() {
		for ev := range w.Events() {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
		}
	}()
	has := func(want Event) func() bool {
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			for _, ev := range seen {
				if ev == want {
					return true
				}
			}
			return false
		}
	}
	wait := func(want Event) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !has(want)() {
			if time.Now().After(deadline) {
				t.Fatalf("no %+v", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	f, err := os.OpenFile(agent, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{}\n")
	_ = f.Close()
	wait(Event{Op: Change, Path: agent})
	if err := os.Remove(meta); err != nil {
		t.Fatal(err)
	}
	wait(Event{Op: Unlink, Path: meta})
	mustWrite(t, meta, "{}")
	wait(Event{Op: Add, Path: meta})
}
