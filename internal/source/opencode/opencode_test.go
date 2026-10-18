package opencode_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
	"github.com/danielfnfaria/agtop/internal/proc"
	"github.com/danielfnfaria/agtop/internal/source"
	"github.com/danielfnfaria/agtop/internal/source/opencode"
)

const (
	minute = 60_000
	hour   = 60 * minute
	// now is the clock of the tests.
	now   int64 = 1_800_000_000_000
	model       = "claude-sonnet-4-5"
)

// procStart is the start of the default test process: two hours ago.
var procStart = time.UnixMilli(now - 2*hour).UTC()

// procAt is an opencode process with cwd dir started at start.
func procAt(pid int, dir string, start time.Time) proc.Process {
	return proc.Process{PID: pid, Name: "opencode", Exe: "/home/u/.opencode/bin/opencode", Cwd: dir, Start: start}
}

// newSource is the source with one opencode process in the fixture's project directory.
func newSource(t *testing.T, f *fixture) *opencode.Source {
	t.Helper()
	return newSourceWith(t, f, []proc.Process{procAt(55813, "/home/u/proj", procStart)})
}

// newSourceWith is the source over a fixed process list (nil lists nothing).
func newSourceWith(t *testing.T, f *fixture, procs []proc.Process) *opencode.Source {
	t.Helper()
	return newSourceListing(t, f, func() ([]proc.Process, error) { return procs, nil })
}

func newSourceListing(t *testing.T, f *fixture, list func() ([]proc.Process, error)) *opencode.Source {
	t.Helper()
	s := opencode.New(opencode.Options{Env: config.Env{"XDG_CACHE_HOME": filepath.Join(f.dir, "cache")}, Homedir: f.dir,
		Now: func() int64 { return now }, ListProcesses: list})
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func discoverOne(t *testing.T, s *opencode.Source, f *fixture) discovery.ProfileDiscovery {
	t.Helper()
	res, err := s.Discover([]config.ProfileDir{f.profile()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Profiles) != 1 {
		t.Fatalf("profiles = %d", len(res.Profiles))
	}
	return res.Profiles[0]
}

func liveIDs(pd discovery.ProfileDiscovery) map[string]discovery.Activity {
	out := map[string]discovery.Activity{}
	for _, d := range pd.Live {
		out[d.SessionID] = d.Activity
	}
	return out
}

func readAll(t *testing.T, r source.TranscriptReader) source.TranscriptRead {
	t.Helper()
	res, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return res
}

func kinds(events []domain.TranscriptEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.EventKind())
	}
	return out
}

