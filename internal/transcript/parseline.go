// Package transcript turns the raw JSONL lines of a Claude Code transcript into normalized
// domain events. Only metadata leaves it: prompts, commands, tool inputs and outputs never do.
package transcript

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/danielfnfaria/agtop/internal/domain"
)

var (
	// ErrEmptyLine is a blank line.
	ErrEmptyLine = errors.New("empty line")
	// ErrInvalidJSON is a line that is not valid JSON. The parser's own error is never passed
	// on, as it may quote part of the content.
	ErrInvalidJSON = errors.New("invalid json")
)

// ParseLine decodes one JSONL line. Objects decode to map[string]any and numbers to float64.
func ParseLine(line string) (any, error) {
	trimmed := strings.TrimFunc(line, domain.IsJSSpace)
	if trimmed == "" {
		return nil, ErrEmptyLine
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, ErrInvalidJSON
	}
	return value, nil
}
