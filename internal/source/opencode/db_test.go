package opencode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielfnfaria/agtop/internal/config"
)

func TestDSNEscapesThePath(t *testing.T) {
	got := dsn("/Users/a b/x?y#z/opencode.db")
	if !strings.HasPrefix(got, "file:/Users/a%20b/x%3Fy%23z/opencode.db?mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)") {
		t.Errorf("dsn = %q", got)
	}
}

// TestConnectionIsReadOnly: the connection the source opens cannot write, even in a directory
// with a space and a question mark in its name.
func TestConnectionIsReadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd ?dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.OpencodeDBName)
	w, err := sql.Open("sqlite", "file:"+strings.NewReplacer("?", "%3F", " ", "%20").Replace(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT, version TEXT,
		agent TEXT, model TEXT, time_created INTEGER, time_updated INTEGER, time_archived INTEGER);
		CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		CREATE TABLE part (id TEXT, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
		INSERT INTO session (id, directory, title, version, time_created, time_updated) VALUES ('s1', '/d', 't', 'v', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	p := newProfileDB(config.ProfileDir{Label: "oc", Dir: dir})
	db, err := p.conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	var queryOnly int
	if err := db.QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Errorf("query_only = %d, %v", queryOnly, err)
	}
	for _, stmt := range []string{
		`INSERT INTO session (id, directory, title, version, time_created, time_updated) VALUES ('s2', '/d', 't', 'v', 1, 2)`,
		`UPDATE session SET title = 'x'`,
		`DELETE FROM session`,
		`PRAGMA journal_mode = DELETE`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
		`CREATE TABLE x (a)`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			if strings.HasPrefix(stmt, "PRAGMA") {
				// A pragma that cannot apply returns its current value instead of failing.
				continue
			}
			t.Errorf("%q succeeded on a read-only connection", stmt)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d, %v", n, err)
	}
	row, found, err := selectSession(t.Context(), db, "s1")
	if err != nil || !found || row.Directory != "/d" {
		t.Errorf("selectSession = %+v %v %v", row, found, err)
	}
	p.close()
	if _, err := p.conn(context.Background()); ErrorCode(err) != CodeClosed {
		t.Errorf("after close: %v", err)
	}
}

func TestParseRef(t *testing.T) {
	for ref, want := range map[string][3]string{
		"opencode:ses_1":        {"ses_1", "", "ok"},
		"opencode:ses_1/agents": {"ses_1", "/agents", "ok"},
		"opencode:ses_1/meta":   {"ses_1", "/meta", "ok"},
		"opencode:":             {"", "", ""},
		"opencode:/agents":      {"", "", ""},
		"/tmp/x.jsonl":          {"", "", ""},
	} {
		id, suffix, ok := parseRef(ref)
		if id != want[0] || suffix != want[1] || ok != (want[2] == "ok") {
			t.Errorf("parseRef(%q) = %q %q %v", ref, id, suffix, ok)
		}
	}
	if got := agentDescription("Scan (@explore subagent)", "explore"); got != "Scan" {
		t.Errorf("description = %q", got)
	}
	if got := agentDescription("Scan (@explore subagent)", ""); got != "Scan (@explore subagent)" {
		t.Errorf("description without agent = %q", got)
	}
}
