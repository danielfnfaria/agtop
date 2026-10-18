package opencode_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/cli"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/source/opencode"
	"github.com/danielfnfaria/agtop/internal/store"
)

// storeOver runs one scan of a store over the source alone (no watchers) and returns the
// snapshot.
func storeOver(t *testing.T, f *fixture, src *opencode.Source, nowMs int64) *store.Snapshot {
	t.Helper()
	opts := store.DefaultOptions([]config.ProfileDir{f.profile()})
	opts.Watch = false
	s := store.New(opts, store.Deps{Sources: []source.Source{src}, Clock: fixedClock{nowMs}})
	s.Start()
	snap := s.Snapshot()
	s.Stop()
	<-s.Done()
	return snap
}

type fixedClock struct{ now int64 }

func (c fixedClock) Now() int64 { return c.now }
func (fixedClock) AfterFunc(_ int64, f func()) func() {
	t := time.AfterFunc(time.Hour, f)
	return func() { t.Stop() }
}

// TestSecretsNeverLeave: conversation content (text, reasoning, tool input and output,
// commands, prompts) never reaches the snapshot, the dump or the errors.
func TestSecretsNeverLeave(t *testing.T) {
	f := newFixture(t)
	const sid, child = "ses_s", "ses_sc"
	f.addSession(sessionSpec{ID: sid, Title: "Safe title", Model: model, Created: now - hour, Updated: now - minute})
	f.addMessage(sid, "msg_u", now-hour, now-hour, userData(now-hour, model))
	f.addPart(sid, "msg_u", "prt_prompt", now-hour, now-hour, textData("SECRET_PROMPT please rotate SECRET_KEY"))
	f.addMessage(sid, "msg_a", now-hour+1, now-minute, assistantData(sid, model, now-hour+1, 0, "", tokens{}))
	f.addPart(sid, "msg_a", "prt_reason", now-hour+2, now-hour+2, reasoningData("SECRET_REASONING"))
	f.addPart(sid, "msg_a", "prt_text", now-hour+3, now-hour+3, textData("SECRET_TEXT https://github.com/o/r/pull/12"))
	f.addPart(sid, "msg_a", "prt_bash", now-hour+4, now-minute, toolData("bash", "call_bash", "completed",
		map[string]any{"command": "export TOKEN=SECRET_COMMAND; go test ./...", "description": "SECRET_DESC"}, nil,
		"SECRET_OUTPUT", now-hour+4, now-minute))
	f.addPart(sid, "msg_a", "prt_edit", now-hour+5, now-hour+5, toolData("edit", "call_edit", "running",
		map[string]any{"filePath": "/home/u/proj/safe.go", "oldString": "SECRET_OLD", "newString": "SECRET_NEW"}, nil, "", now-hour+5, 0))
	f.addPart(sid, "msg_a", "prt_task", now-hour+6, now-hour+6, toolData("task", "call_task", "running",
		map[string]any{"description": "Safe description", "prompt": "SECRET_TASK_PROMPT", "subagent_type": "explore"},
		map[string]any{"sessionId": child, "parentSessionId": sid}, "", now-hour+6, 0))
	f.addSession(sessionSpec{ID: child, Parent: sid, Agent: "explore", Title: "Safe description (@explore subagent)", Model: model, Created: now - hour + 6, Updated: now - 2*minute})
	f.addMessage(child, "msg_c", now-hour+7, now-2*minute, assistantData(child, model, now-hour+7, 0, "", tokens{}))
	f.addPart(child, "msg_c", "prt_c_text", now-hour+8, now-hour+8, textData("SECRET_CHILD_TEXT"))
	f.addPart(child, "msg_c", "prt_c_tool", now-hour+9, now-2*minute, toolData("bash", "call_c", "error",
		map[string]any{"command": "cat SECRET_FILE"}, nil, "SECRET_CHILD_OUTPUT", now-hour+9, now-2*minute))

	src := newSource(t, f)
	snap := storeOver(t, f, src, now)
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %d, errors = %v", len(snap.Sessions), snap.Errors)
	}
	v := snap.Sessions[0]
	if v.DisplayTitle != "Safe title" || v.Activity != "busy" || v.PID != 55813 || len(v.PendingTools) != 1 {
		t.Errorf("view = title %q activity %q pid %d pending %d", v.DisplayTitle, v.Activity, v.PID, len(v.PendingTools))
	}
	agent, ok := v.Agents[child]
	if !ok || agent.Status != domain.StatusRunning || agent.Description != "Safe description" || agent.Type != "explore" {
		t.Errorf("agent = %+v (%v)", agent, ok)
	}
	if len(v.PRs) != 0 {
		t.Errorf("PR links come from text, which is never read: %+v", v.PRs)
	}
	var dump bytes.Buffer
	summary := cli.Summarize(snap)
	dump.WriteString(marshal(t, summary))
	for _, text := range []string{marshal(t, snap), dump.String(), strings.Join(snap.Errors, "\n")} {
		if strings.Contains(text, "SECRET_") {
			t.Errorf("content leaked: %s", text)
		}
	}
	for _, e := range snap.Errors {
		t.Errorf("unexpected error: %s", e)
	}
}