func TestDiscover(t *testing.T) {
	f := newFixture(t)
	// Idle for an hour, its process runs and nothing newer in the directory: live. (The others of
	// the directory are older: each process shows one session.)
	f.addSession(sessionSpec{ID: "ses_recent", Title: "Recent work", Model: model, Created: now - hour, Updated: now - hour})
	f.addMessage("ses_recent", "msg_r1", now-hour, now-hour, userData(now-hour, model))
	f.addPart("ses_recent", "msg_r1", "prt_r1", now-50*minute, now-50*minute, textData("hello"))
	// Untouched since the process started: never live.
	f.addSession(sessionSpec{ID: "ses_old", Title: "Old", Model: model, Created: now - 3*hour, Updated: now - 3*hour})
	f.addMessage("ses_old", "msg_o1", now-3*hour, now-3*hour, userData(now-3*hour, model))
	// Touched after the start, but older than ses_recent: not the process's session.
	f.addSession(sessionSpec{ID: "ses_prev", Title: "Previous", Model: model, Created: now - 90*minute, Updated: now - 70*minute})
	// Archived: never live.
	f.addSession(sessionSpec{ID: "ses_arch", Title: "Archived", Model: model, Created: now - minute, Updated: now - minute, Archived: now - minute})
	// Another directory without a process: not live.
	f.addSession(sessionSpec{ID: "ses_other", Title: "Other", Dir: "/home/u/other", Model: model, Created: now - minute, Updated: now - minute})
	// A child session is not a root, and its activity counts as its parent's (in a third
	// directory with its own process).
	f.addSession(sessionSpec{ID: "ses_parent", Title: "Parent", Dir: "/home/u/third", Model: model, Created: now - 3*hour, Updated: now - 3*hour})
	f.addSession(sessionSpec{ID: "ses_child", Parent: "ses_parent", Dir: "/home/u/third", Title: "Scan (@explore subagent)", Agent: "explore", Model: model, Created: now - 5*minute, Updated: now - 5*minute})

	s := newSourceWith(t, f, []proc.Process{procAt(55813, "/home/u/proj", procStart), procAt(55900, "/home/u/third", procStart)})
	pd := discoverOne(t, s, f)
	want := map[string]discovery.Activity{"ses_recent": discovery.ActivityIdle, "ses_parent": discovery.ActivityIdle}
	if got := liveIDs(pd); len(got) != len(want) || got["ses_recent"] != want["ses_recent"] || got["ses_parent"] != want["ses_parent"] {
		t.Errorf("live = %v, want %v", got, want)
	}
	if len(pd.Diagnostics.Issues) != 0 || len(pd.Dead) != 0 {
		t.Errorf("diagnostics = %+v", pd.Diagnostics)
	}
	for _, d := range pd.Live {
		if d.SessionID != "ses_recent" {
			continue
		}
		if d.PID != 55813 || d.ProcessStartedAt != now-2*hour || d.Name != "Recent work" || d.Cwd != "/home/u/proj" || d.Version != "1.19.3" ||
			d.StartedAt != now-hour || d.Key != "oc:ses_recent" ||
			d.Files.TranscriptPath != "opencode:ses_recent" || d.Files.SubagentsDir != "opencode:ses_recent/agents" {
			t.Errorf("record = %+v", d)
		}
	}
	if agents := s.ListAgents("opencode:ses_parent/agents", map[string]string{}); len(agents) != 1 ||
		agents[0].AgentID != "ses_child" || agents[0].TranscriptPath != "opencode:ses_child" || agents[0].MetaPath != "opencode:ses_child/meta" {
		t.Errorf("agents = %+v", agents)
	}
	meta := s.ReadAgentMeta("opencode:ses_child/meta")
	if !meta.OK || meta.Meta.AgentType != "explore" || meta.Meta.Description != "Scan" || meta.Meta.Model != model {
		t.Errorf("meta = %+v", meta)
	}
	if sig, ok := s.FileSignature("opencode:ses_child/meta"); !ok || sig == "" {
		t.Errorf("signature = %q %v", sig, ok)
	}
	if _, ok := s.FileSignature("opencode:nope/meta"); ok {
		t.Error("unknown session must have no signature")
	}
	if _, err := s.OpenTranscript("opencode:nope"); err == nil || opencode.ErrorCode(err) != opencode.CodeRef {
		t.Errorf("unknown ref: %v", err)
	}
	// The store forgets a session: its children go with it.
	s.ForgetSession(f.profile(), "ses_parent")
	if _, err := s.OpenTranscript("opencode:ses_child"); err == nil {
		t.Error("child of a forgotten session must be unknown")
	}
}

