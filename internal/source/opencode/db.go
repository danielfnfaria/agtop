package opencode

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // pure Go sqlite driver ("sqlite"), CGO-free

	"github.com/danielfnfaria/agtop/internal/config"
)

// Error codes of the source (store errors show the code, never the message of the driver).
const (
	// CodeMissing: <dir>/opencode.db does not exist.
	CodeMissing = "DB_MISSING"
	// CodeOpen: the database could not be opened read-only.
	CodeOpen = "DB_OPEN"
	// CodeSchema: a table or column the queries need is not there (another opencode version).
	CodeSchema = "DB_SCHEMA"
	// CodeQuery: a query failed (locked beyond the busy timeout, corrupt file...).
	CodeQuery = "DB_QUERY"
	// CodeClosed: the source was closed (the store stopped).
	CodeClosed = "DB_CLOSED"
	// CodeRef: a transcript reference the source did not hand out.
	CodeRef = "BAD_REF"
)

// Error is a failure of the source: its text is the code alone, so it can be shown as is. The
// cause is kept for debugging (Unwrap) and never enters the text.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return "opencode: " + e.Code }
func (e *Error) Unwrap() error { return e.Err }

func codedErr(code string, err error) error { return &Error{Code: code, Err: err} }

// ErrorCode is the code of an Error in err's chain, "" for other errors.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// dsn is the read-only connection string of path: a file: URI (path percent-escaped) opened
// with mode=ro and, on every connection, busy_timeout and query_only. Nothing here can write.
func dsn(path string) string {
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)"
}

// profileDB is the lazy, single read-only connection to the database of one profile. It is
// opened on first use, verified once against the schema the queries need, reopened when the
// file at the path is no longer the one it was opened on, and closed with the source.
type profileDB struct {
	profile config.ProfileDir
	path    string

	mu      sync.Mutex
	db      *sql.DB
	checked bool
	closed  bool
	// ident is the file the handle was opened on (device and inode).
	ident os.FileInfo
}

func newProfileDB(profile config.ProfileDir) *profileDB {
	return &profileDB{profile: profile, path: filepath.Join(profile.Dir, config.OpencodeDBName)}
}

// conn is the open connection, opening it if needed. The file is checked on every call: gone,
// the handle is dropped (a missing file is not cached: the next call tries again, opencode may
// create it later); replaced (deleted and recreated, or renamed over), the handle, which would
// keep reading the old inode, is reopened on the new file.
func (p *profileDB) conn() (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, codedErr(CodeClosed, nil)
	}
	info, err := os.Stat(p.path)
	if err != nil || !info.Mode().IsRegular() {
		p.dropLocked()
		return nil, codedErr(CodeMissing, err)
	}
	if p.db != nil && !os.SameFile(p.ident, info) {
		p.dropLocked()
	}
	if p.db == nil {
		db, err := sql.Open("sqlite", dsn(p.path))
		if err != nil {
			return nil, codedErr(CodeOpen, err)
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		p.db = db
		p.ident = info
		p.checked = false
	}
	if !p.checked {
		if err := checkSchema(p.db); err != nil {
			if ErrorCode(err) != CodeSchema {
				// Not reachable right now (locked, gone, not a database): forget the handle so a
				// later call starts over; a schema mismatch is kept and reported on every use.
				p.dropLocked()
			}
			return nil, err
		}
		p.checked = true
	}
	return p.db, nil
}

// dropLocked closes the handle, if any; the next conn opens the file again.
func (p *profileDB) dropLocked() {
	if p.db != nil {
		_ = p.db.Close()
	}
	p.db, p.ident, p.checked = nil, nil, false
}

func (p *profileDB) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.dropLocked()
}