// TestConcurrentWriter: a writer committing through the WAL while the reader reads; the reader
// sees the new commits and never changes the database file.
func TestConcurrentWriter(t *testing.T) {
	f := newFixture(t)
	const sid = "ses_w"
	f.addSession(sessionSpec{ID: sid, Title: "Writer", Model: model, Created: now - minute, Updated: now - minute})
	f.addMessage(sid, "msg_0", now-minute, now-minute, userData(now-minute, model))
	// Checkpoint and close the writer: the main file now holds everything and the WAL is gone.
	f.exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.close()
	before := hashFile(t, f.path)
	f.db = f.openWriter()

	src := newSource(t, f)
	discoverOne(t, src, f)
	r, err := src.OpenTranscript("opencode:" + sid)
	if err != nil {
		t.Fatal(err)
	}
	if res := readAll(t, r); len(res.Events) != 3 {
		t.Fatalf("first read = %v", kinds(res.Events))
	}
	// Writes in the WAL (no checkpoint: the main file stays as hashed) while reading.
	const n = 50
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n {
			f.addMessage(sid, "msg_w"+string(rune('a'+i%26))+string(rune('a'+i/26)), now+int64(i), now+int64(i), userData(now+int64(i), model))
		}
	}()
	seen := 0
	deadline := time.Now().Add(10 * time.Second)
	for seen < n && time.Now().Before(deadline) {
		res, err := r.Read()
		if err != nil {
			t.Fatalf("read while writing: %v", err)
		}
		for _, e := range res.Events {
			if _, ok := e.(domain.UserPromptEvent); ok {
				seen++
			}
		}
	}
	wg.Wait()
	for seen < n {
		res := readAll(t, r)
		if len(res.Events) == 0 {
			break
		}
		for _, e := range res.Events {
			if _, ok := e.(domain.UserPromptEvent); ok {
				seen++
			}
		}
	}
	if seen != n {
		t.Errorf("reader saw %d of %d commits", seen, n)
	}
	if after := hashFile(t, f.path); after != before {
		t.Error("the database file changed while agtop only read it")
	}
	if _, err := os.Stat(f.path + "-wal"); err != nil {
		t.Errorf("the WAL must still hold the writes: %v", err)
	}
	// The reader alone, after the writer is gone, changes nothing either.
	f.exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.close()
	settled := hashFile(t, f.path)
	for range 5 {
		readAll(t, r)
		discoverOne(t, src, f)
	}
	_ = r.Close()
	_ = src.Close()
	if hashFile(t, f.path) != settled {
		t.Error("the database file changed under the reader alone")
	}
}