// TestDiscoverByProcess: the process list decides what is live.
func TestDiscoverByProcess(t *testing.T) {
	f := newFixture(t)
	f.addSession(sessionSpec{ID: "ses_a", Title: "A", Model: model, Created: now - hour, Updated: now - hour})
	f.addSession(sessionSpec{ID: "ses_b", Title: "B", Model: model, Created: now - 10*minute, Updated: now - 10*minute})
	f.addSession(sessionSpec{ID: "ses_c", Title: "C", Dir: "/home/u/other", Model: model, Created: now - minute, Updated: now - minute})

	t.Run("no process: nothing, and the database is not even needed", func(t *testing.T) {
		s := newSourceWith(t, f, nil)
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 0 {
			t.Errorf("live = %v", got)
		}
		dir := t.TempDir()
		res, err := s.Discover([]config.ProfileDir{{Label: "none", Dir: dir, Provider: domain.Opencode}})
		if err != nil || len(res.Profiles) != 1 || len(res.Profiles[0].Diagnostics.Issues) != 0 {
			t.Errorf("without database: %v %+v", err, res)
		}
	})
	t.Run("one session per process", func(t *testing.T) {
		s := newSourceWith(t, f, []proc.Process{procAt(100, "/home/u/proj", procStart)})
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_b"] == "" {
			t.Errorf("live = %v, want the most recently touched", got)
		}
	})
	t.Run("busy and in progress", func(t *testing.T) {
		f.addMessage("ses_b", "msg_b1", now-9*minute, now-9*minute, assistantData("ses_b", model, now-9*minute, 0, "", tokens{}))
		s := newSourceWith(t, f, []proc.Process{procAt(100, "/home/u/proj", procStart)})
		if got := liveIDs(discoverOne(t, s, f)); got["ses_b"] != discovery.ActivityBusy {
			t.Errorf("live = %v", got)
		}
	})
	t.Run("a session touched before the process start is never its session, without slack", func(t *testing.T) {
		s := newSourceWith(t, f, []proc.Process{procAt(100, "/home/u/proj", time.UnixMilli(now-9*minute+1).UTC())})
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 0 {
			t.Errorf("live = %v", got)
		}
		exact := newSourceWith(t, f, []proc.Process{procAt(100, "/home/u/proj", time.UnixMilli(now-9*minute).UTC())})
		if got := liveIDs(discoverOne(t, exact, f)); len(got) != 1 || got["ses_b"] == "" {
			t.Errorf("touched at the start: %v", got)
		}
	})
	t.Run("two processes in the same directory: each takes a session, newest process first", func(t *testing.T) {
		older, newer := procAt(100, "/home/u/proj", time.UnixMilli(now-2*hour).UTC()), procAt(200, "/home/u/proj", time.UnixMilli(now-30*minute).UTC())
		s := newSourceWith(t, f, []proc.Process{older, newer})
		pd := discoverOne(t, s, f)
		// Both sessions show, but the assignment is a guess: no pid is exposed, so nothing can
		// be closed by it.
		pids := map[string]int{}
		for _, d := range pd.Live {
			pids[d.SessionID] = d.PID
			if d.ProcessStartedAt != 0 {
				t.Errorf("%s: process start exposed: %d", d.SessionID, d.ProcessStartedAt)
			}
		}
		if len(pids) != 2 || pids["ses_b"] != 0 || pids["ses_a"] != 0 {
			t.Errorf("pids = %v", pids)
		}
		// A late write to the older session after the newer process started does not drop the
		// newer session: both still rank.
		f.addMessage("ses_a", "msg_a_late", now-20*minute, now-20*minute, userData(now-20*minute, model))
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 2 || got["ses_a"] == "" || got["ses_b"] == "" {
			t.Errorf("after a late write: %v", got)
		}
		f.deleteSession("ses_a")
		f.addSession(sessionSpec{ID: "ses_a", Title: "A", Model: model, Created: now - hour, Updated: now - hour})
	})
	t.Run("a process per directory, with a symlinked cwd", func(t *testing.T) {
		real := filepath.Join(f.dir, "real")
		link := filepath.Join(f.dir, "link")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		f.addSession(sessionSpec{ID: "ses_link", Title: "Link", Dir: link + "/", Model: model, Created: now - minute, Updated: now - minute})
		s := newSourceWith(t, f, []proc.Process{procAt(300, "/home/u/other", procStart), procAt(400, real, procStart)})
		got := liveIDs(discoverOne(t, s, f))
		if len(got) != 2 || got["ses_c"] == "" || got["ses_link"] == "" {
			t.Errorf("live = %v", got)
		}
		f.deleteSession("ses_link")
	})
	t.Run("a process without a cwd or with an unknown start", func(t *testing.T) {
		blind := proc.Process{PID: 500, Name: "opencode"}
		noStart := proc.Process{PID: 600, Name: "opencode", Cwd: "/home/u/proj"}
		s := newSourceWith(t, f, []proc.Process{blind, noStart})
		pd := discoverOne(t, s, f)
		if got := liveIDs(pd); len(got) != 1 || got["ses_b"] == "" {
			t.Errorf("live = %v", got)
		}
		for _, d := range pd.Live {
			if d.PID != 600 || d.ProcessStartedAt != 0 {
				t.Errorf("%s: pid %d start %d", d.SessionID, d.PID, d.ProcessStartedAt)
			}
		}
	})
	t.Run("the process exits: the session leaves on the next scan", func(t *testing.T) {
		var procs []proc.Process
		s := newSourceListing(t, f, func() ([]proc.Process, error) { return procs, nil })
		procs = []proc.Process{procAt(700, "/home/u/proj", procStart)}
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 {
			t.Errorf("running: %v", got)
		}
		procs = nil
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 0 {
			t.Errorf("exited: %v", got)
		}
	})
	t.Run("two profiles over the same directory: the process is claimed once", func(t *testing.T) {
		s := newSourceWith(t, f, []proc.Process{procAt(800, "/home/u/proj", procStart)})
		second := f.profile()
		second.Label = "oc2"
		res, err := s.Discover([]config.ProfileDir{f.profile(), second})
		if err != nil || len(res.Profiles) != 2 || len(res.Profiles[0].Live) != 1 || len(res.Profiles[1].Live) != 0 {
			t.Errorf("err = %v, live = %d/%d", err, len(res.Profiles[0].Live), len(res.Profiles[1].Live))
		}
	})
	t.Run("old sessions are not examined", func(t *testing.T) {
		// A root far before the process start with a message whose data is not JSON: examining
		// it (json_extract) would fail the scan.
		f.addSession(sessionSpec{ID: "ses_ancient", Title: "Ancient", Model: model, Created: now - 48*hour, Updated: now - 48*hour})
		f.exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES ('msg_bad', 'ses_ancient', ?, ?, 'not json')`, now-48*hour, now-48*hour)
		s := newSourceWith(t, f, []proc.Process{procAt(900, "/home/u/proj", procStart)})
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_b"] == "" {
			t.Errorf("live = %v", got)
		}
		// Not even when it ranks second by time_updated but is already out of reach: a session
		// touched well before the session found.
		f.addSession(sessionSpec{ID: "ses_between", Title: "Between", Model: model, Created: now - hour, Updated: now - 30*minute})
		f.exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES ('msg_bad2', 'ses_between', ?, ?, 'not json')`, now-30*minute, now-30*minute)
		if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_b"] == "" {
			t.Errorf("live = %v", got)
		}
		f.deleteSession("ses_ancient")
		f.deleteSession("ses_between")
	})
	t.Run("the process list fails: a coded error, no sessions", func(t *testing.T) {
		s := newSourceListing(t, f, func() ([]proc.Process, error) { return nil, errors.New("sysctl: boom") })
		res, err := s.Discover([]config.ProfileDir{f.profile()})
		if opencode.ErrorCode(err) != opencode.CodeProcesses || len(res.Profiles) != 1 || len(res.Profiles[0].Live) != 0 {
			t.Errorf("err = %v, res = %+v", err, res)
		}
	})
}

