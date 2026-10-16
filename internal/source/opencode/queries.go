package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// Every SQL statement of the source lives here. They touch the tables session, message and part
// only, select metadata (json_extract of a few keys) and never a message or part body, and every
// list is bounded by a LIMIT.

// Required columns per table (the schema check reads sqlite's own table info, not the rows).
var requiredColumns = map[string][]string{
	"session": {"id", "parent_id", "directory", "title", "version", "agent", "model", "time_created", "time_updated", "time_archived"},
	"message": {"id", "session_id", "time_created", "time_updated", "data"},
	"part":    {"id", "message_id", "session_id", "time_created", "time_updated", "data"},
}

const (
	// maxRoots is the most root sessions considered per scan (the most recently updated).
	maxRoots = 200
	// maxChildren is the most child sessions listed per session.
	maxChildren = 200
	// pageSize is the rows of a message/part page.
	pageSize = 500
	// maxPages bounds one Read (the rest comes on the next one).
	maxPages = 40
	// commandClip is how much of a bash command the classifier sees (it clips at 4000 itself).
	commandClip = 4000
)

func checkSchema(db *sql.DB) error {
	for table, columns := range requiredColumns {
		rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			return codedErr(CodeQuery, err)
		}
		have := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return codedErr(CodeQuery, err)
			}
			have[name] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return codedErr(CodeQuery, err)
		}
		_ = rows.Close()
		if len(have) == 0 {
			return codedErr(CodeSchema, fmt.Errorf("missing table %s", table))
		}
		for _, c := range columns {
			if !have[c] {
				return codedErr(CodeSchema, fmt.Errorf("missing column %s.%s", table, c))
			}
		}
	}
	return nil
}

// rootRow is a root session with the signals of its liveness.
type rootRow struct {
	ID          string
	Directory   string
	Title       string
	Version     string
	ModelID     string
	TimeCreated int64
	TimeUpdated int64
	// LastActivity is the latest time_updated of the session, its messages, its parts and its
	// child sessions.
	LastActivity int64
	// InProgress: an assistant message without time.completed or a tool part pending/running,
	// updated within the stuck window.
	InProgress bool
}

var queryRoots = `
SELECT s.id, s.directory, s.title, s.version, s.time_created, s.time_updated,
  json_extract(s.model, '$.id'),
  COALESCE((SELECT MAX(m.time_updated) FROM message m WHERE m.session_id = s.id), 0),
  COALESCE((SELECT MAX(p.time_updated) FROM part p WHERE p.session_id = s.id), 0),
  COALESCE((SELECT MAX(c.time_updated) FROM session c WHERE c.parent_id = s.id), 0),
  EXISTS(SELECT 1 FROM message m WHERE m.session_id = s.id AND m.time_updated >= ?2
    AND json_extract(m.data, '$.role') = 'assistant' AND json_extract(m.data, '$.time.completed') IS NULL),
  EXISTS(SELECT 1 FROM part p WHERE p.session_id = s.id AND p.time_updated >= ?2
    AND json_extract(p.data, '$.type') = 'tool' AND json_extract(p.data, '$.state.status') IN ('pending', 'running'))
FROM session s
WHERE s.parent_id IS NULL AND s.time_archived IS NULL AND s.time_updated >= ?1
ORDER BY s.time_updated DESC
LIMIT ` + itoa(maxRoots)