func hashFile(t *testing.T, p string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// TestStoreEndToEnd: the store over the opencode source shows sessions, agents, context and
// profile status, and reports a broken database by code.
func TestStoreEndToEnd(t *testing.T) {
	f := newFixture(t)
	writeModels(t, f, 200_000)
	const sid, child = "ses_e", "ses_ec"
	f.addSession(sessionSpec{ID: sid, Title: "End to end", Model: model, Created: now - hour, Updated: now - minute})
	f.addMessage(sid, "msg_1", now-hour, now-hour, userData(now-hour, model))
	f.addMessage(sid, "msg_2", now-hour+1, now-hour+5, assistantData(sid, model, now-hour+1, now-hour+5, "tool-calls", tokens{input: 1000, output: 100, cacheRead: 49_000}))
	f.addPart(sid, "msg_2", "prt_task", now-hour+2, now-hour+2, toolData("task", "call_t", "completed",
		map[string]any{"description": "Done agent", "subagent_type": "general"}, map[string]any{"sessionId": child}, "ok", now-hour+2, now-hour+4))
	f.addSession(sessionSpec{ID: child, Parent: sid, Agent: "general", Title: "Done agent (@general subagent)", Model: model, Created: now - hour + 2, Updated: now - hour + 4})
	f.addMessage(child, "msg_c", now-hour+2, now-hour+4, assistantData(child, model, now-hour+2, now-hour+4, "stop", tokens{input: 5, output: 5}))
	f.addMessage(sid, "msg_3", now-minute, now-minute, assistantData(sid, model, now-minute, 0, "", tokens{}))
	f.addPart(sid, "msg_3", "prt_bash", now-minute, now-minute, toolData("bash", "call_b", "running", map[string]any{"command": "npm test"}, nil, "", now-minute, 0))

	src := newSource(t, f)
	snap := storeOver(t, f, src, now)
	if len(snap.Errors) != 0 || len(snap.Sessions) != 1 {
		t.Fatalf("errors = %v, sessions = %d", snap.Errors, len(snap.Sessions))
	}
	v := snap.Sessions[0]
	if v.Provider != domain.Opencode || v.DisplayTitle != "End to end" || v.Activity != "busy" || v.Cwd != "/home/u/proj" ||
		v.Model == nil || v.Model.ID != model || v.Model.Name != "Claude Sonnet 4.5" ||
		v.PID != 55813 || v.ProcessStart != now-2*hour || v.StartedAt != now-hour {
		t.Errorf("view = %+v", v)
	}
	if v.ContextWindow != (domain.ContextWindow{Size: 200_000, Source: domain.SourceReported}) || v.ContextUsed != 50_000 || v.ContextPercent != 25 {
		t.Errorf("context = %+v used %d %%%v", v.ContextWindow, v.ContextUsed, v.ContextPercent)
	}
	if len(v.PendingTools) != 1 || v.PendingTools[0].Name != "Bash" || v.PendingTools[0].CommandCategory != domain.CmdTests {
		t.Errorf("pending = %+v", v.PendingTools)
	}
	if a := v.Agents[child]; a == nil || a.Status != domain.StatusCompleted || a.Description != "Done agent" || a.Totals.Input != 5 ||
		a.Context.ContextWindow != (domain.ContextWindow{Size: 200_000, Source: domain.SourceModel}) {
		t.Errorf("agent = %+v", a)
	}
	if len(snap.Profiles) != 1 || snap.Profiles[0].Provider != domain.Opencode || snap.Profiles[0].LiveCount != 1 {
		t.Errorf("profiles = %+v", snap.Profiles)
	}

	// The database goes away: the error is coded, the session goes stale, the profile has an issue.
	f.close()
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(f.path + "-wal")
	_ = os.Remove(f.path + "-shm")
	src2 := newSource(t, f)
	snap = storeOver(t, f, src2, now)
	if len(snap.Errors) != 1 || snap.Errors[0] != "discovery opencode: DB_MISSING" {
		t.Errorf("errors = %v", snap.Errors)
	}
	if len(snap.Profiles) != 1 || snap.Profiles[0].Issues["database"] != 1 {
		t.Errorf("profile = %+v", snap.Profiles)
	}
}