func TestDiscoverWithoutDatabase(t *testing.T) {
	dir := t.TempDir()
	s := opencode.New(opencode.Options{Now: func() int64 { return now }, Homedir: dir, Env: config.Env{},
		ListProcesses: func() ([]proc.Process, error) { return []proc.Process{procAt(1, "/home/u/proj", procStart)}, nil }})
	defer func() { _ = s.Close() }()
	profile := config.ProfileDir{Label: "oc", Dir: dir, Provider: domain.Opencode}
	res, err := s.Discover([]config.ProfileDir{profile})
	if opencode.ErrorCode(err) != opencode.CodeMissing || err.Error() != "opencode: DB_MISSING" {
		t.Errorf("err = %v", err)
	}
	if len(res.Profiles) != 1 || len(res.Profiles[0].Live) != 0 ||
		len(res.Profiles[0].Diagnostics.Issues) != 1 || res.Profiles[0].Diagnostics.Issues[0].Reason != discovery.IssueDatabase {
		t.Errorf("result = %+v", res)
	}
	// Closed: everything fails with the closed code.
	_ = s.Close()
	if _, err := s.Discover([]config.ProfileDir{profile}); opencode.ErrorCode(err) != opencode.CodeClosed {
		t.Errorf("after close: %v", err)
	}
}

func TestRegistryWatch(t *testing.T) {
	f := newFixture(t)
	s := newSource(t, f)
	target, ok := s.RegistryWatch(f.profile())
	if !ok || target != watcher.FilesTarget(f.dir, "opencode.db", "opencode.db-wal") {
		t.Errorf("target = %+v %v", target, ok)
	}
	if _, ok := s.RegistryWatch(config.ProfileDir{Dir: filepath.Join(f.dir, "missing")}); ok {
		t.Error("a missing directory has nothing to watch")
	}
	if _, ok := s.SessionWatch("opencode:x", "opencode:x/agents"); ok {
		t.Error("sessions have no watch of their own")
	}
	if s.Provider() != domain.Opencode {
		t.Errorf("provider = %q", s.Provider())
	}
}

