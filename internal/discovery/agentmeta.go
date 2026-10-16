package discovery

import (
	"encoding/json"
	"errors"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/errno"
	"github.com/danielfnfaria/agtop/internal/transcript"
)

// MaxAgentMetaBytes caps an agent-<id>.meta.json.
const MaxAgentMetaBytes = 64 * 1024

// AgentMetaResult is the outcome of ReadAgentMeta. On failure Code is a short code for the
// error report (never file content): TOO_LARGE, INVALID_JSON, NOT_OBJECT, the errno name of
// the read, or UNKNOWN.
type AgentMetaResult struct {
	OK   bool
	Meta domain.AgentMeta
	Code string
}

// ReadAgentMeta reads and validates an agent-<id>.meta.json returned by ListSubagents (a path
// contained in subagents/), up to MaxAgentMetaBytes.
func ReadAgentMeta(fs FS, file string) AgentMetaResult {
	raw, err := fs.ReadFile(file, MaxAgentMetaBytes)
	if err != nil {
		if errors.Is(err, ErrFileTooLarge) {
			return AgentMetaResult{Code: "TOO_LARGE"}
		}
		return AgentMetaResult{Code: readErrorCode(err)}
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return AgentMetaResult{Code: "INVALID_JSON"}
	}
	meta, ok := transcript.ParseAgentMeta(value)
	if !ok {
		return AgentMetaResult{Code: "NOT_OBJECT"}
	}
	return AgentMetaResult{OK: true, Meta: meta}
}

func readErrorCode(err error) string {
	if code := errno.Code(err); code != "" {
		return code
	}
	return "UNKNOWN"
}

// StatSignature is "size:mtimeMs" of a file; false if the stat fails or the fs reports neither.
func StatSignature(fs FS, file string) (string, bool) {
	info, err := fs.Stat(file)
	if err != nil {
		return "", false
	}
	return info.Signature()
}