// selectRoots lists the root sessions updated since candidateSince, with their activity; the
// in-progress signals only count when updated since stuckSince.
func selectRoots(db *sql.DB, candidateSince, stuckSince int64) ([]rootRow, error) {
	rows, err := db.Query(queryRoots, candidateSince, stuckSince)
	if err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	defer rows.Close()
	var out []rootRow
	for rows.Next() {
		var r rootRow
		var directory, title, version, model sql.NullString
		var created, updated, msgAt, partAt, childAt sql.NullInt64
		var openMsg, openTool bool
		if err := rows.Scan(&r.ID, &directory, &title, &version, &created, &updated, &model,
			&msgAt, &partAt, &childAt, &openMsg, &openTool); err != nil {
			return nil, codedErr(CodeQuery, err)
		}
		r.Directory, r.Title, r.Version, r.ModelID = directory.String, title.String, version.String, model.String
		r.TimeCreated, r.TimeUpdated = created.Int64, updated.Int64
		r.LastActivity = max(r.TimeUpdated, msgAt.Int64, partAt.Int64, childAt.Int64)
		r.InProgress = openMsg || openTool
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	return out, nil
}

// sessionRow is the metadata of one session (root or child).
type sessionRow struct {
	ID          string
	ParentID    string
	Directory   string
	Title       string
	Version     string
	Agent       string
	ModelID     string
	ProviderID  string
	TimeCreated int64
	TimeUpdated int64
}

const querySession = `
SELECT id, parent_id, directory, title, version, agent, json_extract(model, '$.id'), json_extract(model, '$.providerID'),
  time_created, time_updated
FROM session WHERE id = ?1 LIMIT 1`

// selectSession reads one session; found is false when there is no such row.
func selectSession(db *sql.DB, id string) (row sessionRow, found bool, err error) {
	var parent, directory, title, version, agent, model, provider sql.NullString
	var created, updated sql.NullInt64
	err = db.QueryRow(querySession, id).Scan(&row.ID, &parent, &directory, &title, &version, &agent, &model, &provider, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRow{}, false, nil
	}
	if err != nil {
		return sessionRow{}, false, codedErr(CodeQuery, err)
	}
	row.ParentID, row.Directory, row.Title, row.Version, row.Agent, row.ModelID, row.ProviderID =
		parent.String, directory.String, title.String, version.String, agent.String, model.String, provider.String
	row.TimeCreated, row.TimeUpdated = created.Int64, updated.Int64
	return row, true, nil
}

var queryChildren = `
SELECT id, time_updated FROM session WHERE parent_id = ?1 ORDER BY time_created, id LIMIT ` + itoa(maxChildren)

// childRef is a child session: its id and change signature.
type childRef struct {
	ID          string
	TimeUpdated int64
}

func selectChildren(db *sql.DB, parentID string) ([]childRef, error) {
	rows, err := db.Query(queryChildren, parentID)
	if err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	defer rows.Close()
	var out []childRef
	for rows.Next() {
		var c childRef
		var updated sql.NullInt64
		if err := rows.Scan(&c.ID, &updated); err != nil {
			return nil, codedErr(CodeQuery, err)
		}
		c.TimeUpdated = updated.Int64
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	return out, nil
}

// messageRow is the metadata of a message: role, model, times, finish reason and tokens. The
// content (parts) is never selected.
type messageRow struct {
	ID          string
	TimeCreated int64
	TimeUpdated int64
	Role        string
	Finish      string
	ModelID     string
	Created     int64
	Completed   int64
	HasError    bool
	Input       int64
	Output      int64
	Reasoning   int64
	CacheRead   int64
	CacheWrite  int64
}

var queryMessages = `
SELECT id, time_created, time_updated,
  json_extract(data, '$.role'), json_extract(data, '$.finish'), json_extract(data, '$.modelID'),
  json_extract(data, '$.time.created'), json_extract(data, '$.time.completed'),
  json_extract(data, '$.error') IS NOT NULL,
  json_extract(data, '$.tokens.input'), json_extract(data, '$.tokens.output'), json_extract(data, '$.tokens.reasoning'),
  json_extract(data, '$.tokens.cache.read'), json_extract(data, '$.tokens.cache.write')
FROM message
WHERE session_id = ?1 AND (time_updated > ?2 OR (time_updated = ?2 AND id > ?3))
ORDER BY time_updated, id
LIMIT ` + itoa(pageSize)

// selectMessages is one page of the messages of a session after the cursor (time_updated, id):
// rows with the cursor time are taken when their id is above afterID ("" takes them all).
func selectMessages(db *sql.DB, sessionID string, since int64, afterID string) ([]messageRow, error) {
	rows, err := db.Query(queryMessages, sessionID, since, afterID)
	if err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	defer rows.Close()
	var out []messageRow
	for rows.Next() {
		var m messageRow
		var role, finish, model sql.NullString
		var created, updated, tCreated, tCompleted, in, outT, reasoning, cRead, cWrite sql.NullInt64
		if err := rows.Scan(&m.ID, &created, &updated, &role, &finish, &model, &tCreated, &tCompleted, &m.HasError,
			&in, &outT, &reasoning, &cRead, &cWrite); err != nil {
			return nil, codedErr(CodeQuery, err)
		}
		m.TimeCreated, m.TimeUpdated = created.Int64, updated.Int64
		m.Role, m.Finish, m.ModelID = role.String, finish.String, model.String
		m.Created, m.Completed = tCreated.Int64, tCompleted.Int64
		m.Input, m.Output, m.Reasoning, m.CacheRead, m.CacheWrite = in.Int64, outT.Int64, reasoning.Int64, cRead.Int64, cWrite.Int64
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	return out, nil
}

// partRow is the metadata of a tool part. Command is the only piece of input that leaves the
// database, clipped, and only to be classified and dropped.
type partRow struct {
	ID             string
	MessageID      string
	TimeCreated    int64
	TimeUpdated    int64
	Tool           string
	CallID         string
	Status         string
	FilePath       string
	Path           string
	Command        string
	Description    string
	SubagentType   string
	ChildSessionID string
	Start          int64
	End            int64
}

var queryToolParts = `
SELECT id, message_id, time_created, time_updated,
  json_extract(data, '$.tool'), json_extract(data, '$.callID'), json_extract(data, '$.state.status'),
  json_extract(data, '$.state.input.filePath'), json_extract(data, '$.state.input.path'),
  substr(json_extract(data, '$.state.input.command'), 1, ` + itoa(commandClip) + `),
  json_extract(data, '$.state.input.description'), json_extract(data, '$.state.input.subagent_type'),
  json_extract(data, '$.state.metadata.sessionId'),
  json_extract(data, '$.state.time.start'), json_extract(data, '$.state.time.end')
FROM part
WHERE session_id = ?1 AND (time_updated > ?2 OR (time_updated = ?2 AND id > ?3))
  AND json_extract(data, '$.type') = 'tool'
ORDER BY time_updated, id
LIMIT ` + itoa(pageSize)

// selectToolParts is one page of the tool parts of a session after the cursor (see
// selectMessages).
func selectToolParts(db *sql.DB, sessionID string, since int64, afterID string) ([]partRow, error) {
	rows, err := db.Query(queryToolParts, sessionID, since, afterID)
	if err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	defer rows.Close()
	var out []partRow
	for rows.Next() {
		var p partRow
		var message, tool, callID, status, filePath, path, command, description, subagent, child sql.NullString
		var created, updated, start, end sql.NullInt64
		if err := rows.Scan(&p.ID, &message, &created, &updated, &tool, &callID, &status, &filePath, &path, &command,
			&description, &subagent, &child, &start, &end); err != nil {
			return nil, codedErr(CodeQuery, err)
		}
		p.MessageID, p.TimeCreated, p.TimeUpdated = message.String, created.Int64, updated.Int64
		p.Tool, p.CallID, p.Status = tool.String, callID.String, status.String
		p.FilePath, p.Path, p.Command = filePath.String, path.String, command.String
		p.Description, p.SubagentType, p.ChildSessionID = description.String, subagent.String, child.String
		p.Start, p.End = start.Int64, end.Int64
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, codedErr(CodeQuery, err)
	}
	return out, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