// writeModels writes a catalog with the context window of model.
func writeModels(t *testing.T, f *fixture, context int) {
	t.Helper()
	p := filepath.Join(f.dir, "cache", "opencode", "models.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"anthropic": map[string]any{"models": map[string]any{
		model: map[string]any{"name": "Claude Sonnet 4.5", "limit": map[string]any{"context": context, "output": 64000}},
	}}}
	if err := os.WriteFile(p, []byte(toJSON(t, data)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReaderEventsAndCursor(t *testing.T) {
	f := newFixture(t)
	writeModels(t, f, 1_000_000)
	const sid = "ses_1"
	f.addSession(sessionSpec{ID: sid, Title: "Work", Model: model, Created: now - hour, Updated: now - minute})
	f.addMessage(sid, "msg_u1", now-hour, now-hour, userData(now-hour, model))
	f.addMessage(sid, "msg_a1", now-hour+1, now-hour+1, assistantData(sid, model, now-hour+1, 0, "", tokens{}))
	f.addPart(sid, "msg_a1", "prt_text", now-hour+2, now-hour+2, textData("thinking out loud"))
	f.addPart(sid, "msg_a1", "prt_tool", now-hour+3, now-hour+3,
		toolData("edit", "call_1", "running", map[string]any{"filePath": "/home/u/proj/main.go", "oldString": "a", "newString": "b"}, nil, "", now-hour+3, 0))

	s := newSource(t, f)
	discoverOne(t, s, f)
	r, err := s.OpenTranscript("opencode:" + sid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	first := readAll(t, r)
	if got := kinds(first.Events); strings.Join(got, ",") != "envelope,model_attachment,user_prompt,assistant,assistant" {
		t.Fatalf("first read = %v", got)
	}
	env := first.Events[0].(domain.EnvelopeInfoEvent)
	if env.Cwd != "/home/u/proj" || env.Version != "1.19.3" || env.SessionID != sid {
		t.Errorf("envelope = %+v", env)
	}
	attach := first.Events[1].(domain.ModelAttachmentEvent)
	if attach.ModelID != model || attach.Name != "Claude Sonnet 4.5" || attach.ContextWindow != 1_000_000 {
		t.Errorf("model = %+v", attach)
	}
	open := first.Events[3].(domain.AssistantMessageEvent)
	if open.MessageID != "msg_a1" || open.Model != model || open.Usage != nil {
		t.Errorf("open assistant = %+v", open)
	}
	use := first.Events[4].(domain.AssistantMessageEvent)
	if len(use.ToolUses) != 1 || use.ToolUses[0].ID != "call_1" || use.ToolUses[0].Name != "Edit" ||
		use.ToolUses[0].Target != "/home/u/proj/main.go" || use.MessageID != "msg_a1" {
		t.Errorf("tool use = %+v", use)
	}
	state := reduce.ReduceSessionEvents(reduce.NewSessionState(sid), first.Events)
	if len(state.PendingTools) != 1 || state.PendingTools[0].Name != "Edit" || state.Model.ContextWindow != 1_000_000 {
		t.Errorf("state = %+v", state)
	}

	// Nothing changed: nothing comes back.
	if second := readAll(t, r); len(second.Events) != 0 || second.Reset {
		t.Errorf("second read = %+v", second)
	}

	// The tool completes in place and the message completes with its tokens.
	f.updatePart("prt_tool", now-minute, toolData("edit", "call_1", "completed",
		map[string]any{"filePath": "/home/u/proj/main.go"}, nil, "edited", now-hour+3, now-minute))
	f.updateMessage("msg_a1", now-minute+1, assistantData(sid, model, now-hour+1, now-minute+1, "stop", tokens{input: 100, output: 20, reasoning: 5, cacheRead: 1000, cacheWrite: 50}))
	third := readAll(t, r)
	if got := kinds(third.Events); strings.Join(got, ",") != "assistant,tool_result,assistant,turn_end" {
		t.Fatalf("third read = %v", got)
	}
	done := third.Events[2].(domain.AssistantMessageEvent)
	if done.Usage == nil || *done.Usage != (domain.TokenTotals{Input: 100, Output: 25, CacheRead: 1000, CacheCreate: 50}) || done.StopReason != "stop" {
		t.Errorf("completed = %+v", done)
	}
	state = reduce.ReduceSessionEvents(state, third.Events)
	if len(state.PendingTools) != 0 || state.Totals != (domain.TokenTotals{Input: 100, Output: 25, CacheRead: 1000, CacheCreate: 50}) ||
		state.Context.Used != 1150 {
		t.Errorf("state after completion = %+v", state)
	}
	// The same rows at the same time_updated are not read twice.
	if fourth := readAll(t, r); len(fourth.Events) != 0 {
		t.Errorf("fourth read = %v", kinds(fourth.Events))
	}

	// The session disappears: reset once, then nothing.
	f.deleteSession(sid)
	if res := readAll(t, r); !res.Reset {
		t.Error("gone session must reset")
	}
	if res := readAll(t, r); res.Reset || len(res.Events) != 0 {
		t.Errorf("after reset = %+v", res)
	}
}

func TestReaderWithoutCatalog(t *testing.T) {
	f := newFixture(t)
	const sid = "ses_nc"
	f.addSession(sessionSpec{ID: sid, Title: "No catalog", Model: model, Created: now - minute, Updated: now - minute})
	s := newSource(t, f)
	discoverOne(t, s, f)
	r, err := s.OpenTranscript("opencode:" + sid)
	if err != nil {
		t.Fatal(err)
	}
	res := readAll(t, r)
	if len(res.Events) != 2 {
		t.Fatalf("events = %v", kinds(res.Events))
	}
	if attach := res.Events[1].(domain.ModelAttachmentEvent); attach.ContextWindow != 0 || attach.Name != "" || attach.ModelID != model {
		t.Errorf("attachment without catalog = %+v", attach)
	}
}

// TestTaskLifecycle follows a sub-agent through the parent's task part and the child session.
func TestTaskLifecycle(t *testing.T) {
	f := newFixture(t)
	const parent, child = "ses_p", "ses_c"
	f.addSession(sessionSpec{ID: parent, Title: "Parent", Model: model, Created: now - hour, Updated: now - hour})
	f.addMessage(parent, "msg_a1", now-hour, now-hour, assistantData(parent, model, now-hour, 0, "", tokens{}))
	input := map[string]any{"description": "Scan the repo", "prompt": "Look around", "subagent_type": "explore"}
	f.addPart(parent, "msg_a1", "prt_task", now-hour+1, now-hour+1, toolData("task", "call_task", "pending", input, nil, "", now-hour+1, 0))

	s := newSource(t, f)
	discoverOne(t, s, f)
	r, err := s.OpenTranscript("opencode:" + parent)
	if err != nil {
		t.Fatal(err)
	}
	state := reduce.ReduceSessionEvents(reduce.NewSessionState(parent), readAll(t, r).Events)
	agent, ok := state.Agents["call_task"]
	if !ok || agent.Status != domain.StatusLaunched || agent.Type != "explore" || agent.Description != "Scan the repo" {
		t.Fatalf("launched = %+v (%v)", agent, ok)
	}

	// Running: the child session exists and the part knows it.
	f.addSession(sessionSpec{ID: child, Parent: parent, Agent: "explore", Title: "Scan the repo (@explore subagent)", Model: model, Created: now - hour + 2, Updated: now - hour + 2})
	f.updatePart("prt_task", now-hour+2, toolData("task", "call_task", "running", input,
		map[string]any{"sessionId": child, "parentSessionId": parent, "model": map[string]any{"providerID": "anthropic", "modelID": model}}, "", now-hour+1, 0))
	f.addMessage(child, "msg_c1", now-hour+3, now-hour+3, assistantData(child, model, now-hour+3, 0, "", tokens{}))
	f.addPart(child, "msg_c1", "prt_c1", now-hour+4, now-hour+4, toolData("grep", "call_c1", "running", map[string]any{"pattern": "x", "path": "/home/u/proj"}, nil, "", now-hour+4, 0))
	state = reduce.ReduceSessionEvents(state, readAll(t, r).Events)
	agents := s.ListAgents("opencode:"+parent+"/agents", map[string]string{})
	if len(agents) != 1 || agents[0].AgentID != child {
		t.Fatalf("agents = %+v", agents)
	}
	state = reduce.ApplyAgentMeta(state, child, s.ReadAgentMeta(agents[0].MetaPath).Meta)
	cr, err := s.OpenTranscript(agents[0].TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	state = reduce.ReduceSessionAgentEvents(state, child, readAll(t, cr).Events)
	if len(state.Agents) != 1 {
		t.Fatalf("agents = %+v", state.Agents)
	}
	agent = state.Agents[child]
	if agent.Status != domain.StatusRunning || agent.ToolUseID != "call_task" || agent.Type != "explore" ||
		agent.Model != model || len(agent.PendingTools) != 1 || agent.PendingTools[0].Name != "Grep" || agent.PendingTools[0].Target != "/home/u/proj" {
		t.Errorf("running = %+v", agent)
	}

	// Completed.
	f.updatePart("prt_c1", now-hour+5, toolData("grep", "call_c1", "completed", map[string]any{"pattern": "x"}, nil, "found", now-hour+4, now-hour+5))
	f.updateMessage("msg_c1", now-hour+6, assistantData(child, model, now-hour+3, now-hour+6, "stop", tokens{input: 10, output: 2}))
	f.updatePart("prt_task", now-hour+7, toolData("task", "call_task", "completed", input,
		map[string]any{"sessionId": child, "parentSessionId": parent}, "result text", now-hour+1, now-hour+7))
	state = reduce.ReduceSessionAgentEvents(state, child, readAll(t, cr).Events)
	state = reduce.ReduceSessionEvents(state, readAll(t, r).Events)
	agent = state.Agents[child]
	if agent.Status != domain.StatusCompleted || agent.FinishedAt != now-hour+7 || agent.Totals.Input != 10 || len(agent.PendingTools) != 0 {
		t.Errorf("completed = %+v", agent)
	}

	// A second task that fails.
	f.addPart(parent, "msg_a1", "prt_task2", now-minute, now-minute, toolData("task", "call_task2", "error",
		map[string]any{"description": "Doomed", "prompt": "fail", "subagent_type": "general"},
		map[string]any{"sessionId": "ses_c2"}, "boom", now-2*minute, now-minute))
	state = reduce.ReduceSessionEvents(state, readAll(t, r).Events)
	if failed := state.Agents["ses_c2"]; failed.Status != domain.StatusFailed || failed.Description != "Doomed" || failed.Type != "general" {
		t.Errorf("failed = %+v", failed)
	}
}

func TestReaderToolMapping(t *testing.T) {
	f := newFixture(t)
	const sid = "ses_tools"
	f.addSession(sessionSpec{ID: sid, Title: "Tools", Model: model, Created: now - minute, Updated: now - minute})
	f.addMessage(sid, "msg_1", now-minute, now-minute, assistantData(sid, model, now-minute, 0, "", tokens{}))
	f.addPart(sid, "msg_1", "prt_bash", now-minute+1, now-minute+1, toolData("bash", "call_b", "running",
		map[string]any{"command": "git status && gh pr view 7", "description": "show"}, nil, "", now-minute+1, 0))
	f.addPart(sid, "msg_1", "prt_read", now-minute+2, now-minute+2, toolData("read", "call_r", "pending",
		map[string]any{"filePath": "/home/u/proj/x.go"}, nil, "", now-minute+2, 0))
	f.addPart(sid, "msg_1", "prt_other", now-minute+3, now-minute+3, toolData("todowrite", "call_o", "error",
		map[string]any{"todos": []any{}}, nil, "bad", now-minute+3, now-minute+4))
	f.addPart(sid, "msg_1", "prt_noid", now-minute+5, now-minute+5, map[string]any{"type": "tool", "tool": "glob",
		"state": map[string]any{"status": "running", "input": map[string]any{"pattern": "*"}}})
	f.addPart(sid, "msg_1", "prt_step", now-minute+6, now-minute+6, stepFinishData(tokens{input: 1}))
	s := newSource(t, f)
	discoverOne(t, s, f)
	r, err := s.OpenTranscript("opencode:" + sid)
	if err != nil {
		t.Fatal(err)
	}
	res := readAll(t, r)
	byID := map[string]domain.ToolUseInfo{}
	var results []domain.ToolResultEvent
	for _, e := range res.Events {
		switch ev := e.(type) {
		case domain.AssistantMessageEvent:
			for _, u := range ev.ToolUses {
				byID[u.ID] = u
			}
		case domain.ToolResultEvent:
			results = append(results, ev)
		}
	}
	if b := byID["call_b"]; b.Name != "Bash" || b.CommandCategory != domain.CmdGit || !b.UsesGh || b.Target != "" {
		t.Errorf("bash = %+v", b)
	}
	if rd := byID["call_r"]; rd.Name != "Read" || rd.Target != "/home/u/proj/x.go" {
		t.Errorf("read = %+v", rd)
	}
	if o := byID["call_o"]; o.Name != "todowrite" {
		t.Errorf("other = %+v", o)
	}
	if g := byID["prt_noid"]; g.Name != "Glob" {
		t.Errorf("part without callID must use its id: %+v", byID)
	}
	if len(results) != 1 || results[0].ToolUseID != "call_o" || !results[0].IsError {
		t.Errorf("results = %+v", results)
	}
}

func TestSchemaMismatch(t *testing.T) {
	for _, tc := range []struct{ name, ddl string }{
		{"missing column", `ALTER TABLE part DROP COLUMN time_updated`},
		{"missing table", `DROP TABLE message`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.addSession(sessionSpec{ID: "ses_1", Title: "x", Model: model, Created: now, Updated: now})
			f.exec(tc.ddl)
			s := newSource(t, f)
			res, err := s.Discover([]config.ProfileDir{f.profile()})
			if opencode.ErrorCode(err) != opencode.CodeSchema {
				t.Errorf("err = %v", err)
			}
			if len(res.Profiles) != 1 || len(res.Profiles[0].Live) != 0 || len(res.Profiles[0].Diagnostics.Issues) != 1 {
				t.Errorf("result = %+v", res)
			}
			// Reported again, never cached as fine.
			if _, err := s.Discover([]config.ProfileDir{f.profile()}); opencode.ErrorCode(err) != opencode.CodeSchema {
				t.Errorf("second err = %v", err)
			}
		})
	}
	t.Run("not a database", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "opencode.db"), []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := opencode.New(opencode.Options{Now: func() int64 { return now }, Homedir: dir, Env: config.Env{},
			ListProcesses: func() ([]proc.Process, error) { return []proc.Process{procAt(1, "/home/u/proj", procStart)}, nil }})
		defer func() { _ = s.Close() }()
		_, err := s.Discover([]config.ProfileDir{{Label: "oc", Dir: dir, Provider: domain.Opencode}})
		if code := opencode.ErrorCode(err); code != opencode.CodeQuery && code != opencode.CodeOpen {
			t.Errorf("err = %v", err)
		}
	})
}

// TestForbiddenReferences: the source never names the tables or files that hold what agtop
// must not read, and every SQL statement is in queries.go.
func TestForbiddenReferences(t *testing.T) {
	forbidden := []string{"acc" + "ount", "cred" + "ential", "control_" + "account", "session_" + "share",
		"auth" + ".json", "mcp-" + "auth", "prompt-" + "history"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(data))
		for _, word := range forbidden {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q", file, word)
			}
		}
		if file != "queries.go" {
			for _, kw := range []string{"select ", "from ", "pragma_", "insert ", "update "} {
				if strings.Contains(text, "`"+kw) || strings.Contains(text, "\""+kw) || strings.Contains(text, "\n"+kw) {
					t.Errorf("%s has SQL (%q); only queries.go may", file, kw)
				}
			}
		}
	}
	q, err := os.ReadFile("queries.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"session": true, "message": true, "part": true, "pragma_table_info": true}
	targets := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(string(q), -1)
	if len(targets) == 0 {
		t.Fatal("no FROM/JOIN found in queries.go")
	}
	for _, m := range targets {
		if !allowed[strings.ToLower(m[1])] {
			t.Errorf("queries.go reads %q; only session, message and part are allowed", m[1])
		}
	}
}

