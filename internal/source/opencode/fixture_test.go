package opencode_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/danielfnfaria/agtop/internal/config"
)

// fixture is a synthetic opencode database (same tables, columns and JSON shapes as opencode
// 1.19, invented data) in a temporary directory, written through a read-write connection the
// tests keep open (WAL mode, like opencode).
type fixture struct {
	t    *testing.T
	dir  string
	path string
	db   *sql.DB
}

const fixtureSchema = `
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, name TEXT,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL,
  directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL, agent TEXT, model TEXT, cost REAL,
  tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER,
  tokens_cache_write INTEGER, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  time_compacting INTEGER, time_archived INTEGER, workspace_id TEXT, permission TEXT);
CREATE INDEX session_parent_idx ON session(parent_id);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE INDEX message_session_idx ON message(session_id, time_created, id);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE INDEX part_session_idx ON part(session_id);
`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, dir: dir, path: filepath.Join(dir, config.OpencodeDBName)}
	f.db = f.openWriter()
	f.exec(`PRAGMA journal_mode=WAL`)
	f.exec(fixtureSchema)
	f.exec(`INSERT INTO project VALUES ('prj_1', '/home/u/proj', 'git', 'proj', 1, 1)`)
	t.Cleanup(f.close)
	return f
}

func (f *fixture) openWriter() *sql.DB {
	f.t.Helper()
	db, err := sql.Open("sqlite", "file:"+f.path+"?_pragma=busy_timeout(2000)")
	if err != nil {
		f.t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func (f *fixture) close() {
	if f.db != nil {
		_ = f.db.Close()
		f.db = nil
	}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fixture) profile() config.ProfileDir {
	return config.ProfileDir{Label: "oc", Dir: f.dir, Source: config.SourceConfig, Provider: "opencode"}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sessionSpec is a row of session.
type sessionSpec struct {
	ID        string
	Parent    string
	Title     string
	Dir       string
	Agent     string
	Model     string // model id; provider is "anthropic"
	Created   int64
	Updated   int64
	Archived  int64
	Version   string
	NullModel bool
}

func (f *fixture) addSession(s sessionSpec) {
	f.t.Helper()
	var parent, agent, model, archived any
	if s.Parent != "" {
		parent = s.Parent
	}
	if s.Agent != "" {
		agent = s.Agent
	}
	if !s.NullModel {
		model = toJSON(f.t, map[string]any{"id": s.Model, "providerID": "anthropic", "variant": nil})
	}
	if s.Archived != 0 {
		archived = s.Archived
	}
	if s.Version == "" {
		s.Version = "1.19.3"
	}
	if s.Dir == "" {
		s.Dir = "/home/u/proj"
	}
	f.exec(`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, agent, model, cost,
		tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write,
		time_created, time_updated, time_compacting, time_archived, workspace_id, permission)
		VALUES (?, 'prj_1', ?, 'slug', ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, 0, ?, ?, NULL, ?, NULL, NULL)`,
		s.ID, parent, s.Dir, s.Title, s.Version, agent, model, s.Created, s.Updated, archived)
}

func (f *fixture) deleteSession(id string) {
	f.exec(`DELETE FROM part WHERE session_id = ?`, id)
	f.exec(`DELETE FROM message WHERE session_id = ?`, id)
	f.exec(`DELETE FROM session WHERE id = ?`, id)
}

func (f *fixture) addMessage(sessionID, id string, created, updated int64, data map[string]any) {
	f.t.Helper()
	f.exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		id, sessionID, created, updated, toJSON(f.t, data))
}

func (f *fixture) updateMessage(id string, updated int64, data map[string]any) {
	f.t.Helper()
	f.exec(`UPDATE message SET time_updated = ?, data = ? WHERE id = ?`, updated, toJSON(f.t, data), id)
}

func (f *fixture) addPart(sessionID, messageID, id string, created, updated int64, data map[string]any) {
	f.t.Helper()
	f.exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		id, messageID, sessionID, created, updated, toJSON(f.t, data))
}

func (f *fixture) updatePart(id string, updated int64, data map[string]any) {
	f.t.Helper()
	f.exec(`UPDATE part SET time_updated = ?, data = ? WHERE id = ?`, updated, toJSON(f.t, data), id)
}

// JSON shapes of message.data and part.data.

type tokens struct{ input, output, reasoning, cacheRead, cacheWrite int }

func assistantData(sessionID, model string, created, completed int64, finish string, tk tokens) map[string]any {
	d := map[string]any{
		"parentID": "msg_user", "role": "assistant", "mode": "build", "agent": "build",
		"path":    map[string]any{"cwd": "/home/u/proj", "root": "/home/u/proj"},
		"cost":    0.01,
		"tokens":  map[string]any{"input": tk.input, "output": tk.output, "reasoning": tk.reasoning, "cache": map[string]any{"read": tk.cacheRead, "write": tk.cacheWrite}},
		"modelID": model, "providerID": "anthropic",
		"time": map[string]any{"created": created},
	}
	if completed != 0 {
		d["time"] = map[string]any{"created": created, "completed": completed}
	}
	if finish != "" {
		d["finish"] = finish
	}
	_ = sessionID
	return d
}

func userData(created int64, model string) map[string]any {
	return map[string]any{
		"role": "user", "time": map[string]any{"created": created}, "agent": "build",
		"model": map[string]any{"providerID": "anthropic", "modelID": model},
	}
}

func textData(text string) map[string]any {
	return map[string]any{"type": "text", "text": text, "time": map[string]any{"start": 1, "end": 2}}
}

func reasoningData(text string) map[string]any {
	return map[string]any{"type": "reasoning", "text": text, "time": map[string]any{"start": 1, "end": 2}}
}

func stepFinishData(tk tokens) map[string]any {
	return map[string]any{"type": "step-finish", "reason": "stop", "cost": 0.1,
		"tokens": map[string]any{"input": tk.input, "output": tk.output, "reasoning": tk.reasoning, "cache": map[string]any{"read": tk.cacheRead, "write": tk.cacheWrite}}}
}

// toolData is a tool part: state.status pending/running/completed/error with input, output,
// title, metadata and times.
func toolData(tool, callID, status string, input, metadata map[string]any, output string, start, end int64) map[string]any {
	state := map[string]any{"status": status, "input": input, "time": map[string]any{"start": start}}
	if end != 0 {
		state["time"] = map[string]any{"start": start, "end": end}
	}
	if status == "completed" || status == "error" {
		state["output"] = output
		state["title"] = "done"
	}
	if status == "error" {
		state["error"] = output
	}
	if metadata != nil {
		state["metadata"] = metadata
	}
	return map[string]any{"type": "tool", "tool": tool, "callID": callID, "state": state}
}
