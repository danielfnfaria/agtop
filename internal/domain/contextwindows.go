package domain

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
)

// Context window sizes as Claude Code reports them to the status line command, and the small
// file `agtop statusline-hook` keeps them in (<configDir>/agtop/context.json, next to
// usage.json, which stays free of ids). One entry per session: the full model id and the size.

const (
	// ContextFileName is the context file, inside UsageDirName.
	ContextFileName = "context.json"
	// MaxContextFileBytes is the size limit when reading the context file.
	MaxContextFileBytes = 64 * 1024
	// ContextFileVersion is the version of the context file format.
	ContextFileVersion = 1
	// MaxContextSessions is the most sessions kept; beyond it the oldest entries go.
	MaxContextSessions = 64
	// ContextEntryMaxAgeMs is the age beyond which entries are dropped on write.
	ContextEntryMaxAgeMs = 30 * 24 * 60 * 60_000
	// ContextRewriteAfterMs is the age after which an unchanged entry is still rewritten.
	ContextRewriteAfterMs = 10 * 60_000

	minWindow = 1000
	maxWindow = 100_000_000
)

var (
	sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	modelIDRE   = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,64}$`)
)

// ContextReport is what a status line payload says about the context window of one session.
type ContextReport struct {
	SessionID string
	// Model is the full model id, variant suffix included (`claude-x[1m]` and `claude-x` differ).
	Model string
	Size  int
}

// ContextEntry is one session in the context file.
type ContextEntry struct {
	Model string `json:"model"`
	Size  int    `json:"size"`
	// At is the epoch ms of the last write of this entry.
	At int64 `json:"at"`
}

// ContextFile is the content of <configDir>/agtop/context.json.
type ContextFile struct {
	Version int `json:"version"`
	// UpdatedAt is the epoch ms of the last write.
	UpdatedAt int64 `json:"updatedAt"`
	// Sessions by session id.
	Sessions map[string]ContextEntry `json:"sessions"`
}

func asWindowSize(v any) (int, bool) {
	f, ok := asNumber(v)
	if !ok || f != math.Trunc(f) || f < minWindow || f > maxWindow {
		return 0, false
	}
	return int(f), true
}

func asModelID(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && modelIDRE.MatchString(s)
}

// ExtractContextWindow reads the session, model and window size of a status line payload
// (`session_id`, `model.id`, `context_window.context_window_size`). It returns false when any is
// missing or invalid. `context_window.used_percentage` is deliberately not used.
func ExtractContextWindow(payload any) (ContextReport, bool) {
	p, ok := asRecord(payload)
	if !ok {
		return ContextReport{}, false
	}
	sessionID, _ := p["session_id"].(string)
	model, okModel := asRecord(p["model"])
	cw, okCW := asRecord(p["context_window"])
	if !sessionIDRE.MatchString(sessionID) || !okModel || !okCW {
		return ContextReport{}, false
	}
	size, ok := asWindowSize(cw["context_window_size"])
	if !ok {
		return ContextReport{}, false
	}
	id, ok := asModelID(model["id"])
	if !ok {
		return ContextReport{}, false
	}
	return ContextReport{SessionID: sessionID, Model: id, Size: size}, true
}

func parseContextEntry(v any) (ContextEntry, bool) {
	m, ok := asRecord(v)
	if !ok || !onlyKeys(m, "model", "size", "at") {
		return ContextEntry{}, false
	}
	model, okM := asModelID(m["model"])
	size, okS := asWindowSize(m["size"])
	at, okA := asEpoch(m["at"])
	if !okM || !okS || !okA {
		return ContextEntry{}, false
	}
	return ContextEntry{model, size, at}, true
}

// ParseContextFile strictly validates the context file (already decoded JSON): the known keys
// only, with the expected types and ranges, at most MaxContextSessions sessions.
func ParseContextFile(value any) (ContextFile, bool) {
	m, ok := asRecord(value)
	if !ok || !onlyKeys(m, "version", "updatedAt", "sessions") {
		return ContextFile{}, false
	}
	version, okV := asNumber(m["version"])
	updatedAt, okU := asEpoch(m["updatedAt"])
	raw, okR := asRecord(m["sessions"])
	if !okV || version != ContextFileVersion || !okU || !okR || len(raw) > MaxContextSessions {
		return ContextFile{}, false
	}
	sessions := make(map[string]ContextEntry, len(raw))
	for id, v := range raw {
		entry, ok := parseContextEntry(v)
		if !sessionIDRE.MatchString(id) || !ok {
			return ContextFile{}, false
		}
		sessions[id] = entry
	}
	return ContextFile{ContextFileVersion, updatedAt, sessions}, true
}

// UnmarshalContextFile decodes and validates the bytes of a context file.
func UnmarshalContextFile(data []byte) (ContextFile, bool) {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return ContextFile{}, false
	}
	return ParseContextFile(v)
}

// MarshalContextFile is the JSON form of file.
func MarshalContextFile(file ContextFile) ([]byte, error) {
	return json.Marshal(file)
}

type sessionEntry struct {
	id    string
	entry ContextEntry
}

// UpsertContextEntry returns current with the report of a session recorded at now, or false
// when nothing needs writing: the session's entry is the same and younger than
// ContextRewriteAfterMs. On a write, entries older than ContextEntryMaxAgeMs go, and so do the
// oldest ones beyond MaxContextSessions.
func UpsertContextEntry(current *ContextFile, report ContextReport, now int64) (ContextFile, bool) {
	var existing map[string]ContextEntry
	if current != nil {
		existing = current.Sessions
	}
	if prev, ok := existing[report.SessionID]; ok && prev.Model == report.Model && prev.Size == report.Size &&
		now-prev.At >= 0 && now-prev.At < ContextRewriteAfterMs {
		return ContextFile{}, false
	}
	kept := make([]sessionEntry, 0, len(existing))
	for id, e := range existing {
		if id != report.SessionID && now-e.At <= ContextEntryMaxAgeMs {
			kept = append(kept, sessionEntry{id, e})
		}
	}
	// Newest first; the entry being written always survives.
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].entry.At != kept[j].entry.At {
			return kept[i].entry.At > kept[j].entry.At
		}
		return kept[i].id < kept[j].id
	})
	if len(kept) > MaxContextSessions-1 {
		kept = kept[:MaxContextSessions-1]
	}
	sessions := make(map[string]ContextEntry, len(kept)+1)
	for _, k := range kept {
		sessions[k.id] = k.entry
	}
	sessions[report.SessionID] = ContextEntry{report.Model, report.Size, now}
	return ContextFile{ContextFileVersion, now, sessions}, true
}

// ContextReports is what the context file says, for the viewer. Sizes of 0 mean none.
type ContextReports struct {
	file   ContextFile
	newest map[string]ContextEntry
}

// NoReports is the value for no context file.
var NoReports = ContextReports{}

// NewContextReports builds the lookups over file.
func NewContextReports(file ContextFile) ContextReports {
	ids := make([]string, 0, len(file.Sessions))
	for id := range file.Sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	newest := make(map[string]ContextEntry)
	for _, id := range ids {
		e := file.Sessions[id]
		if best, ok := newest[e.Model]; !ok || e.At > best.At {
			newest[e.Model] = e
		}
	}
	return ContextReports{file, newest}
}

// ForSession is the window reported by sessionID, if its entry is for this model (full id,
// suffix included).
func (r ContextReports) ForSession(sessionID, modelID string) int {
	e, ok := r.file.Sessions[sessionID]
	if ok && modelID != "" && e.Model == modelID {
		return e.Size
	}
	return 0
}

// ForModel is the window of the most recent entry for this model (full id, suffix included).
func (r ContextReports) ForModel(modelID string) int {
	if modelID == "" {
		return 0
	}
	return r.newest[modelID].Size
}

// SameContextWindows reports whether two context files tell the same windows: the same
// sessions with the same model and size, and the same most recent size per model. It ignores
// order, At and UpdatedAt (a hook rewrite that changes nothing else is not a change).
func SameContextWindows(a, b ContextFile) bool {
	if len(a.Sessions) != len(b.Sessions) {
		return false
	}
	for id, ea := range a.Sessions {
		eb, ok := b.Sessions[id]
		if !ok || eb.Model != ea.Model || eb.Size != ea.Size {
			return false
		}
	}
	ra, rb := NewContextReports(a), NewContextReports(b)
	for _, e := range a.Sessions {
		if ra.ForModel(e.Model) != rb.ForModel(e.Model) {
			return false
		}
	}
	return true
}