// TestLateCommitBelowCursor: a row whose time_updated is below the cursor (stamped before a
// slow commit) is still read, once.
func TestLateCommitBelowCursor(t *testing.T) {
	f := newFixture(t)
	const sid = "ses_late"
	f.addSession(sessionSpec{ID: sid, Title: "Late", Model: model, Created: now - minute, Updated: now - minute})
	f.addMessage(sid, "msg_1", now-minute, now, userData(now-minute, model))
	s := newSource(t, f)
	discoverOne(t, s, f)
	r, err := s.OpenTranscript("opencode:" + sid)
	if err != nil {
		t.Fatal(err)
	}
	if res := readAll(t, r); len(res.Events) != 3 {
		t.Fatalf("first = %v", kinds(res.Events))
	}
	// Committed after the read with a time 1 s behind the cursor.
	f.addMessage(sid, "msg_late", now-1000, now-1000, userData(now-1000, model))
	f.addPart(sid, "msg_late", "prt_late", now-1500, now-1500, toolData("read", "call_late", "running", map[string]any{"filePath": "/x"}, nil, "", now-1500, 0))
	res := readAll(t, r)
	if got := kinds(res.Events); strings.Join(got, ",") != "assistant,user_prompt" {
		t.Errorf("late read = %v", got)
	}
	if res := readAll(t, r); len(res.Events) != 0 {
		t.Errorf("read twice: %v", kinds(res.Events))
	}
	// A change to a row inside the window still comes back once.
	f.updatePart("prt_late", now-500, toolData("read", "call_late", "completed", map[string]any{"filePath": "/x"}, nil, "ok", now-1500, now-500))
	if got := kinds(readAll(t, r).Events); strings.Join(got, ",") != "assistant,tool_result" {
		t.Errorf("updated = %v", got)
	}
	if res := readAll(t, r); len(res.Events) != 0 {
		t.Errorf("read twice after update: %v", kinds(res.Events))
	}
}

// TestDatabaseReplaced: the database replaced by rename (or deleted and recreated) while agtop
// runs is reopened, and what it holds appears.
func TestDatabaseReplaced(t *testing.T) {
	f := newFixture(t)
	f.addSession(sessionSpec{ID: "ses_a", Title: "A", Model: model, Created: now - minute, Updated: now - minute})
	s := newSource(t, f)
	if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_a"] == "" {
		t.Fatalf("before = %v", got)
	}
	// Build a new database elsewhere and rename it over the old one.
	g := newFixture(t)
	g.addSession(sessionSpec{ID: "ses_b", Title: "B", Model: model, Created: now - minute, Updated: now - minute})
	g.exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	g.close()
	f.exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	f.close()
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(f.path + suffix)
	}
	if err := os.Rename(g.path, f.path); err != nil {
		t.Fatal(err)
	}
	if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_b"] == "" {
		t.Errorf("after rename = %v", got)
	}
	// Deleted: missing, then recreated: read again.
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Discover([]config.ProfileDir{f.profile()}); opencode.ErrorCode(err) != opencode.CodeMissing {
		t.Errorf("deleted: %v", err)
	}
	h := newFixture(t)
	h.addSession(sessionSpec{ID: "ses_c", Title: "C", Model: model, Created: now - minute, Updated: now - minute})
	h.exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	h.close()
	if err := os.Rename(h.path, f.path); err != nil {
		t.Fatal(err)
	}
	if got := liveIDs(discoverOne(t, s, f)); len(got) != 1 || got["ses_c"] == "" {
		t.Errorf("recreated = %v", got)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
